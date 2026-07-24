package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// reauthEngineHandler mints "refreshed" credentials and counts mints.
type reauthEngineHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	mints atomic.Int32
}

func (h *reauthEngineHandler) CreateSessionCredential(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateSessionCredentialRequest],
) (*connect.Response[sandboxv1.CreateSessionCredentialResponse], error) {
	if got := req.Header().Get(headerAuthorization); got != "Bearer tk_test" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing original auth"))
	}
	h.mints.Add(1)
	return connect.NewResponse(&sandboxv1.CreateSessionCredentialResponse{
		Credential: testSessionCredential("refreshed", time.Now().Add(time.Hour)),
	}), nil
}

// reauthDataPlaneHandler rejects any credential != "refreshed" with
// Unauthenticated. Stat always succeeds so the cold-start readiness probe
// passes without itself driving a re-mint.
type reauthDataPlaneHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
}

func (h *reauthDataPlaneHandler) Stat(
	_ context.Context,
	_ *connect.Request[sandboxv1.SandboxSessionDataPlaneServiceStatRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceStatResponse], error) {
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceStatResponse{
		Response: &sandboxv1.StatResponse{Size: 0},
	}), nil
}

func (h *reauthDataPlaneHandler) Mkdir(
	_ context.Context,
	req *connect.Request[sandboxv1.SandboxSessionDataPlaneServiceMkdirRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceMkdirResponse], error) {
	if got := req.Header().Get(dataPlaneCredentialHeader); got != "refreshed" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("stale credential"))
	}
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceMkdirResponse{Response: &sandboxv1.MkdirResponse{}}), nil
}

func (h *reauthDataPlaneHandler) Run(
	_ context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
) error {
	if got := stream.RequestHeader().Get(dataPlaneCredentialHeader); got != "refreshed" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("stale credential"))
	}
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{Pid: 1}},
	}}); err != nil {
		return err
	}
	return stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Exit{Exit: &sandboxv1.RunExit{ExitCode: 0}},
	}})
}

func newReauthSession(t *testing.T) (*Session, *reauthEngineHandler) {
	t.Helper()
	engine := &reauthEngineHandler{}
	mux := http.NewServeMux()
	servicePath, serviceHandler := sandboxv1connect.NewSandboxServiceHandler(engine)
	mux.Handle(servicePath, serviceHandler)
	dataPath, dataHandler := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(&reauthDataPlaneHandler{})
	mux.Handle(dataPath, dataHandler)

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)

	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	session := &Session{client: client, ID: "session-1"}
	// Far-future expiry so the proactive renewal loop / 10ms guard never fire;
	// the only re-mint must be the reactive one under test.
	session.configureDataPlane(server.URL, testSessionCredential("initial", time.Now().Add(time.Hour)))
	return session, engine
}

// TestReactiveRefreshUnary proves a unary data-plane op transparently re-mints
// and retries once when the node-agent rejects the credential as Unauthenticated.
func TestReactiveRefreshUnary(t *testing.T) {
	t.Parallel()
	session, engine := newReauthSession(t)

	if err := session.Mkdir(context.Background(), "/home/tenki/x"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := engine.mints.Load(); got != 1 {
		t.Fatalf("mints got %d, want 1 (one reactive re-mint)", got)
	}
}

// TestReactiveRefreshRun proves the streaming Run (exec) open transparently
// re-mints and retries once on an Unauthenticated reject at establishment.
func TestReactiveRefreshRun(t *testing.T) {
	t.Parallel()
	session, engine := newReauthSession(t)

	result, err := session.Command([]string{"echo", "hi"}).Exec(context.Background())
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code got %d, want 0", result.ExitCode)
	}
	if got := engine.mints.Load(); got != 1 {
		t.Fatalf("mints got %d, want 1 (one reactive re-mint)", got)
	}
}
