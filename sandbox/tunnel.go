package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type HostPortTunnelOptions struct {
	SandboxBindAddress  string
	SandboxPort         uint32
	HostDialTargetLabel string
}

type TunnelTerminationReason string

const (
	TunnelTerminationSDKClosed        TunnelTerminationReason = "sdk_closed"
	TunnelTerminationHostClosed       TunnelTerminationReason = "host_closed"
	TunnelTerminationHostError        TunnelTerminationReason = "host_error"
	TunnelTerminationEngineTerminated TunnelTerminationReason = "engine_terminated"
	TunnelTerminationEngineDraining   TunnelTerminationReason = "engine_draining"
	TunnelTerminationTransportError   TunnelTerminationReason = "transport_error"
	TunnelTerminationTimeout          TunnelTerminationReason = "timeout"
)

type HostPortTunnelTermination struct {
	Reason TunnelTerminationReason
	Detail string
	Err    error
}

type HostPortTunnel struct {
	SandboxPort    uint32
	SandboxAddress string

	stream   hostPortTunnelStream
	once     sync.Once
	sendMu   sync.Mutex
	termOnce sync.Once
	termCh   chan HostPortTunnelTermination
	termMu   sync.Mutex
	term     HostPortTunnelTermination
	done     bool
	onTerm   []func(HostPortTunnelTermination)
}

type hostPortTunnelStream interface {
	Send(*sandboxv1.HostPortTunnelRequest) error
	Receive() (*sandboxv1.HostPortTunnelResponse, error)
	CloseRequest() error
}

type dataPlaneHostPortTunnelStream struct {
	stream *connect.BidiStreamForClient[sandboxv1.SandboxSessionDataPlaneServiceHostPortTunnelRequest, sandboxv1.SandboxSessionDataPlaneServiceHostPortTunnelResponse]
}

func (s *dataPlaneHostPortTunnelStream) Send(req *sandboxv1.HostPortTunnelRequest) error {
	return s.stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceHostPortTunnelRequest{Frame: req})
}

func (s *dataPlaneHostPortTunnelStream) Receive() (*sandboxv1.HostPortTunnelResponse, error) {
	resp, err := s.stream.Receive()
	if err != nil {
		return nil, err
	}
	return resp.GetFrame(), nil
}

func (s *dataPlaneHostPortTunnelStream) CloseRequest() error {
	return s.stream.CloseRequest()
}

func (t *HostPortTunnel) Close() error {
	var err error
	t.once.Do(func() {
		_ = t.send(&sandboxv1.HostPortTunnelRequest{Payload: &sandboxv1.HostPortTunnelRequest_Close{Close: &sandboxv1.HostPortTunnelClose{SubStreamId: 0, Reason: string(TunnelTerminationSDKClosed)}}})
		err = t.stream.CloseRequest()
		t.finish(HostPortTunnelTermination{Reason: TunnelTerminationSDKClosed})
	})
	return err
}

func (t *HostPortTunnel) Terminated() <-chan HostPortTunnelTermination {
	return t.termCh
}

func (t *HostPortTunnel) OnTerminated(cb func(HostPortTunnelTermination)) {
	t.termMu.Lock()
	if t.done {
		termination := t.term
		t.termMu.Unlock()
		go cb(termination)
		return
	}
	t.onTerm = append(t.onTerm, cb)
	t.termMu.Unlock()
}

func (s *Session) ExposeHostPort(ctx context.Context, hostAddr string, opts ...HostPortTunnelOptions) (*HostPortTunnel, error) {
	var opt HostPortTunnelOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.HostDialTargetLabel == "" {
		opt.HostDialTargetLabel = hostAddr
	}

	for attempt := 0; ; attempt++ {
		stream, err := s.hostPortTunnelStream(ctx)
		if err != nil {
			return nil, err
		}
		if err := stream.Send(&sandboxv1.HostPortTunnelRequest{Payload: &sandboxv1.HostPortTunnelRequest_Open{Open: &sandboxv1.HostPortTunnelOpen{
			SessionId:           s.ID,
			SandboxBindAddress:  opt.SandboxBindAddress,
			SandboxPort:         opt.SandboxPort,
			HostDialTargetLabel: opt.HostDialTargetLabel,
		}}}); err != nil {
			if attempt == 0 && s.reauthOnUnauthenticated(ctx, err) {
				_ = stream.CloseRequest()
				continue
			}
			return nil, mapError(err)
		}

		first, err := stream.Receive()
		if err != nil {
			if attempt == 0 && s.reauthOnUnauthenticated(ctx, err) {
				_ = stream.CloseRequest()
				continue
			}
			return nil, mapError(err)
		}
		if terminated := first.GetTerminated(); terminated != nil {
			if terminated.GetReason() == sandboxv1.HostPortTunnelTerminated_REASON_CAPABILITY_UNAVAILABLE {
				if strings.HasPrefix(terminated.GetDetail(), "first_frame_timeout") {
					return nil, &PrimitiveTimeoutError{Primitive: "host_port_tunnel", Message: terminated.GetDetail()}
				}
				return nil, &CapabilityUnavailableError{Primitive: "host_port_tunnel", Message: terminated.GetDetail()}
			}
			return nil, fmt.Errorf("sandbox host port tunnel failed: %s", terminated.GetDetail())
		}
		opened := first.GetOpened()
		if opened == nil {
			return nil, errors.New("sandbox host port tunnel did not open")
		}

		tunnel := &HostPortTunnel{SandboxPort: opened.GetSandboxPort(), SandboxAddress: opened.GetSandboxAddress(), stream: stream, termCh: make(chan HostPortTunnelTermination, 1)}
		go tunnel.forward(hostAddr)
		return tunnel, nil
	}
}

func (s *Session) hostPortTunnelStream(ctx context.Context) (hostPortTunnelStream, error) {
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return nil, err
	}
	return &dataPlaneHostPortTunnelStream{stream: dp.HostPortTunnel(ctx)}, nil
}

// HostPortTunnel is kept for the phase-2 skeleton API; prefer ExposeHostPort.
func (s *Session) HostPortTunnel(ctx context.Context, host string, port int, opts ...HostPortTunnelOptions) (*HostPortTunnel, error) {
	return s.ExposeHostPort(ctx, net.JoinHostPort(host, fmt.Sprint(port)), opts...)
}

func (t *HostPortTunnel) forward(hostAddr string) {
	var mu sync.Mutex
	conns := map[uint64]net.Conn{}
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for id, conn := range conns {
			_ = conn.Close()
			delete(conns, id)
		}
	}()

	for {
		frame, err := t.stream.Receive()
		if err != nil {
			t.finish(HostPortTunnelTermination{Reason: TunnelTerminationTransportError, Err: err})
			return
		}
		switch payload := frame.GetPayload().(type) {
		case *sandboxv1.HostPortTunnelResponse_Accept:
			id := payload.Accept.GetSubStreamId()
			conn, err := net.Dial("tcp", hostAddr)
			if err != nil {
				_ = t.send(&sandboxv1.HostPortTunnelRequest{Payload: &sandboxv1.HostPortTunnelRequest_Close{Close: &sandboxv1.HostPortTunnelClose{SubStreamId: id, Reason: err.Error()}}})
				continue
			}
			mu.Lock()
			conns[id] = conn
			mu.Unlock()
			go t.pumpHostToSandbox(id, conn, &mu, conns)
		case *sandboxv1.HostPortTunnelResponse_Data:
			mu.Lock()
			conn := conns[payload.Data.GetSubStreamId()]
			mu.Unlock()
			if conn != nil {
				_, _ = conn.Write(payload.Data.GetPayload())
			}
		case *sandboxv1.HostPortTunnelResponse_HalfClose:
			mu.Lock()
			conn := conns[payload.HalfClose.GetSubStreamId()]
			mu.Unlock()
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
		case *sandboxv1.HostPortTunnelResponse_Close:
			id := payload.Close.GetSubStreamId()
			mu.Lock()
			conn := conns[id]
			delete(conns, id)
			mu.Unlock()
			if conn != nil {
				_ = conn.Close()
			}
		case *sandboxv1.HostPortTunnelResponse_Ping:
			_ = t.send(&sandboxv1.HostPortTunnelRequest{Payload: &sandboxv1.HostPortTunnelRequest_Pong{Pong: &sandboxv1.HostPortTunnelKeepalivePong{PingId: payload.Ping.GetPingId()}}})
		case *sandboxv1.HostPortTunnelResponse_Terminated:
			t.finish(mapHostPortTunnelTermination(payload.Terminated))
			return
		}
	}
}

func (t *HostPortTunnel) finish(termination HostPortTunnelTermination) {
	t.termOnce.Do(func() {
		// Fan out callbacks so OnTerminated is safe for multiple and late subscribers.
		t.termMu.Lock()
		t.term = termination
		t.done = true
		listeners := append([]func(HostPortTunnelTermination){}, t.onTerm...)
		t.onTerm = nil
		t.termMu.Unlock()
		t.termCh <- termination
		close(t.termCh)
		for _, listener := range listeners {
			listener(termination)
		}
	})
}

func (t *HostPortTunnel) pumpHostToSandbox(id uint64, conn net.Conn, mu *sync.Mutex, conns map[uint64]net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			_ = t.send(&sandboxv1.HostPortTunnelRequest{Payload: &sandboxv1.HostPortTunnelRequest_Data{Data: &sandboxv1.HostPortTunnelData{SubStreamId: id, Payload: append([]byte(nil), buf[:n]...)}}})
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = t.send(&sandboxv1.HostPortTunnelRequest{Payload: &sandboxv1.HostPortTunnelRequest_HalfClose{HalfClose: &sandboxv1.HostPortTunnelHalfClose{SubStreamId: id, ClientToServer: true}}})
			} else {
				_ = t.send(&sandboxv1.HostPortTunnelRequest{Payload: &sandboxv1.HostPortTunnelRequest_Close{Close: &sandboxv1.HostPortTunnelClose{SubStreamId: id, Reason: err.Error()}}})
			}
			mu.Lock()
			delete(conns, id)
			mu.Unlock()
			return
		}
	}
}

func (t *HostPortTunnel) send(frame *sandboxv1.HostPortTunnelRequest) error {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	return t.stream.Send(frame)
}

func mapHostPortTunnelTermination(terminated *sandboxv1.HostPortTunnelTerminated) HostPortTunnelTermination {
	if terminated == nil {
		return HostPortTunnelTermination{Reason: TunnelTerminationEngineTerminated}
	}
	switch terminated.GetReason() {
	case sandboxv1.HostPortTunnelTerminated_REASON_KEEPALIVE_TIMEOUT:
		return HostPortTunnelTermination{Reason: TunnelTerminationTimeout, Detail: terminated.GetDetail()}
	case sandboxv1.HostPortTunnelTerminated_REASON_SESSION_LOST:
		return HostPortTunnelTermination{Reason: TunnelTerminationEngineTerminated, Detail: terminated.GetDetail()}
	case sandboxv1.HostPortTunnelTerminated_REASON_ENGINE_DRAINING:
		return HostPortTunnelTermination{Reason: TunnelTerminationEngineDraining, Detail: terminated.GetDetail()}
	case sandboxv1.HostPortTunnelTerminated_REASON_BIND_FAILED,
		sandboxv1.HostPortTunnelTerminated_REASON_PLATFORM_ERROR,
		sandboxv1.HostPortTunnelTerminated_REASON_CAPABILITY_UNAVAILABLE:
		return HostPortTunnelTermination{Reason: TunnelTerminationHostError, Detail: terminated.GetDetail()}
	default:
		return HostPortTunnelTermination{Reason: TunnelTerminationEngineTerminated, Detail: terminated.GetDetail()}
	}
}

type ResilientHostPortTunnelOptions struct {
	HostPortTunnelOptions
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type ResilientHostPortTunnelState string

const (
	ResilientHostPortTunnelStateOpen         ResilientHostPortTunnelState = "open"
	ResilientHostPortTunnelStateReconnecting ResilientHostPortTunnelState = "reconnecting"
	ResilientHostPortTunnelStateClosed       ResilientHostPortTunnelState = "closed"
)

type ResilientHostPortTunnelStateEvent struct {
	State        string
	Reason       HostPortTunnelTermination
	Attempt      int
	Delay        time.Duration
	PreviousPort uint32
	SandboxPort  uint32
}

type ResilientHostPortTunnel struct {
	SandboxPort    uint32
	SandboxAddress string

	ctx       context.Context
	cancel    context.CancelFunc
	session   *Session
	hostAddr  string
	options   ResilientHostPortTunnelOptions
	current   *HostPortTunnel
	state     ResilientHostPortTunnelState
	mu        sync.RWMutex
	termOnce  sync.Once
	termCh    chan HostPortTunnelTermination
	termMu    sync.Mutex
	term      HostPortTunnelTermination
	done      bool
	onTerm    []func(HostPortTunnelTermination)
	stateMu   sync.Mutex
	listeners []func(ResilientHostPortTunnelStateEvent)
}

func (s *Session) ExposeHostPortResilient(ctx context.Context, hostAddr string, opts ...ResilientHostPortTunnelOptions) (*ResilientHostPortTunnel, error) {
	var opt ResilientHostPortTunnelOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.InitialBackoff <= 0 {
		opt.InitialBackoff = 250 * time.Millisecond
	}
	if opt.MaxBackoff <= 0 {
		opt.MaxBackoff = 10 * time.Second
	}
	childCtx, cancel := context.WithCancel(ctx)
	current, err := s.ExposeHostPort(childCtx, hostAddr, opt.HostPortTunnelOptions)
	if err != nil {
		cancel()
		return nil, err
	}
	tunnel := &ResilientHostPortTunnel{
		SandboxPort:    current.SandboxPort,
		SandboxAddress: current.SandboxAddress,
		ctx:            childCtx,
		cancel:         cancel,
		session:        s,
		hostAddr:       hostAddr,
		options:        opt,
		current:        current,
		state:          ResilientHostPortTunnelStateOpen,
		termCh:         make(chan HostPortTunnelTermination, 1),
	}
	go tunnel.reconnectLoop()
	return tunnel, nil
}

func (s *Session) ResilientHostPortTunnel(ctx context.Context, host string, port int, opts ...ResilientHostPortTunnelOptions) (*ResilientHostPortTunnel, error) {
	return s.ExposeHostPortResilient(ctx, net.JoinHostPort(host, fmt.Sprint(port)), opts...)
}

func (t *ResilientHostPortTunnel) Close() error {
	t.cancel()
	t.mu.RLock()
	current := t.current
	t.mu.RUnlock()
	var err error
	if current != nil {
		err = current.Close()
	}
	t.finish(HostPortTunnelTermination{Reason: TunnelTerminationSDKClosed})
	return err
}

func (t *ResilientHostPortTunnel) State() ResilientHostPortTunnelState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

func (t *ResilientHostPortTunnel) Terminated() <-chan HostPortTunnelTermination {
	return t.termCh
}

func (t *ResilientHostPortTunnel) OnTerminated(cb func(HostPortTunnelTermination)) {
	t.termMu.Lock()
	if t.done {
		termination := t.term
		t.termMu.Unlock()
		go cb(termination)
		return
	}
	t.onTerm = append(t.onTerm, cb)
	t.termMu.Unlock()
}

func (t *ResilientHostPortTunnel) OnStateChange(cb func(ResilientHostPortTunnelStateEvent)) {
	t.stateMu.Lock()
	t.listeners = append(t.listeners, cb)
	t.stateMu.Unlock()
}

func (t *ResilientHostPortTunnel) reconnectLoop() {
	attempt := 0
	for {
		t.mu.RLock()
		current := t.current
		t.mu.RUnlock()
		termination := <-current.Terminated()
		if t.ctx.Err() != nil {
			return
		}
		if !shouldReconnectTunnel(termination.Reason) {
			t.finish(termination)
			return
		}
		for {
			attempt++
			var delay time.Duration
			if termination.Reason == TunnelTerminationEngineDraining {
				// Rolling deploys have already sent HTTP/2 GOAWAY, so reconnect immediately to a healthy engine.
				attempt = 0
			} else {
				delay = jitteredBackoff(t.options.InitialBackoff, t.options.MaxBackoff, attempt)
			}
			t.setState(ResilientHostPortTunnelStateReconnecting)
			t.emit(ResilientHostPortTunnelStateEvent{State: "reconnecting", Reason: termination, Attempt: attempt, Delay: delay})
			timer := time.NewTimer(delay)
			select {
			case <-t.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			next, err := t.session.ExposeHostPort(t.ctx, t.hostAddr, t.options.HostPortTunnelOptions)
			if err != nil {
				termination = HostPortTunnelTermination{Reason: TunnelTerminationTransportError, Err: err}
				continue
			}
			attempt = 0
			t.mu.Lock()
			previousPort := t.SandboxPort
			t.current = next
			t.SandboxPort = next.SandboxPort
			t.SandboxAddress = next.SandboxAddress
			t.state = ResilientHostPortTunnelStateOpen
			t.mu.Unlock()
			t.emit(ResilientHostPortTunnelStateEvent{State: "open", SandboxPort: next.SandboxPort})
			if previousPort != next.SandboxPort {
				t.emit(ResilientHostPortTunnelStateEvent{State: "port-changed", PreviousPort: previousPort, SandboxPort: next.SandboxPort})
			}
			break
		}
	}
}

func (t *ResilientHostPortTunnel) setState(state ResilientHostPortTunnelState) {
	t.mu.Lock()
	t.state = state
	t.mu.Unlock()
}

func (t *ResilientHostPortTunnel) finish(termination HostPortTunnelTermination) {
	t.termOnce.Do(func() {
		t.setState(ResilientHostPortTunnelStateClosed)
		// Fan out callbacks so OnTerminated is safe for multiple and late subscribers.
		t.termMu.Lock()
		t.term = termination
		t.done = true
		listeners := append([]func(HostPortTunnelTermination){}, t.onTerm...)
		t.onTerm = nil
		t.termMu.Unlock()
		t.termCh <- termination
		close(t.termCh)
		for _, listener := range listeners {
			listener(termination)
		}
		t.emit(ResilientHostPortTunnelStateEvent{State: "closed", Reason: termination})
	})
}

func (t *ResilientHostPortTunnel) emit(event ResilientHostPortTunnelStateEvent) {
	t.stateMu.Lock()
	listeners := append([]func(ResilientHostPortTunnelStateEvent){}, t.listeners...)
	t.stateMu.Unlock()
	for _, listener := range listeners {
		listener(event)
	}
}

func shouldReconnectTunnel(reason TunnelTerminationReason) bool {
	return reason == TunnelTerminationTransportError ||
		reason == TunnelTerminationEngineTerminated ||
		reason == TunnelTerminationEngineDraining ||
		reason == TunnelTerminationTimeout
}

func jitteredBackoff(initialBackoff, maxBackoff time.Duration, attempt int) time.Duration {
	base := initialBackoff * time.Duration(1<<max(0, attempt-1))
	if base > maxBackoff {
		base = maxBackoff
	}
	jitter := 0.75 + rand.Float64()*0.5
	return time.Duration(float64(base) * jitter)
}
