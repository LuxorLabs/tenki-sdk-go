package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	getCalls                   atomic.Int32
	createWaitReady            atomic.Bool
	createWaitForRuntime       atomic.Bool
	createTailnetWaitForOnline atomic.Bool
	createAllowInbound         *bool
	createAllowOutbound        *bool
	terminateCalls             atomic.Int32
	waitErr                    error
	terminalError              string
	runtimeFailure             bool
	stayCreating               bool
	waitTailnetOnline          bool
}

func (h *waitSessionHandler) CreateSession(_ context.Context, req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	h.createWaitReady.Store(req.Msg.WaitReady)
	h.createWaitForRuntime.Store(req.Msg.WaitForRuntime)
	h.createTailnetWaitForOnline.Store(req.Msg.GetTailnet().GetWaitForOnline())
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
	session := &sandboxv1.SandboxSession{
		Id:        "019e84bc-6df8-765f-8507-2734f87156c7",
		State:     state,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	}
	if h.waitTailnetOnline {
		session.TailnetStatus = &sandboxv1.TailnetStatus{Provider: "tailscale", State: "pending"}
	}
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{Session: session}), nil
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
	if !handler.createWaitReady.Load() {
		t.Fatal("CreateSession wait_ready = false, want true")
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
	if handler.createWaitReady.Load() {
		t.Fatal("CreateSession wait_ready = true, want false")
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

func TestCreateDefaultsToClientWaitReady(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"), WithName("wait-stream"), WithWaitTimeout(time.Second))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if handler.createWaitReady.Load() {
		t.Fatal("CreateSession wait_ready = true, want false")
	}
	if session.State != SessionStateRunning {
		t.Fatalf("state = %s, want %s", session.State, SessionStateRunning)
	}
}

func TestCreateWaitForTailnetOnlineReturnsUpdatedStatus(t *testing.T) {
	t.Parallel()

	handler := &waitSessionHandler{waitTailnetOnline: true}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session, err := client.Create(context.Background(),
		WithWorkspaceID("ws-1"),
		WithTailnet(TailnetAttachment{
			AuthKey:       NewTailnetAuthKey("tskey-auth-test"),
			Provider:      TailnetProviderTailscale,
			WaitForOnline: true,
		}),
		WithWaitTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !handler.createTailnetWaitForOnline.Load() {
		t.Fatal("CreateSession tailnet wait_for_online = false")
	}
	if session.State != SessionStateRunning || session.TailnetStatus == nil || session.TailnetStatus.State != TailnetStateOnline {
		t.Fatalf("created state = %s, tailnet = %+v; want RUNNING with tailnet online", session.State, session.TailnetStatus)
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
	session := &sandboxv1.SandboxSession{
		Id:        req.Msg.SessionId,
		State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	}
	if h.waitTailnetOnline {
		session.TailnetStatus = &sandboxv1.TailnetStatus{Provider: "tailscale", State: "online", NodeId: "node-1"}
	}
	return stream.Send(&sandboxv1.WaitSessionResponse{
		Session:           session,
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

	handler := &waitSessionHandler{waitErr: connect.NewError(connect.CodePermissionDenied, errors.New("wait denied"))}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	_, err := client.Create(
		context.Background(),
		WithWorkspaceID("ws-1"),
		WithName("wait-failure"),
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

func TestCreateAndWaitTransportRecovery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code    connect.Code
		message string
		recover bool
	}{
		{connect.CodeUnimplemented, "not implemented", true},
		{connect.CodeUnavailable, "service unavailable", true},
		{connect.CodeDeadlineExceeded, "upstream deadline", true},
		{connect.CodeInternal, "REFUSED_STREAM", true},
		{connect.CodeInternal, "GOAWAY", true},
		{connect.CodeInternal, "stream reset", true},
		{connect.CodeInternal, "stream error", true},
		{connect.CodeInternal, "NGHTTP2_INTERNAL_ERROR", true},
		{connect.CodeInternal, "ECONNRESET", true},
		{connect.CodeInternal, "connection reset by peer", true},
		{connect.CodeInternal, "socket hang up", true},
		{connect.CodeInternal, "unexpected EOF", true},
		{connect.CodeInternal, "stream closed before headers", true},
		{connect.CodeInternal, "server closed", true},
		{connect.CodeInternal, "transport closing", true},
		{connect.CodeInternal, "upstream request timeout", true},
		{connect.CodeAborted, "REFUSED_STREAM", true},
		{connect.CodeAborted, "GOAWAY", true},
		{connect.CodeAborted, "stream reset", true},
		{connect.CodeAborted, "stream error", true},
		{connect.CodeAborted, "NGHTTP2_INTERNAL_ERROR", true},
		{connect.CodeAborted, "ECONNRESET", true},
		{connect.CodeAborted, "connection reset by peer", true},
		{connect.CodeAborted, "socket hang up", true},
		{connect.CodeAborted, "unexpected EOF", true},
		{connect.CodeAborted, "stream closed before headers", true},
		{connect.CodeAborted, "server closed", true},
		{connect.CodeAborted, "transport closing", true},
		{connect.CodeAborted, "upstream request timeout", true},
		{connect.CodeUnknown, "REFUSED_STREAM", true},
		{connect.CodeUnknown, "GOAWAY", true},
		{connect.CodeUnknown, "stream reset", true},
		{connect.CodeUnknown, "stream error", true},
		{connect.CodeUnknown, "NGHTTP2_INTERNAL_ERROR", true},
		{connect.CodeUnknown, "ECONNRESET", true},
		{connect.CodeUnknown, "connection reset by peer", true},
		{connect.CodeUnknown, "socket hang up", true},
		{connect.CodeUnknown, "unexpected EOF", true},
		{connect.CodeUnknown, "stream closed before headers", true},
		{connect.CodeUnknown, "server closed", true},
		{connect.CodeUnknown, "transport closing", true},
		{connect.CodeUnknown, "upstream request timeout", true},
		{connect.CodePermissionDenied, "NGHTTP2_INTERNAL_ERROR", false},
		{connect.CodeUnauthenticated, "NGHTTP2_INTERNAL_ERROR", false},
		{connect.CodeCanceled, "NGHTTP2_INTERNAL_ERROR", true},
		{connect.CodeInvalidArgument, "NGHTTP2_INTERNAL_ERROR", false},
		{connect.CodeInternal, "Received RST_STREAM with error code 2", true},
		{connect.CodeUnknown, "Stream removed", true},
		{connect.CodeUnknown, "Socket closed", true},
		{connect.CodeInternal, "protocol error: missing status", true},
		{connect.CodeUnknown, "protocol error: no Grpc-Status trailer: unexpected EOF", true},
		{connect.CodeInternal, "http2: client connection lost", true},
		{connect.CodeInternal, "http2: timeout awaiting response headers", true},
		{connect.CodeCanceled, "http/2 stream closed with error code CANCEL (0x8)", true},
		{connect.CodeInvalidArgument, "protocol error: incomplete envelope: http2: server sent GOAWAY and closed the connection", true},
		{connect.CodeInvalidArgument, "invalid session id", false},
		{connect.CodeCanceled, "application failure", false},
		{connect.CodeResourceExhausted, "http/2 connection closed with error code ENHANCE_YOUR_CALM (0xb), too_many_pings, doubled the interval", true},
		{connect.CodeResourceExhausted, "REFUSED_STREAM", true},
		{connect.CodePermissionDenied, "GOAWAY", true},
		{connect.CodeResourceExhausted, "rate limit exceeded", false},
		{connect.CodeInternal, "http2: client conn is closed", true},
		{connect.CodeResourceExhausted, "NGHTTP2_INTERNAL_ERROR", false},
		{connect.CodeNotFound, "NGHTTP2_INTERNAL_ERROR", false},
		{connect.CodeInternal, "application failure", false},
		{connect.CodeAborted, "application failure", false},
		{connect.CodeUnknown, "application failure", false},
	}
	for _, tc := range tests {
		t.Run(tc.code.String()+"/"+tc.message, func(t *testing.T) {
			t.Parallel()
			handler := &waitSessionHandler{waitErr: connect.NewError(tc.code, errors.New(tc.message))}
			server, client := newWaitSessionTestServer(t, handler)
			defer server.Close()
			session, err := client.CreateAndWait(context.Background(), time.Second, WithWorkspaceID("ws-1"))
			if tc.recover {
				if err != nil {
					t.Fatalf("CreateAndWait: %v", err)
				}
				if session.State != SessionStateRunning {
					t.Fatalf("state = %s", session.State)
				}
				if handler.getCalls.Load() == 0 {
					t.Fatal("expected fallback polling")
				}
			} else {
				if err == nil {
					t.Fatal("expected stream failure")
				}
				if handler.getCalls.Load() != 0 {
					t.Fatal("unexpected fallback polling")
				}
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

func TestWaitReadyDoesNotPollAfterDeadlineOrCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired", true: "canceled"}[canceled], func(t *testing.T) {
			handler := &waitSessionHandler{waitErr: connect.NewError(connect.CodeUnavailable, errors.New("unavailable"))}
			server, client := newWaitSessionTestServer(t, handler)
			defer server.Close()
			session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"))
			if err != nil {
				t.Fatal(err)
			}
			handler.getCalls.Store(0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := time.Duration(0)
			if canceled {
				cancel()
				timeout = time.Second
			}
			if err := session.WaitReady(ctx, timeout); err == nil {
				t.Fatal("expected wait failure")
			}
			if handler.getCalls.Load() != 0 {
				t.Fatal("unexpected fallback polling")
			}
		})
	}
}

type recoveringWaitHandler struct {
	waitSessionHandler
	pollCalls      atomic.Int32
	firstPollError error
	emptyStream    bool
}

func (h *recoveringWaitHandler) GetSession(ctx context.Context, req *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
	if h.pollCalls.Add(1) == 1 && h.firstPollError != nil {
		return nil, h.firstPollError
	}
	return h.waitSessionHandler.GetSession(ctx, req)
}

func (h *recoveringWaitHandler) WaitSession(ctx context.Context, req *connect.Request[sandboxv1.WaitSessionRequest], stream *connect.ServerStream[sandboxv1.WaitSessionResponse]) error {
	if h.emptyStream {
		return nil
	}
	return h.waitSessionHandler.WaitSession(ctx, req, stream)
}

func TestWaitReadyRecoversCleanStreamClose(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "creating", true: "empty"}[empty], func(t *testing.T) {
			h := &recoveringWaitHandler{waitSessionHandler: waitSessionHandler{stayCreating: true}, emptyStream: empty}
			server, client := newWaitSessionTestServer(t, h)
			defer server.Close()
			session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_CREATING})
			if err := session.WaitReady(context.Background(), time.Second); err != nil {
				t.Fatal(err)
			}
			if h.pollCalls.Load() != 1 || !session.State.IsReady() {
				t.Fatal("expected polling to reach ready")
			}
		})
	}
}

func TestWaitReadyPollRecoversTransientErrors(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeNotFound, connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeInternal, connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeCanceled, connect.CodeResourceExhausted} {
		t.Run(code.String(), func(t *testing.T) {
			message := "poll failed"
			if code == connect.CodeInternal {
				message = "stream closed with error code NGHTTP2_INTERNAL_ERROR"
			}
			h := &recoveringWaitHandler{waitSessionHandler: waitSessionHandler{waitErr: connect.NewError(connect.CodeUnavailable, errors.New("temporary"))}, firstPollError: connect.NewError(code, errors.New(message))}
			server, client := newWaitSessionTestServer(t, h)
			defer server.Close()
			session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_CREATING})
			err := session.WaitReady(context.Background(), 2*time.Second)
			recover := code == connect.CodeNotFound || code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded || code == connect.CodeInternal
			if recover {
				if err != nil {
					t.Fatal(err)
				}
				if h.pollCalls.Load() != 2 || !session.State.IsReady() {
					t.Fatal("expected second poll to reach ready")
				}
			} else if err == nil || h.pollCalls.Load() != 1 {
				t.Fatal("expected immediate poll failure")
			}
		})
	}
}

func TestWaitReadyWrappedDeadlineClassification(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeInternal, connect.CodeAborted, connect.CodeUnknown, connect.CodePermissionDenied, connect.CodeCanceled} {
		want := code != connect.CodePermissionDenied
		if got := isRetryableWaitStreamError(connect.NewError(code, context.DeadlineExceeded)); got != want {
			t.Errorf("code %s: got %v want %v", code, got, want)
		}
	}
	if isRetryableWaitStreamError(connect.NewError(connect.CodeInternal, errors.New("application timeout"))) {
		t.Fatal("generic timeout must not be retried")
	}
}

type stalledPollHandler struct {
	waitSessionHandler
	pollErr error
}

func (h *stalledPollHandler) GetSession(ctx context.Context, _ *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
	if h.pollErr != nil {
		return nil, h.pollErr
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWaitReadyPollStaysWithinBudget(t *testing.T) {
	h := &stalledPollHandler{waitSessionHandler: waitSessionHandler{waitErr: connect.NewError(connect.CodeUnavailable, errors.New("temporary"))}}
	server, client := newWaitSessionTestServer(t, h)
	defer server.Close()
	session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_CREATING})
	start := time.Now()
	err := session.WaitReady(context.Background(), 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "last error") {
		t.Fatalf("err = %v, want timeout with last poll error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("WaitReady took %s with a 300ms budget", elapsed)
	}
}

func TestWaitReadyTimeoutReportsLastPollError(t *testing.T) {
	h := &stalledPollHandler{
		waitSessionHandler: waitSessionHandler{waitErr: connect.NewError(connect.CodeUnavailable, errors.New("temporary"))},
		pollErr:            connect.NewError(connect.CodeUnavailable, errors.New("edge unreachable")),
	}
	server, client := newWaitSessionTestServer(t, h)
	defer server.Close()
	session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_CREATING})
	err := session.WaitReady(context.Background(), 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "edge unreachable") {
		t.Fatalf("err = %v, want last poll error", err)
	}
}

func TestWaitReadyClassifierIgnoresNonRPCErrors(t *testing.T) {
	terminal := fmt.Errorf("session entered terminal state: TERMINATED: guest agent: connection reset by peer")
	if isRetryableWaitStreamError(terminal) || isRetryableWaitPollError(terminal) {
		t.Fatal("terminal-state error must not be retried")
	}
	ioTimeout := connect.NewError(connect.CodeUnknown, fmt.Errorf("read enveloped message: read tcp: %w", os.ErrDeadlineExceeded))
	if !isRetryableWaitStreamError(ioTimeout) || !isRetryableWaitPollError(ioTimeout) {
		t.Fatal("network i/o timeout must be retried")
	}
}

type stallOncePollHandler struct {
	waitSessionHandler
	pollCalls atomic.Int32
}

func (h *stallOncePollHandler) GetSession(ctx context.Context, req *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
	if h.pollCalls.Add(1) == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return h.waitSessionHandler.GetSession(ctx, req)
}

func TestWaitReadyPollRetriesAfterStalledAttempt(t *testing.T) {
	previous := waitPollAttemptTimeout
	waitPollAttemptTimeout = 100 * time.Millisecond
	defer func() { waitPollAttemptTimeout = previous }()

	h := &stallOncePollHandler{waitSessionHandler: waitSessionHandler{waitErr: connect.NewError(connect.CodeUnavailable, errors.New("temporary"))}}
	server, client := newWaitSessionTestServer(t, h)
	defer server.Close()
	session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_CREATING})
	if err := session.WaitReady(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if h.pollCalls.Load() != 2 || !session.State.IsReady() {
		t.Fatalf("poll calls = %d, want a second attempt after the stalled one", h.pollCalls.Load())
	}
}
