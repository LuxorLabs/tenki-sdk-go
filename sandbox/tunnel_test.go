package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type tunnelTestHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	mu        sync.Mutex
	hostAddrs []string
	scripts   chan *tunnelServerScript
}

type tunnelServerScript struct {
	port      uint32
	responses chan *sandboxv1.HostPortTunnelResponse
	err       error
	closeOnce sync.Once
}

type dataPlaneTunnelTestHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	control *tunnelTestHandler
}

func (h *dataPlaneTunnelTestHandler) Stat(
	_ context.Context,
	_ *connect.Request[sandboxv1.SandboxSessionDataPlaneServiceStatRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceStatResponse], error) {
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceStatResponse{
		Response: &sandboxv1.StatResponse{Exists: true, IsDir: true},
	}), nil
}

func (h *dataPlaneTunnelTestHandler) HostPortTunnel(
	_ context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceHostPortTunnelRequest, sandboxv1.SandboxSessionDataPlaneServiceHostPortTunnelResponse],
) error {
	script := <-h.control.scripts
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	h.control.mu.Lock()
	h.control.hostAddrs = append(h.control.hostAddrs, first.GetFrame().GetOpen().GetHostDialTargetLabel())
	h.control.mu.Unlock()
	if script.err != nil && script.port == 0 {
		return script.err
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceHostPortTunnelResponse{Frame: openedTunnel(script.port)}); err != nil {
		return err
	}
	for response := range script.responses {
		if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceHostPortTunnelResponse{Frame: response}); err != nil {
			return err
		}
	}
	if script.err != nil {
		return script.err
	}
	return nil
}

func (h *tunnelTestHandler) CreateSessionCredential(
	_ context.Context,
	_ *connect.Request[sandboxv1.CreateSessionCredentialRequest],
) (*connect.Response[sandboxv1.CreateSessionCredentialResponse], error) {
	return connect.NewResponse(&sandboxv1.CreateSessionCredentialResponse{
		Credential: testSessionCredential("tunnel-test", time.Now().Add(time.Hour)),
	}), nil
}

func (h *tunnelTestHandler) HostPortTunnel(_ context.Context, stream *connect.BidiStream[sandboxv1.HostPortTunnelRequest, sandboxv1.HostPortTunnelResponse]) error {
	script := <-h.scripts
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.hostAddrs = append(h.hostAddrs, first.GetOpen().GetHostDialTargetLabel())
	h.mu.Unlock()
	if err := stream.Send(openedTunnel(script.port)); err != nil {
		return err
	}
	for response := range script.responses {
		if err := stream.Send(response); err != nil {
			return err
		}
	}
	if script.err != nil {
		return script.err
	}
	return nil
}

func (h *tunnelTestHandler) calledHostAddrs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.hostAddrs...)
}

func newTunnelTestSession(t *testing.T, scripts ...*tunnelServerScript) (*Session, *tunnelTestHandler) {
	t.Helper()

	h := &tunnelTestHandler{scripts: make(chan *tunnelServerScript, len(scripts))}
	for _, script := range scripts {
		h.scripts <- script
	}

	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(h)
	mux.Handle(path, svc)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	dataMux := http.NewServeMux()
	dataPath, dataSvc := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(&dataPlaneTunnelTestHandler{control: h})
	dataMux.Handle(dataPath, dataSvc)
	dataServer := httptest.NewServer(h2c.NewHandler(dataMux, &http2.Server{}))
	t.Cleanup(dataServer.Close)
	t.Cleanup(func() {
		for _, script := range scripts {
			script.closeResponses()
		}
	})

	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	session := &Session{client: client, ID: "session-1"}
	session.configureDataPlane(dataServer.URL, testSessionCredential("tunnel-test", time.Now().Add(time.Hour)))
	return session, h
}

func newTunnelScript(port uint32) *tunnelServerScript {
	return &tunnelServerScript{port: port, responses: make(chan *sandboxv1.HostPortTunnelResponse, 1)}
}

func (s *tunnelServerScript) closeResponses() {
	s.closeOnce.Do(func() {
		close(s.responses)
	})
}

func openedTunnel(port uint32) *sandboxv1.HostPortTunnelResponse {
	return &sandboxv1.HostPortTunnelResponse{Payload: &sandboxv1.HostPortTunnelResponse_Opened{Opened: &sandboxv1.HostPortTunnelOpened{
		SandboxAddress: "127.0.0.1",
		SandboxPort:    port,
	}}}
}

func terminatedTunnel(reason sandboxv1.HostPortTunnelTerminated_Reason, detail string) *sandboxv1.HostPortTunnelResponse {
	return &sandboxv1.HostPortTunnelResponse{Payload: &sandboxv1.HostPortTunnelResponse_Terminated{Terminated: &sandboxv1.HostPortTunnelTerminated{
		Reason: reason,
		Detail: detail,
	}}}
}

func receiveTermination(t *testing.T, ch <-chan HostPortTunnelTermination) HostPortTunnelTermination {
	t.Helper()
	select {
	case termination := <-ch:
		return termination
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tunnel termination")
		return HostPortTunnelTermination{}
	}
}

func waitForTunnelTest(t *testing.T, predicate func() bool) {
	t.Helper()
	for range 200 {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestHostPortTunnelTerminatedReasons(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   sandboxv1.HostPortTunnelTerminated_Reason
		want TunnelTerminationReason
	}{
		{"unspecified", sandboxv1.HostPortTunnelTerminated_REASON_UNSPECIFIED, TunnelTerminationEngineTerminated},
		{"bind_failed", sandboxv1.HostPortTunnelTerminated_REASON_BIND_FAILED, TunnelTerminationHostError},
		{"keepalive_timeout", sandboxv1.HostPortTunnelTerminated_REASON_KEEPALIVE_TIMEOUT, TunnelTerminationTimeout},
		{"session_lost", sandboxv1.HostPortTunnelTerminated_REASON_SESSION_LOST, TunnelTerminationEngineTerminated},
		{"platform_error", sandboxv1.HostPortTunnelTerminated_REASON_PLATFORM_ERROR, TunnelTerminationHostError},
		{"capability_unavailable", sandboxv1.HostPortTunnelTerminated_REASON_CAPABILITY_UNAVAILABLE, TunnelTerminationHostError},
		{"engine_draining", sandboxv1.HostPortTunnelTerminated_REASON_ENGINE_DRAINING, TunnelTerminationEngineDraining},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := newTunnelScript(9000)
			session, _ := newTunnelTestSession(t, script)
			tunnel, err := session.ExposeHostPort(context.Background(), "127.0.0.1:1234")
			if err != nil {
				t.Fatalf("ExposeHostPort: %v", err)
			}

			var callbacksMu sync.Mutex
			callbacks := []HostPortTunnelTermination{}
			tunnel.OnTerminated(func(termination HostPortTunnelTermination) {
				callbacksMu.Lock()
				callbacks = append(callbacks, termination)
				callbacksMu.Unlock()
			})
			tunnel.OnTerminated(func(termination HostPortTunnelTermination) {
				callbacksMu.Lock()
				callbacks = append(callbacks, termination)
				callbacksMu.Unlock()
			})
			script.responses <- terminatedTunnel(tc.in, "detail")

			termination := receiveTermination(t, tunnel.Terminated())
			if termination.Reason != tc.want || termination.Detail != "detail" {
				t.Fatalf("unexpected termination: %#v", termination)
			}
			waitForTunnelTest(t, func() bool {
				callbacksMu.Lock()
				defer callbacksMu.Unlock()
				return len(callbacks) == 2
			})
			tunnel.OnTerminated(func(termination HostPortTunnelTermination) {
				callbacksMu.Lock()
				callbacks = append(callbacks, termination)
				callbacksMu.Unlock()
			})
			waitForTunnelTest(t, func() bool {
				callbacksMu.Lock()
				defer callbacksMu.Unlock()
				return len(callbacks) == 3 && callbacks[2].Reason == tc.want
			})
		})
	}
}

func TestHostPortTunnelCloseTerminatesSDKClosed(t *testing.T) {
	t.Parallel()

	script := newTunnelScript(9001)
	session, _ := newTunnelTestSession(t, script)
	tunnel, err := session.ExposeHostPort(context.Background(), "127.0.0.1:1234")
	if err != nil {
		t.Fatalf("ExposeHostPort: %v", err)
	}
	if err := tunnel.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	termination := receiveTermination(t, tunnel.Terminated())
	if termination.Reason != TunnelTerminationSDKClosed {
		t.Fatalf("unexpected termination: %#v", termination)
	}
}

func TestHostPortTunnelReceiveErrorTerminatesTransportErrorAndClosesSockets(t *testing.T) {
	t.Parallel()

	serverSockets := make(chan net.Conn, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			serverSockets <- conn
		}
	}()

	script := newTunnelScript(9002)
	session, _ := newTunnelTestSession(t, script)
	tunnel, err := session.ExposeHostPort(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatalf("ExposeHostPort: %v", err)
	}
	script.responses <- &sandboxv1.HostPortTunnelResponse{Payload: &sandboxv1.HostPortTunnelResponse_Accept{Accept: &sandboxv1.HostPortTunnelAccept{SubStreamId: 1}}}
	conn := <-serverSockets
	script.err = connect.NewError(connect.CodeUnavailable, errors.New("boom"))
	script.closeResponses()

	termination := receiveTermination(t, tunnel.Terminated())
	if termination.Reason != TunnelTerminationTransportError {
		t.Fatalf("unexpected termination: %#v", termination)
	}
	if termination.Err == nil || !strings.Contains(termination.Err.Error(), "unavailable: boom") {
		t.Fatalf("expected wrapped transport error, got %#v", termination.Err)
	}
	waitForTunnelTest(t, func() bool {
		one := []byte{0}
		_, err := conn.Write(one)
		return err != nil
	})
}

func TestHostPortTunnelMapsEdge404OpenError(t *testing.T) {
	t.Parallel()

	script := newTunnelScript(0)
	script.err = connect.NewError(connect.CodeUnimplemented, errors.New("HTTP status 404 Not Found"))
	session, _ := newTunnelTestSession(t, script)

	_, err := session.ExposeHostPort(context.Background(), "127.0.0.1:1234")
	if !IsDataPlaneNotReady(err) {
		t.Fatalf("expected DataPlaneNotReadyError, got %T: %v", err, err)
	}
}

func TestHostPortTunnelNoGoroutineLeakAfterTermination(t *testing.T) {
	before := runtime.NumGoroutine()
	script := newTunnelScript(9003)
	session, _ := newTunnelTestSession(t, script)
	tunnel, err := session.ExposeHostPort(context.Background(), "127.0.0.1:1234")
	if err != nil {
		t.Fatalf("ExposeHostPort: %v", err)
	}
	script.responses <- terminatedTunnel(sandboxv1.HostPortTunnelTerminated_REASON_SESSION_LOST, "lost")
	_ = receiveTermination(t, tunnel.Terminated())
	waitForTunnelTest(t, func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= before+8
	})
}

func TestResilientHostPortTunnelReconnectsAfterTransportError(t *testing.T) {
	t.Parallel()

	first := newTunnelScript(9100)
	second := newTunnelScript(9101)
	session, handler := newTunnelTestSession(t, first, second)
	tunnel, err := session.ExposeHostPortResilient(context.Background(), "127.0.0.1:1234", ResilientHostPortTunnelOptions{
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ExposeHostPortResilient: %v", err)
	}
	defer func() { _ = tunnel.Close() }()

	events := make(chan string, 4)
	tunnel.OnStateChange(func(event ResilientHostPortTunnelStateEvent) {
		events <- event.State
	})
	first.err = connect.NewError(connect.CodeUnavailable, errors.New("boom"))
	first.closeResponses()
	waitForTunnelTest(t, func() bool { return tunnel.SandboxPort == 9101 && tunnel.State() == ResilientHostPortTunnelStateOpen })
	got := drainStateEvents(events)
	if !slices.Contains(got, "reconnecting") || !slices.Contains(got, "open") {
		t.Fatalf("missing reconnect state events: %#v", got)
	}
	if got := handler.calledHostAddrs(); len(got) != 2 || got[0] != "127.0.0.1:1234" || got[1] != "127.0.0.1:1234" {
		t.Fatalf("unexpected host addr calls: %#v", got)
	}
}

func TestResilientHostPortTunnelRetriesImmediatelyAfterEngineDraining(t *testing.T) {
	t.Parallel()

	first := newTunnelScript(9150)
	second := newTunnelScript(9151)
	session, _ := newTunnelTestSession(t, first, second)
	tunnel, err := session.ExposeHostPortResilient(context.Background(), "127.0.0.1:1234", ResilientHostPortTunnelOptions{
		InitialBackoff: 10 * time.Second,
		MaxBackoff:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("ExposeHostPortResilient: %v", err)
	}
	defer func() { _ = tunnel.Close() }()

	delays := make(chan time.Duration, 1)
	tunnel.OnStateChange(func(event ResilientHostPortTunnelStateEvent) {
		if event.State == "reconnecting" {
			delays <- event.Delay
		}
	})
	first.responses <- terminatedTunnel(sandboxv1.HostPortTunnelTerminated_REASON_ENGINE_DRAINING, "drain")
	waitForTunnelTest(t, func() bool { return tunnel.SandboxPort == 9151 && tunnel.State() == ResilientHostPortTunnelStateOpen })

	select {
	case delay := <-delays:
		if delay > time.Millisecond {
			t.Fatalf("expected immediate retry, got %s", delay)
		}
	default:
		t.Fatal("missing reconnect event")
	}
}

func TestResilientHostPortTunnelRepeatedReconnectsNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	scripts := make([]*tunnelServerScript, 101)
	for i := range scripts {
		scripts[i] = newTunnelScript(uint32(9200 + i))
		if i < 100 {
			scripts[i].err = connect.NewError(connect.CodeUnavailable, fmt.Errorf("boom-%d", i))
			scripts[i].closeResponses()
		}
	}
	session, _ := newTunnelTestSession(t, scripts...)
	tunnel, err := session.ExposeHostPortResilient(context.Background(), "127.0.0.1:1234", ResilientHostPortTunnelOptions{
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ExposeHostPortResilient: %v", err)
	}
	waitForTunnelTest(t, func() bool { return tunnel.SandboxPort == 9300 && tunnel.State() == ResilientHostPortTunnelStateOpen })
	if err := tunnel.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = receiveTermination(t, tunnel.Terminated())
	waitForTunnelTest(t, func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= before+16
	})
}

func TestResilientHostPortTunnelNonRetryableTerminates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		response *sandboxv1.HostPortTunnelResponse
		want     TunnelTerminationReason
	}{
		{"host_error", terminatedTunnel(sandboxv1.HostPortTunnelTerminated_REASON_BIND_FAILED, "bind"), TunnelTerminationHostError},
		{"sdk_closed", nil, TunnelTerminationSDKClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := newTunnelScript(9400)
			session, handler := newTunnelTestSession(t, script)
			tunnel, err := session.ExposeHostPortResilient(context.Background(), "127.0.0.1:1234", ResilientHostPortTunnelOptions{
				InitialBackoff: time.Millisecond,
				MaxBackoff:     time.Millisecond,
			})
			if err != nil {
				t.Fatalf("ExposeHostPortResilient: %v", err)
			}
			if tc.response != nil {
				script.responses <- tc.response
			} else if err := tunnel.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			termination := receiveTermination(t, tunnel.Terminated())
			if termination.Reason != tc.want {
				t.Fatalf("unexpected termination: %#v", termination)
			}
			time.Sleep(20 * time.Millisecond)
			if got := handler.calledHostAddrs(); len(got) != 1 {
				t.Fatalf("unexpected reconnects: %#v", got)
			}
		})
	}
}

func drainStateEvents(events <-chan string) []string {
	var got []string
	for {
		select {
		case event := <-events:
			got = append(got, event)
		default:
			return got
		}
	}
}
