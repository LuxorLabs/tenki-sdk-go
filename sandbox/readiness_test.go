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

type readinessEngineHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	calls          atomic.Int32
	notReadyBefore int32
	failed         bool
	endpoint       string
}

func (h *readinessEngineHandler) CreateSessionCredential(
	_ context.Context,
	_ *connect.Request[sandboxv1.CreateSessionCredentialRequest],
) (*connect.Response[sandboxv1.CreateSessionCredentialResponse], error) {
	n := h.calls.Add(1)
	status := sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_VERIFIED
	if h.failed {
		status = sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_FAILED
	} else if n <= h.notReadyBefore {
		status = sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_NOT_READY
	}
	return connect.NewResponse(&sandboxv1.CreateSessionCredentialResponse{
		Credential:        testSessionCredential("ready", time.Now().Add(time.Hour)),
		DataPlaneEndpoint: h.endpoint,
		RouteStatus:       status,
	}), nil
}

type readinessDataPlaneHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	runFailures atomic.Int32
	runCalls    atomic.Int32
	runFailure  error
	statCalls   atomic.Int32
}

func (h *readinessDataPlaneHandler) Stat(
	_ context.Context,
	_ *connect.Request[sandboxv1.SandboxSessionDataPlaneServiceStatRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceStatResponse], error) {
	h.statCalls.Add(1)
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceStatResponse{
		Response: &sandboxv1.StatResponse{Exists: true},
	}), nil
}

func (h *readinessDataPlaneHandler) Run(
	_ context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
) error {
	h.runCalls.Add(1)
	if h.runFailures.Add(-1) >= 0 {
		if h.runFailure != nil {
			return h.runFailure
		}
		return connect.NewError(connect.CodeUnimplemented, errors.New("HTTP status 404 Not Found"))
	}
	if _, err := stream.Receive(); err != nil {
		return err
	}
	return stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{Pid: 1}},
	}})
}

func newReadinessHarness(t *testing.T, engine *readinessEngineHandler, dataPlane *readinessDataPlaneHandler, opts ...Option) (*Client, string) {
	t.Helper()
	mux := http.NewServeMux()
	servicePath, serviceHandler := sandboxv1connect.NewSandboxServiceHandler(engine)
	mux.Handle(servicePath, serviceHandler)
	dataPath, dataHandler := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(dataPlane)
	mux.Handle(dataPath, dataHandler)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	engine.endpoint = server.URL
	options := []Option{WithAuthToken("tk_test"), WithBaseURL(server.URL)}
	options = append(options, opts...)
	client, err := New(options...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, server.URL
}

func TestRouteStatusNotReadyRetriesUntilVerified(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{notReadyBefore: 2}
	client, _ := newReadinessHarness(t, engine, &readinessDataPlaneHandler{}, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}

	if _, err := session.dataPlane(context.Background()); err != nil {
		t.Fatalf("dataPlane: %v", err)
	}
	if got := engine.calls.Load(); got != 3 {
		t.Fatalf("credential calls got %d, want 3", got)
	}
}

func TestVerifiedRouteStatusSkipsReadinessStatProbe(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{}
	dataPlane := &readinessDataPlaneHandler{}
	client, _ := newReadinessHarness(t, engine, dataPlane, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}

	if _, err := session.dataPlane(context.Background()); err != nil {
		t.Fatalf("dataPlane: %v", err)
	}
	if got := dataPlane.statCalls.Load(); got != 0 {
		t.Fatalf("stat calls got %d, want 0", got)
	}
}

func TestRouteStatusFailedReturnsTerminalError(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{failed: true}
	client, _ := newReadinessHarness(t, engine, &readinessDataPlaneHandler{}, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}

	_, err := session.dataPlane(context.Background())
	var notReady *DataPlaneNotReadyError
	if !errors.As(err, &notReady) || !notReady.Terminal || notReady.IsRetryable() {
		t.Fatalf("expected terminal DataPlaneNotReadyError, got %#v", err)
	}
}

func TestDataPlaneReadyBudgetIgnoresTightDeadline(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{notReadyBefore: 1}
	client, _ := newReadinessHarness(t, engine, &readinessDataPlaneHandler{}, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	if _, err := session.dataPlane(ctx); err != nil {
		t.Fatalf("dataPlane with expired op deadline: %v", err)
	}
}

func TestDataPlaneReadyCancelFuncIsIdempotent(t *testing.T) {
	t.Parallel()
	_, cancel := dataPlaneReadyContext(context.Background(), time.Second)
	cancel()
	cancel()
}

func TestCredentialRenewalLoopStopsOnTerminalRouteFailure(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{failed: true}
	client, _ := newReadinessHarness(t, engine, &readinessDataPlaneHandler{}, WithDataPlaneReadyTimeout(50*time.Millisecond))
	session := &Session{client: client, ID: "session-1", dataPlaneExpiresAt: time.Now().Add(-time.Second)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		session.credentialRenewalLoop(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("credentialRenewalLoop did not stop on terminal route failure")
	}
	if got := engine.calls.Load(); got != 1 {
		t.Fatalf("credential calls got %d, want 1", got)
	}
}

func TestRunStreamOpenRetriesEdgeNotReady(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{}
	dataPlane := &readinessDataPlaneHandler{}
	dataPlane.runFailures.Store(1)
	client, endpoint := newReadinessHarness(t, engine, dataPlane, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}
	session.configureDataPlane(endpoint, testSessionCredential("ready", time.Now().Add(time.Hour)))

	handle, err := session.Command([]string{"true"}).Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if handle.PID != 1 {
		t.Fatalf("pid got %d, want 1", handle.PID)
	}
	_ = handle.Stdin.Close()
}

func TestRunStreamOpenRetriesTransientReset(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{}
	dataPlane := &readinessDataPlaneHandler{
		runFailure: connect.NewError(connect.CodeAborted, errors.New("read ECONNRESET")),
	}
	dataPlane.runFailures.Store(1)
	client, endpoint := newReadinessHarness(t, engine, dataPlane, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}
	session.configureDataPlane(endpoint, testSessionCredential("ready", time.Now().Add(time.Hour)))

	handle, err := session.Command([]string{"true"}).Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if handle.PID != 1 {
		t.Fatalf("pid got %d, want 1", handle.PID)
	}
	if got := dataPlane.runCalls.Load(); got != 2 {
		t.Fatalf("run calls got %d, want 2", got)
	}
	_ = handle.Stdin.Close()
}

func TestRunStreamOpenDoesNotRetryNonTransientAborted(t *testing.T) {
	t.Parallel()
	engine := &readinessEngineHandler{}
	dataPlane := &readinessDataPlaneHandler{
		runFailure: connect.NewError(connect.CodeAborted, errors.New("command rejected")),
	}
	dataPlane.runFailures.Store(1)
	client, endpoint := newReadinessHarness(t, engine, dataPlane, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}
	session.configureDataPlane(endpoint, testSessionCredential("ready", time.Now().Add(time.Hour)))

	_, err := session.Command([]string{"true"}).Stream(context.Background())
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stream error got %v, want aborted", err)
	}
	if got := dataPlane.runCalls.Load(); got != 1 {
		t.Fatalf("run calls got %d, want 1", got)
	}
}
