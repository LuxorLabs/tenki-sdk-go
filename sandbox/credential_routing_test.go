package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A recording data-plane node capturing the credential header of every Stat,
// plus a scriptable engine whose CreateSessionCredential can hand back a
// different endpoint/credential pair on each call.

type reproNode struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler

	name string
	// statErr, when set, is returned for every Stat before recording succeeds.
	statErr func() error

	mu   sync.Mutex
	seen []string // credential header values, in arrival order
}

func (n *reproNode) Stat(
	_ context.Context,
	req *connect.Request[sandboxv1.SandboxSessionDataPlaneServiceStatRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceStatResponse], error) {
	n.mu.Lock()
	n.seen = append(n.seen, req.Header().Get(dataPlaneCredentialHeader))
	n.mu.Unlock()
	if n.statErr != nil {
		if err := n.statErr(); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceStatResponse{
		Response: &sandboxv1.StatResponse{Exists: true, Size: 1},
	}), nil
}

func (n *reproNode) credentials() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.seen...)
}

func newReproNode(t *testing.T, name string, statErr func() error) (*reproNode, string) {
	t.Helper()
	node := &reproNode{name: name, statErr: statErr}
	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(node)
	mux.Handle(path, svc)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	return node, server.URL
}

type credentialGrant struct {
	endpoint    string
	credential  string
	expiresIn   time.Duration
	routeStatus sandboxv1.DataPlaneRouteStatus
}

type reproEngine struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	sessionID string

	// grants are consumed in order; the last one repeats once exhausted.
	grants []credentialGrant

	// beforeCredential, when set, runs before each CreateSessionCredential
	// reply, letting a test hold the call open across a concurrent Close.
	beforeCredential func(n int)

	credentialCalls atomic.Int32
	terminateCalls  atomic.Int32
}

func (e *reproEngine) CreateSessionCredential(
	context.Context,
	*connect.Request[sandboxv1.CreateSessionCredentialRequest],
) (*connect.Response[sandboxv1.CreateSessionCredentialResponse], error) {
	n := int(e.credentialCalls.Add(1))
	if e.beforeCredential != nil {
		e.beforeCredential(n)
	}
	grant := e.grants[min(n-1, len(e.grants)-1)]
	expires := grant.expiresIn
	if expires == 0 {
		expires = time.Hour
	}
	return connect.NewResponse(&sandboxv1.CreateSessionCredentialResponse{
		Credential: &sandboxv1.SessionCredential{
			Credential: grant.credential,
			ExpiresAt:  timestamppb.New(time.Now().Add(expires)),
		},
		DataPlaneEndpoint: grant.endpoint,
		RouteStatus:       grant.routeStatus,
	}), nil
}

func (e *reproEngine) TerminateSession(
	context.Context,
	*connect.Request[sandboxv1.TerminateSessionRequest],
) (*connect.Response[sandboxv1.TerminateSessionResponse], error) {
	e.terminateCalls.Add(1)
	return connect.NewResponse(&sandboxv1.TerminateSessionResponse{
		Session: &sandboxv1.SandboxSession{
			Id:    e.sessionID,
			State: sandboxv1.SessionState_SESSION_STATE_TERMINATED,
		},
	}), nil
}

func newReproClient(t *testing.T, engine *reproEngine) *Client {
	t.Helper()
	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(engine)
	mux.Handle(path, svc)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newReproSession(client *Client, endpoint, credential string, expiresIn time.Duration) *Session {
	session := &Session{client: client, ID: "session-repro", State: SessionStateRunning}
	session.configureDataPlane(endpoint, &sandboxv1.SessionCredential{
		Credential: credential,
		ExpiresAt:  timestamppb.New(time.Now().Add(expiresIn)),
	}, false)
	return session
}

// A reauth can relocate the session, which leaves the client that issued the
// request pointed at the old node. Replaying there would hand it the new
// node's credential.

func TestReauthReplayNeverCredentialsTheWrongNode(t *testing.T) {
	t.Parallel()

	nodeA, endpointA := newReproNode(t, "A", func() error {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("stale session cert"))
	})
	nodeB, endpointB := newReproNode(t, "B", nil)

	engine := &reproEngine{
		sessionID: "session-repro",
		grants: []credentialGrant{
			// UNSPECIFIED keeps applyDataPlaneResponse off the readiness mutex;
			// VERIFIED here deadlocks instead (see the deadlock test below).
			{endpoint: endpointB, credential: "cred-for-node-B"},
		},
	}
	client := newReproClient(t, engine)
	session := newReproSession(client, endpointA, "cred-for-node-A", time.Hour)

	// Any unary data-plane op. It starts out routed to node A.
	_, _ = session.Stat(context.Background(), "/home/tenki")

	t.Logf("node A saw credentials: %q", nodeA.credentials())
	t.Logf("node B saw credentials: %q", nodeB.credentials())

	if engine.credentialCalls.Load() == 0 {
		t.Fatal("reauthentication did not run")
	}
	if len(nodeA.credentials()) == 0 || nodeA.credentials()[0] != "cred-for-node-A" {
		t.Fatalf("node A did not receive its own credential: %q", nodeA.credentials())
	}
	for _, got := range nodeA.credentials() {
		if got == "cred-for-node-B" {
			t.Fatalf("MISROUTING: node A (%s) received the credential minted for node B (%s)", endpointA, endpointB)
		}
	}
}

// The readiness probe runs under dataPlaneProbeMu and re-enters the verified
// state through the interceptor's reauth path, so the two must not share a
// mutex: one mutex for both self-deadlocks the session forever.

func TestReauthDuringReadinessProbeDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	_, endpointA := newReproNode(t, "A", func() error {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("stale session cert"))
	})
	_, endpointB := newReproNode(t, "B", nil)

	engine := &reproEngine{
		sessionID: "session-repro",
		grants: []credentialGrant{
			{
				endpoint:    endpointB,
				credential:  "cred-for-node-B",
				routeStatus: sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_VERIFIED,
			},
		},
	}
	client := newReproClient(t, engine)
	session := newReproSession(client, endpointA, "cred-for-node-A", time.Hour)

	done := make(chan error, 1)
	go func() {
		_, err := session.Stat(context.Background(), "/home/tenki")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stat after reauthentication: %v", err)
		}
		if engine.credentialCalls.Load() == 0 {
			t.Fatal("reauthentication did not run")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("DEADLOCK: Stat never returned; ensureDataPlaneServing holds dataPlaneReadyMu " +
			"while the reauth replay re-locks it via markCurrentDataPlaneVerified")
	}
}

// A renewal in flight across Close resolves afterwards; without the closed
// latch it rebuilds both the renewal loop and a pooled transport for a session
// that is already terminated.

func TestLateRenewalDoesNotRearmAClosedSession(t *testing.T) {
	t.Parallel()

	_, endpointA := newReproNode(t, "A", nil)

	closeReturned := make(chan struct{})
	engine := &reproEngine{
		sessionID: "session-repro",
		grants: []credentialGrant{
			{endpoint: endpointA, credential: "cred-renewed", expiresIn: time.Hour},
		},
		beforeCredential: func(int) {
			// Hold the renewal open until Close() has fully returned.
			<-closeReturned
		},
	}
	client := newReproClient(t, engine)
	session := newReproSession(client, endpointA, "cred-initial", time.Hour)

	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		_ = session.renewDataPlaneCredential(context.Background())
	}()

	// Give the renewal time to reach the engine and block there.
	time.Sleep(100 * time.Millisecond)

	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(closeReturned)
	<-renewDone

	// Let the late response land.
	time.Sleep(100 * time.Millisecond)

	session.dataPlaneMu.RLock()
	renewalAlive := session.renewalCancel != nil
	credential := session.dataPlaneCredential.reveal()
	endpoint := session.dataPlaneEndpoint
	transport := session.dataPlaneClient != nil
	session.dataPlaneMu.RUnlock()

	t.Logf("after Close: renewalCancel!=nil=%v credential=%q endpoint=%q client!=nil=%v",
		renewalAlive, credential, endpoint, transport)

	if renewalAlive {
		t.Fatalf("RENEWAL AFTER CLOSE: credential renewal loop is still armed after Close returned")
	}
	if credential != "" || endpoint != "" || transport {
		t.Fatalf("STALE DATA PLANE AFTER CLOSE: credential=%q endpoint=%q client!=nil=%v", credential, endpoint, transport)
	}
}

func TestConcurrentCloseTerminatesOnce(t *testing.T) {
	t.Parallel()

	_, endpointA := newReproNode(t, "A", nil)
	engine := &reproEngine{
		sessionID: "session-repro",
		grants: []credentialGrant{
			{endpoint: endpointA, credential: "cred-initial", expiresIn: time.Hour},
		},
	}
	client := newReproClient(t, engine)
	session := newReproSession(client, endpointA, "cred-initial", time.Hour)

	const closers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range closers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = session.Close(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	got := engine.terminateCalls.Load()
	t.Logf("%d concurrent Close calls produced %d TerminateSession RPCs", closers, got)
	if got != 1 {
		t.Fatalf("CLOSE RACE: expected Close to be idempotent (1 TerminateSession), got %d", got)
	}
}

// Streaming shares the unary binding: a client aimed at the wrong node refuses
// on Send and Receive instead of writing the credential onto the wire.
func TestStreamingRefusesAMisroutedCredential(t *testing.T) {
	t.Parallel()

	_, endpointA := newReproNode(t, "A", nil)
	_, endpointB := newReproNode(t, "B", nil)

	engine := &reproEngine{
		sessionID: "session-repro",
		grants:    []credentialGrant{{endpoint: endpointB, credential: "cred-for-node-B"}},
	}
	client := newReproClient(t, engine)
	session := newReproSession(client, endpointA, "cred-for-node-A", time.Hour)

	// Rebind the credential to node B while the node A client stays live.
	session.dataPlaneMu.Lock()
	session.dataPlaneCredentialEndpoint = endpointB
	dataPlaneClient := session.dataPlaneClient
	session.dataPlaneMu.Unlock()

	stream := dataPlaneClient.Run(context.Background())
	err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunRequest{})
	if !errors.Is(err, errDataPlaneCredentialMisrouted) {
		t.Fatalf("expected a misrouting refusal on Send, got %v", err)
	}
	if _, err := stream.Receive(); !errors.Is(err, errDataPlaneCredentialMisrouted) {
		t.Fatalf("expected a misrouting refusal on Receive, got %v", err)
	}
	_ = stream.CloseRequest()
	_ = stream.CloseResponse()
}

func TestStreamingRefusesAnotherSessionsRequest(t *testing.T) {
	t.Parallel()

	_, endpointA := newReproNode(t, "A", nil)
	engine := &reproEngine{
		sessionID: "session-repro",
		grants:    []credentialGrant{{endpoint: endpointA, credential: "cred-for-node-A"}},
	}
	client := newReproClient(t, engine)
	session := newReproSession(client, endpointA, "cred-for-node-A", time.Hour)

	session.dataPlaneMu.RLock()
	dataPlaneClient := session.dataPlaneClient
	session.dataPlaneMu.RUnlock()

	stream := dataPlaneClient.Run(context.Background())
	err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunRequest{
		Frame: &sandboxv1.RunRequest{Payload: &sandboxv1.RunRequest_Start{
			Start: &sandboxv1.RunStart{SessionId: "someone-elses-session"},
		}},
	})
	if !errors.Is(err, errDataPlaneCredentialMisrouted) {
		t.Fatalf("expected a misrouting refusal on Send, got %v", err)
	}
	_ = stream.CloseRequest()
	_ = stream.CloseResponse()
}

func TestFailedCloseAllowsDataPlaneRecovery(t *testing.T) {
	t.Parallel()
	node, endpoint := newReproNode(t, "A", nil)
	engine := &reproEngine{
		sessionID: "session-repro",
		grants:    []credentialGrant{{endpoint: endpoint, credential: "cred-recovered"}},
	}
	session := newReproSession(newReproClient(t, engine), endpoint, "cred-initial", time.Hour)
	t.Cleanup(session.resetDataPlane)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Close(cancelled); err == nil {
		t.Fatal("Close with cancelled context succeeded")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if _, err := session.Stat(ctx, "/home/tenki"); err != nil {
		t.Fatalf("Stat after failed Close: %v", err)
	}
	if got := node.credentials(); len(got) == 0 || got[len(got)-1] != "cred-recovered" {
		t.Fatalf("recovered credential not sent: %q", got)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
}

func TestCredentialRenewalReapsCompletedLoops(t *testing.T) {
	t.Parallel()
	_, endpoint := newReproNode(t, "A", nil)
	engine := &reproEngine{sessionID: "session-repro"}
	session := newReproSession(newReproClient(t, engine), endpoint, "credential", time.Hour)
	t.Cleanup(session.resetDataPlane)
	for range 20 {
		session.dataPlaneMu.Lock()
		previous := append([]chan struct{}(nil), session.renewalDones...)
		session.restartCredentialRenewalLocked()
		retained := len(session.renewalDones)
		session.dataPlaneMu.Unlock()
		waitRenewalStopped(previous)
		if retained > 2 {
			t.Fatalf("retained %d renewal exit channels after previous loops stopped", retained)
		}
	}
}
