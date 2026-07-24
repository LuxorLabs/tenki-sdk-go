package sandbox

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type Command struct {
	session *Session
	argv    []string
	opts    RunOptions
}

type RunOptions struct {
	Env        map[string]string
	Dir        string
	Stdin      io.Reader
	Timeout    *time.Duration
	Privileged bool
}

type RunHandle struct {
	PID    uint64
	Stdin  io.WriteCloser
	Stdout io.Reader
	Stderr io.Reader

	stream    sandboxv1connectRunStream
	waitCh    chan *Result
	errCh     chan error
	closeOnce sync.Once
}

type sandboxv1connectRunStream interface {
	Send(*sandboxv1.RunRequest) error
	Receive() (*sandboxv1.RunResponse, error)
	CloseRequest() error
}

type dataPlaneRunStream struct {
	stream interface {
		Send(*sandboxv1.SandboxSessionDataPlaneServiceRunRequest) error
		Receive() (*sandboxv1.SandboxSessionDataPlaneServiceRunResponse, error)
		CloseRequest() error
	}
}

func isRetryableRunStreamEstablishmentError(err error) bool {
	if isEdgeNotReady(err) {
		return true
	}
	for current, depth := err, 0; current != nil && depth < 4; current, depth = errors.Unwrap(current), depth+1 {
		msg := strings.ToLower(current.Error())
		if strings.Contains(msg, "refused_stream") || strings.Contains(msg, "refused stream") || strings.Contains(msg, "goaway") {
			return true
		}
	}
	if code := connect.CodeOf(err); code != connect.CodeAborted && code != connect.CodeUnavailable {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "econnreset") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "socket hang up") ||
		strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "stream closed before headers")
}

func waitRunStreamEstablishmentRetry(readyCtx, parent context.Context, attempt int, lastErr error) error {
	if err := waitDataPlaneReadyBackoff(readyCtx, parent, attempt, lastErr); err != nil {
		if isEdgeNotReady(lastErr) || errors.Is(parent.Err(), context.Canceled) {
			return err
		}
		return lastErr
	}
	return nil
}

func (s *dataPlaneRunStream) Send(req *sandboxv1.RunRequest) error {
	return s.stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunRequest{Frame: req})
}

func (s *dataPlaneRunStream) Receive() (*sandboxv1.RunResponse, error) {
	resp, err := s.stream.Receive()
	if err != nil {
		return nil, err
	}
	return resp.GetFrame(), nil
}

func (s *dataPlaneRunStream) CloseRequest() error {
	return s.stream.CloseRequest()
}

func (s *Session) Command(argv []string, opts ...RunOptions) *Command {
	cmd := &Command{session: s, argv: append([]string(nil), argv...)}
	if len(opts) > 0 {
		cmd.opts = opts[0]
	}
	return cmd
}

func (c *Command) Exec(ctx context.Context) (*Result, error) {
	proc, err := c.Stream(ctx)
	if err != nil {
		return nil, err
	}
	// pumpResponses writes stdout/stderr into unbuffered io.Pipes; if no one
	// drains them the writes block and the exit frame is never observed.
	// Exec callers don't get a stream handle, so drain here.
	var drainWG sync.WaitGroup
	drainWG.Add(2)
	go func() {
		defer drainWG.Done()
		_, _ = io.Copy(io.Discard, proc.Stdout)
	}()
	go func() {
		defer drainWG.Done()
		_, _ = io.Copy(io.Discard, proc.Stderr)
	}()
	if c.opts.Stdin != nil {
		go func() {
			_, _ = io.Copy(proc.Stdin, c.opts.Stdin)
			_ = proc.Stdin.Close()
		}()
	} else {
		_ = proc.Stdin.Close()
	}
	result, err := proc.Wait()
	drainWG.Wait()
	return result, err
}

func (c *Command) Stream(ctx context.Context) (*RunHandle, error) {
	if c == nil || c.session == nil || c.session.client == nil {
		return nil, errors.New("sandbox: nil command session")
	}
	if len(c.argv) == 0 || c.argv[0] == "" {
		return nil, errors.New("sandbox: empty command")
	}
	stream, started, err := c.openRunStream(ctx)
	if err != nil {
		return nil, err
	}

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	h := &RunHandle{
		PID:    started.GetPid(),
		Stdin:  stdinWriter,
		Stdout: stdoutReader,
		Stderr: stderrReader,
		stream: stream,
		waitCh: make(chan *Result, 1),
		errCh:  make(chan error, 1),
	}
	go h.pumpStdin(stdinReader)
	go h.pumpResponses(stdoutWriter, stderrWriter)
	return h, nil
}

// openRunStream opens the Run stream and reads its first frame, re-minting the
// credential and retrying the open once on an Unauthenticated reject.
func (c *Command) openRunStream(ctx context.Context) (*dataPlaneRunStream, *sandboxv1.RunStarted, error) {
	readyCtx, cancel := dataPlaneReadyContext(ctx, c.session.client.dataPlaneReadyTimeout)
	defer cancel()
	reauthAttempted := false
	for attempt := 0; ; attempt++ {
		dp, err := c.session.dataPlane(ctx)
		if err != nil {
			return nil, nil, err
		}
		stream := &dataPlaneRunStream{stream: dp.Run(ctx)}
		start := &sandboxv1.RunStart{
			SessionId:   c.session.ID,
			Cmd:         append([]string(nil), c.argv...),
			Cwd:         c.opts.Dir,
			Env:         c.opts.Env,
			StreamStdin: true,
			Privileged:  c.opts.Privileged,
		}
		if c.opts.Timeout != nil && *c.opts.Timeout > 0 {
			start.TimeoutMs = uint32(*c.opts.Timeout / time.Millisecond)
		}
		if err := stream.Send(&sandboxv1.RunRequest{Payload: &sandboxv1.RunRequest_Start{Start: start}}); err != nil {
			if !reauthAttempted && c.session.reauthOnUnauthenticated(ctx, err) {
				reauthAttempted = true
				_ = stream.CloseRequest()
				continue
			}
			if isRetryableRunStreamEstablishmentError(err) {
				_ = stream.CloseRequest()
				if waitErr := waitRunStreamEstablishmentRetry(readyCtx, ctx, attempt, err); waitErr != nil {
					return nil, nil, waitErr
				}
				continue
			}
			if isRunUnimplemented(err) {
				return nil, nil, &CapabilityUnavailableError{Primitive: "run", Message: err.Error()}
			}
			return nil, nil, err
		}
		first, err := stream.Receive()
		if err != nil {
			if !reauthAttempted && c.session.reauthOnUnauthenticated(ctx, err) {
				reauthAttempted = true
				_ = stream.CloseRequest()
				continue
			}
			if isRetryableRunStreamEstablishmentError(err) {
				_ = stream.CloseRequest()
				if waitErr := waitRunStreamEstablishmentRetry(readyCtx, ctx, attempt, err); waitErr != nil {
					return nil, nil, waitErr
				}
				continue
			}
			if isRunUnimplemented(err) {
				return nil, nil, &CapabilityUnavailableError{Primitive: "run", Message: err.Error()}
			}
			return nil, nil, mapError(err)
		}
		if exit := first.GetExit(); exit != nil {
			if isRunFirstFrameTimeout(exit) {
				return nil, nil, &PrimitiveTimeoutError{Primitive: "run", Message: exit.GetReason()}
			}
			if isRunCapabilityUnavailable(exit) {
				return nil, nil, &CapabilityUnavailableError{Primitive: "run", Message: exit.GetReason()}
			}
			return nil, nil, errors.New(exit.GetReason())
		}
		started := first.GetStarted()
		if started == nil {
			return nil, nil, errors.New("sandbox run did not start")
		}
		return stream, started, nil
	}
}

func (h *RunHandle) pumpStdin(r *io.PipeReader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if sendErr := h.stream.Send(&sandboxv1.RunRequest{Payload: &sandboxv1.RunRequest_Stdin{Stdin: append([]byte(nil), buf[:n]...)}}); sendErr != nil {
				_ = r.CloseWithError(sendErr)
				return
			}
		}
		if err != nil {
			_ = h.stream.Send(&sandboxv1.RunRequest{Payload: &sandboxv1.RunRequest_StdinClose{StdinClose: true}})
			return
		}
	}
}

func (h *RunHandle) pumpResponses(stdout, stderr *io.PipeWriter) {
	defer h.closeRequest()
	defer stdout.Close()
	defer stderr.Close()
	result := &Result{Status: CommandStatusRunning}
	for {
		frame, err := h.stream.Receive()
		if err != nil {
			if isEdgeNotReady(err) {
				h.errCh <- dataPlaneNotReadyError(err)
			} else {
				h.errCh <- err
			}
			return
		}
		if data := frame.GetStdout(); len(data) > 0 {
			result.Stdout = append(result.Stdout, data...)
			_, _ = stdout.Write(data)
		}
		if data := frame.GetStderr(); len(data) > 0 {
			result.Stderr = append(result.Stderr, data...)
			_, _ = stderr.Write(data)
		}
		if exit := frame.GetExit(); exit != nil {
			if isRunFirstFrameTimeout(exit) {
				h.errCh <- &PrimitiveTimeoutError{Primitive: "run", Message: exit.GetReason()}
				return
			}
			if isRunCapabilityUnavailable(exit) {
				h.errCh <- &CapabilityUnavailableError{Primitive: "run", Message: exit.GetReason()}
				return
			}
			result.ExitCode = exit.GetExitCode()
			result.Duration = time.Duration(exit.GetDurationMs()) * time.Millisecond
			if exit.GetExitCode() == 0 && exit.GetSignal() == "" {
				result.Status = CommandStatusSucceeded
			} else if exit.GetTimedOut() {
				result.Status = CommandStatusTimedOut
			} else {
				result.Status = CommandStatusFailed
			}
			h.waitCh <- result
			return
		}
	}
}

func (h *RunHandle) Signal(signal os.Signal) error {
	return h.stream.Send(&sandboxv1.RunRequest{Payload: &sandboxv1.RunRequest_Signal{Signal: &sandboxv1.RunSignal{
		Signal: osSignalToRunSignal(signal),
	}}})
}

func (h *RunHandle) Kill() error {
	return h.Signal(os.Kill)
}

func (h *RunHandle) Wait() (*Result, error) {
	select {
	case result := <-h.waitCh:
		return result, nil
	case err := <-h.errCh:
		return nil, err
	}
}

func streamFromRunHandle(h *RunHandle) *Stream {
	stream := &Stream{
		cancel: func() {
			_ = h.Kill()
		},
		chunks:      make(chan Output, streamBufferSize),
		drainCh:     make(chan struct{}),
		resultReady: make(chan struct{}),
		result:      &Result{Status: CommandStatusRunning},
	}
	go stream.receiveRunHandle(h)
	return stream
}

func (s *Stream) receiveRunHandle(h *RunHandle) {
	defer close(s.chunks)
	defer close(s.resultReady)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyRunOutput(s, h.Stdout, false)
	}()
	go func() {
		defer wg.Done()
		copyRunOutput(s, h.Stderr, true)
	}()

	result, err := h.Wait()
	wg.Wait()
	if err != nil {
		s.setErr(err)
		return
	}
	s.mu.Lock()
	s.result = result
	s.mu.Unlock()
}

func copyRunOutput(s *Stream, r io.Reader, stderr bool) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if !s.emit(Output{Data: append([]byte(nil), buf[:n]...), IsStderr: stderr}) {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *RunHandle) closeRequest() {
	h.closeOnce.Do(func() {
		_ = h.stream.CloseRequest()
	})
}

func isRunCapabilityUnavailable(exit *sandboxv1.RunExit) bool {
	return strings.HasPrefix(exit.GetReason(), "capability_unavailable")
}

func isRunFirstFrameTimeout(exit *sandboxv1.RunExit) bool {
	return strings.HasPrefix(exit.GetReason(), "first_frame_timeout")
}

func isRunUnimplemented(err error) bool {
	return connect.CodeOf(err) == connect.CodeUnimplemented
}

func osSignalToRunSignal(signal os.Signal) sandboxv1.RunSignal_Sig {
	switch signal.String() {
	case "killed", "kill":
		return sandboxv1.RunSignal_SIG_KILL
	case "interrupt", "int":
		return sandboxv1.RunSignal_SIG_INT
	case "hangup", "hup":
		return sandboxv1.RunSignal_SIG_HUP
	case "user defined signal 1", "usr1":
		return sandboxv1.RunSignal_SIG_USR1
	case "user defined signal 2", "usr2":
		return sandboxv1.RunSignal_SIG_USR2
	default:
		return sandboxv1.RunSignal_SIG_TERM
	}
}
