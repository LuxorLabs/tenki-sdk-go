package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

type waitSessionHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	getCalls             atomic.Int32
	createWaitReady      atomic.Bool
	createWaitForRuntime atomic.Bool
	createAllowInbound   *bool
	createAllowOutbound  *bool
	terminateCalls       atomic.Int32
	waitErr              error
	terminalError        string
	runtimeFailure       bool
	stayCreating         bool
}

func (h *waitSessionHandler) CreateSession(_ context.Context, req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	h.createWaitReady.Store(req.Msg.WaitReady)
	h.createWaitForRuntime.Store(req.Msg.WaitForRuntime)
	if req.Msg.AllowInbound != nil {
		allowInbound := req.Msg.GetAllowInbound()
		h.createAllowInbound = &allowInbound
	}
	if req.Msg.AllowOutbound != nil {
		allowOutbound := req.Msg.GetAllowOutbound()
		h.createAllowOutbound = &allowOutbound
	}
	if h.runtimeFailure {
		connectErr := connect.NewError(connect.CodeFailedPrecondition, errors.New("template runtime failed"))
		detail, err := connect.NewErrorDetail(&sandboxv1.TemplateRuntimeFailure{
			Session: &sandboxv1.SandboxSession{
				Id:           "019e84bc-6df8-765f-8507-2734f87156c7",
				State:        sandboxv1.SessionState_SESSION_STATE_RUNNING,
				RuntimeState: sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_FAILED,
			},
			Reason: "runtime readiness deadline exceeded",
		})
		if err != nil {
			return nil, err
		}
		connectErr.AddDetail(detail)
		return nil, connectErr
	}
	state := sandboxv1.SessionState_SESSION_STATE_CREATING
	if req.Msg.WaitForRuntime {
		state = sandboxv1.SessionState_SESSION_STATE_RUNNING
	}
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{Session: &sandboxv1.SandboxSession{
		Id:        "019e84bc-6df8-765f-8507-2734f87156c7",
		State:     state,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	}}), nil
}

func TestCreateSendsOutboundPolicyPresence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []CreateOption
		want bool
	}{
		{name: "default true", want: true},
		{name: "explicit true", opts: []CreateOption{WithAllowOutbound(true)}, want: true},
		{name: "explicit false", opts: []CreateOption{WithAllowOutbound(false)}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &waitSessionHandler{}
			server, client := newWaitSessionTestServer(t, handler)
			defer server.Close()

			opts := append([]CreateOption{WithWaitReady(false)}, tt.opts...)
			opts = append(opts, WithWorkspaceID("ws-1"))
			_, err := client.Create(context.Background(), opts...)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if handler.createAllowOutbound == nil {
				t.Fatal("CreateSession allow_outbound is absent")
			}
			if got := *handler.createAllowOutbound; got != tt.want {
				t.Fatalf("CreateSession allow_outbound = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCreateSendsInboundPolicyPresence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []CreateOption
		want bool
	}{
		{name: "default true", want: true},
		{name: "explicit true", opts: []CreateOption{WithAllowInbound(true)}, want: true},
		{name: "explicit false", opts: []CreateOption{WithAllowInbound(false)}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &waitSessionHandler{}
			server, client := newWaitSessionTestServer(t, handler)
			defer server.Close()

			opts := append([]CreateOption{WithWaitReady(false)}, tt.opts...)
			opts = append(opts, WithWorkspaceID("ws-1"))
			_, err := client.Create(context.Background(), opts...)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if handler.createAllowInbound == nil {
				t.Fatal("CreateSession allow_inbound is absent")
			}
			if got := *handler.createAllowInbound; got != tt.want {
				t.Fatalf("CreateSession allow_inbound = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCreateReturnsTypedTemplateRuntimeFailure(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{runtimeFailure: true}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	_, err := client.Create(context.Background(), WithWorkspaceID("ws-1"), WithWaitForRuntime(true), WithWaitTimeout(time.Second))
	if !errors.Is(err, ErrTemplateRuntimeFailed) {
		t.Fatalf("Create error = %v, want ErrTemplateRuntimeFailed", err)
	}
	var runtimeErr *TemplateRuntimeFailedError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("Create error type = %T, want *TemplateRuntimeFailedError", err)
	}
	if runtimeErr.Session == nil || runtimeErr.Session.State != SessionStateRunning {
		t.Fatalf("runtime failure session = %#v, want RUNNING", runtimeErr.Session)
	}
	if runtimeErr.Session.RuntimeState != RuntimeStateFailed {
		t.Fatalf("runtime state = %s, want %s", runtimeErr.Session.RuntimeState, RuntimeStateFailed)
	}
	if runtimeErr.Reason != "runtime readiness deadline exceeded" {
		t.Fatalf("reason = %q", runtimeErr.Reason)
	}
}

func TestCreateCanWaitForTemplateRuntime(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session, err := client.Create(
		context.Background(),
		WithWorkspaceID("ws-1"),
		WithName("runtime"),
		WithWaitReady(false),
		WithWaitForRuntime(true),
		WithWaitTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !handler.createWaitForRuntime.Load() {
		t.Fatal("CreateSession wait_for_runtime = false, want true")
	}
	if session.State != SessionStateRunning {
		t.Fatalf("state = %s, want %s", session.State, SessionStateRunning)
	}
}

func TestDefaultHTTPTimeoutCoversLongestDefaultWait(t *testing.T) {
	t.Parallel()

	client, err := New(
		WithAuthToken("tk_test_api_key"),
		WithBaseURL("http://127.0.0.1:1"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer client.Close()

	if client.httpClient.Timeout <= DefaultRestoreTimeout {
		t.Fatalf(
			"default HTTP timeout = %s, must exceed longest default wait %s",
			client.httpClient.Timeout,
			DefaultRestoreTimeout,
		)
	}

	ctx, cancel := client.heldCreateContext(context.Background(), DefaultRestoreTimeout)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("held create context has no deadline")
	}
	if remaining := time.Until(deadline); remaining < DefaultRestoreTimeout-time.Second {
		t.Fatalf("held create deadline = %s, want full %s budget", remaining, DefaultRestoreTimeout)
	}
}

func TestCreateDefaultsToWaitReady(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"), WithName("wait-stream"), WithWaitTimeout(time.Second))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !handler.createWaitReady.Load() {
		t.Fatal("CreateSession wait_ready = false, want true")
	}
	if session.State != SessionStateRunning {
		t.Fatalf("state = %s, want %s", session.State, SessionStateRunning)
	}
}

func TestCreateWithWaitReadyFalseReturnsImmediately(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"), WithName("wait-stream"), WithWaitReady(false))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if handler.createWaitReady.Load() {
		t.Fatal("CreateSession wait_ready = true, want false")
	}
	if session.State != SessionStateCreating {
		t.Fatalf("state = %s, want %s", session.State, SessionStateCreating)
	}
}

func (h *waitSessionHandler) GetSession(context.Context, *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
	h.getCalls.Add(1)
	return connect.NewResponse(&sandboxv1.GetSessionResponse{Session: &sandboxv1.SandboxSession{
		Id:        "019e84bc-6df8-765f-8507-2734f87156c7",
		State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	}}), nil
}

func (h *waitSessionHandler) WaitSession(_ context.Context, req *connect.Request[sandboxv1.WaitSessionRequest], stream *connect.ServerStream[sandboxv1.WaitSessionResponse]) error {
	if err := stream.Send(&sandboxv1.WaitSessionResponse{Session: &sandboxv1.SandboxSession{
		Id:        req.Msg.SessionId,
		State:     sandboxv1.SessionState_SESSION_STATE_CREATING,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	}}); err != nil {
		return err
	}
	if h.stayCreating {
		return nil
	}
	if h.waitErr != nil {
		return h.waitErr
	}
	if h.terminalError != "" {
		return stream.Send(&sandboxv1.WaitSessionResponse{Session: &sandboxv1.SandboxSession{
			Id:            req.Msg.SessionId,
			State:         sandboxv1.SessionState_SESSION_STATE_TERMINATED,
			OwnerType:     "SERVICE",
			OwnerId:       "self",
			TerminalError: h.terminalError,
		}})
	}
	return stream.Send(&sandboxv1.WaitSessionResponse{
		Session: &sandboxv1.SandboxSession{
			Id:        req.Msg.SessionId,
			State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
			OwnerType: "SERVICE",
			OwnerId:   "self",
		},
		DataPlaneEndpoint: "http://data-plane.test",
		Credential: &sandboxv1.SessionCredential{
			Credential: "wait-session-credential",
		},
		RouteStatus: sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_VERIFIED,
	})
}

func (h *waitSessionHandler) TerminateSession(_ context.Context, req *connect.Request[sandboxv1.TerminateSessionRequest]) (*connect.Response[sandboxv1.TerminateSessionResponse], error) {
	h.terminateCalls.Add(1)
	return connect.NewResponse(&sandboxv1.TerminateSessionResponse{Session: &sandboxv1.SandboxSession{
		Id:        req.Msg.SessionId,
		State:     sandboxv1.SessionState_SESSION_STATE_TERMINATED,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	}}), nil
}

func TestCreateWaitFailureReturnsSessionHandle(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{stayCreating: true}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	_, err := client.Create(
		context.Background(),
		WithWorkspaceID("ws-1"),
		WithName("wait-timeout"),
		WithWaitTimeout(time.Second),
	)
	if !errors.Is(err, ErrWaitReadyFailed) {
		t.Fatalf("Create error = %v, want ErrWaitReadyFailed", err)
	}
	var waitErr *WaitReadyFailedError
	if !errors.As(err, &waitErr) {
		t.Fatalf("Create error type = %T, want *WaitReadyFailedError", err)
	}
	if waitErr.Session == nil || waitErr.Session.ID != "019e84bc-6df8-765f-8507-2734f87156c7" {
		t.Fatalf("wait failure session = %#v, want admitted session handle", waitErr.Session)
	}
	if waitErr.Session.State.IsTerminal() {
		t.Fatalf("session state = %s, want non-terminal", waitErr.Session.State)
	}
	if closeErr := waitErr.Session.Close(context.Background()); closeErr != nil {
		t.Fatalf("Close via returned handle: %v", closeErr)
	}
	if got := handler.terminateCalls.Load(); got != 1 {
		t.Fatalf("TerminateSession calls = %d, want 1", got)
	}
}

func TestCreateWaitTerminalStateNotWrapped(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{terminalError: "sandbox provisioned but readiness check failed"}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	_, err := client.Create(
		context.Background(),
		WithWorkspaceID("ws-1"),
		WithName("wait-terminal"),
		WithWaitTimeout(time.Second),
	)
	if err == nil {
		t.Fatal("Create returned nil error, want terminal-state error")
	}
	if errors.Is(err, ErrWaitReadyFailed) {
		t.Fatalf("terminal-state error wrapped as WaitReadyFailedError: %v", err)
	}
	if !strings.Contains(err.Error(), "sandbox provisioned but readiness check failed") {
		t.Fatalf("error = %q, want terminal_error", err)
	}
}

func TestCreateAndWaitUsesWaitSessionStream(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session, err := client.CreateAndWait(context.Background(), time.Second, WithWorkspaceID("ws-1"), WithName("wait-stream"))
	if err != nil {
		t.Fatalf("CreateAndWait: %v", err)
	}
	if session.State != SessionStateRunning {
		t.Fatalf("state = %s, want %s", session.State, SessionStateRunning)
	}
	if got := handler.getCalls.Load(); got != 0 {
		t.Fatalf("GetSession calls = %d, want 0", got)
	}
	if session.dataPlaneEndpoint != "http://data-plane.test" {
		t.Fatalf("dataPlaneEndpoint = %q, want wait response endpoint", session.dataPlaneEndpoint)
	}
	if got := session.currentDataPlaneCredential(); got != "wait-session-credential" {
		t.Fatalf("data-plane credential = %q, want wait response credential", got)
	}
}

func TestCreateAndWaitIncludesTerminalError(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{terminalError: "sandbox provisioned but readiness check failed"}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	_, err := client.CreateAndWait(context.Background(), time.Second, WithWorkspaceID("ws-1"), WithName("wait-stream"))
	if err == nil {
		t.Fatal("CreateAndWait returned nil error")
	}
	if !strings.Contains(err.Error(), "sandbox provisioned but readiness check failed") {
		t.Fatalf("error = %q, want terminal_error", err)
	}
}

func TestCreateAndWaitFallsBackToPollOnRetryableWaitStreamError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "unavailable", err: connect.NewError(connect.CodeUnavailable, errors.New("transport closing"))},
		{name: "deadline", err: connect.NewError(connect.CodeDeadlineExceeded, context.DeadlineExceeded)},
		{name: "internal stream reset", err: connect.NewError(connect.CodeInternal, errors.New("stream reset: INTERNAL_ERROR"))},
		{name: "internal timeout", err: connect.NewError(connect.CodeInternal, errors.New("upstream request timeout"))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := &waitSessionHandler{waitErr: tc.err}
			server, client := newWaitSessionTestServer(t, handler)
			defer server.Close()

			session, err := client.CreateAndWait(context.Background(), time.Second, WithWorkspaceID("ws-1"), WithName("wait-stream"))
			if err != nil {
				t.Fatalf("CreateAndWait: %v", err)
			}
			if session.State != SessionStateRunning {
				t.Fatalf("state = %s, want %s", session.State, SessionStateRunning)
			}
			if got := handler.getCalls.Load(); got == 0 {
				t.Fatalf("GetSession calls = %d, want fallback polling", got)
			}
		})
	}
}

func newWaitSessionTestServer(t *testing.T, h sandboxv1connect.SandboxServiceHandler) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := sandboxv1connect.NewSandboxServiceHandler(h)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		server.Close()
		t.Fatalf("New client: %v", err)
	}
	return server, client
}
