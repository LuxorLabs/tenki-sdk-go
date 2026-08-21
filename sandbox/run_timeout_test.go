package sandbox

import (
	"errors"
	"io"
	"math"
	"testing"
	"time"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

func TestRunTimeoutMsRendersTheWireValue(t *testing.T) {
	budget := 30 * time.Second
	zero := time.Duration(0)
	negative := -5 * time.Second
	// Long enough that a uint32 millisecond conversion wraps.
	huge := 100 * 24 * time.Hour

	for _, tc := range []struct {
		name    string
		timeout *time.Duration
		want    uint32
	}{
		{"unset is unbounded", nil, 0},
		{"zero is unbounded", &zero, 0},
		{"negative is unbounded", &negative, 0},
		{"positive converts to milliseconds", &budget, 30_000},
		{"oversized clamps instead of wrapping", &huge, math.MaxUint32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runTimeoutMs(tc.timeout); got != tc.want {
				t.Errorf("runTimeoutMs() = %d, want %d", got, tc.want)
			}
		})
	}
}

// frameStream replays fixed response frames, then blocks like a quiet guest.
type frameStream struct {
	frames []*sandboxv1.RunResponse
	next   int
	block  chan struct{}
}

func (s *frameStream) Send(*sandboxv1.RunRequest) error { return nil }

func (s *frameStream) Receive() (*sandboxv1.RunResponse, error) {
	if s.next < len(s.frames) {
		frame := s.frames[s.next]
		s.next++
		return frame, nil
	}
	<-s.block
	return nil, io.EOF
}

func (s *frameStream) CloseRequest() error { return nil }

func stdoutFrame(data string) *sandboxv1.RunResponse {
	return &sandboxv1.RunResponse{Payload: &sandboxv1.RunResponse_Stdout{Stdout: []byte(data)}}
}

func stderrFrame(data string) *sandboxv1.RunResponse {
	return &sandboxv1.RunResponse{Payload: &sandboxv1.RunResponse_Stderr{Stderr: []byte(data)}}
}

func exitFrame(exit *sandboxv1.RunExit) *sandboxv1.RunResponse {
	return &sandboxv1.RunResponse{Payload: &sandboxv1.RunResponse_Exit{Exit: exit}}
}

func resultFromFrames(t *testing.T, frames ...*sandboxv1.RunResponse) *Result {
	t.Helper()
	h := newFrameHandle(t, frames...)
	result, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v, want nil", err)
	}
	return result
}

func newFrameHandle(t *testing.T, frames ...*sandboxv1.RunResponse) *RunHandle {
	t.Helper()
	stream := &frameStream{frames: frames, block: make(chan struct{})}
	t.Cleanup(func() { close(stream.block) })
	h := &RunHandle{
		stream: stream,
		waitCh: make(chan *Result, 1),
		errCh:  make(chan error, 1),
	}
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, stdoutR) }()
	go func() { _, _ = io.Copy(io.Discard, stderrR) }()
	go h.pumpResponses(stdoutW, stderrW)
	return h
}

// A timed-out run can carry exit_code 0. Reverse the switch arms in
// pumpResponses and this test fails.
func TestExitFrameTimedOutOutranksZeroExitCode(t *testing.T) {
	result := resultFromFrames(t, exitFrame(&sandboxv1.RunExit{
		ExitCode: 0,
		TimedOut: true,
		Reason:   "timeout",
	}))

	if result.Status != CommandStatusTimedOut {
		t.Errorf("Status = %s, want %s for a timed-out run that exited 0", result.Status, CommandStatusTimedOut)
	}
	if result.Reason != "timeout" {
		t.Errorf("Reason = %q, want the guest's terminal reason to survive", result.Reason)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want the child's real status preserved", result.ExitCode)
	}
}

func TestExitFrameGraceTimeoutIsDistinguishable(t *testing.T) {
	result := resultFromFrames(t, exitFrame(&sandboxv1.RunExit{
		ExitCode: -1,
		TimedOut: true,
		Reason:   "grace_timeout",
	}))

	if result.Status != CommandStatusTimedOut {
		t.Errorf("Status = %s, want %s", result.Status, CommandStatusTimedOut)
	}
	if result.Reason != "grace_timeout" {
		t.Errorf("Reason = %q, want grace_timeout preserved", result.Reason)
	}
}

func TestExitFrameCleanExitStillSucceeds(t *testing.T) {
	result := resultFromFrames(t, exitFrame(&sandboxv1.RunExit{ExitCode: 0, Reason: "exit"}))

	if result.Status != CommandStatusSucceeded {
		t.Errorf("Status = %s, want %s", result.Status, CommandStatusSucceeded)
	}
	if result.Reason != "exit" {
		t.Errorf("Reason = %q, want %q", result.Reason, "exit")
	}
}

func TestExitFrameNonZeroExitIsFailed(t *testing.T) {
	result := resultFromFrames(t, exitFrame(&sandboxv1.RunExit{ExitCode: 2, Reason: "exit"}))

	if result.Status != CommandStatusFailed {
		t.Errorf("Status = %s, want %s", result.Status, CommandStatusFailed)
	}
}

func TestTimedOutExitCarriesTheOutputCapturedSoFar(t *testing.T) {
	result := resultFromFrames(t,
		stdoutFrame("listening on :3000\n"),
		stderrFrame("warn: slow start\n"),
		exitFrame(&sandboxv1.RunExit{ExitCode: 0, TimedOut: true, Reason: "timeout"}),
	)

	if result.Status != CommandStatusTimedOut {
		t.Fatalf("Status = %s, want %s", result.Status, CommandStatusTimedOut)
	}
	if got := result.StdoutString(); got != "listening on :3000" {
		t.Errorf("Stdout = %q, want the output captured before the budget expired", got)
	}
	if got := result.StderrString(); got != "warn: slow start" {
		t.Errorf("Stderr = %q, want the output captured before the budget expired", got)
	}
}

// The first Wait drains waitCh, so an uncached second call would block forever.
func TestRunHandleWaitIsIdempotent(t *testing.T) {
	h := newFrameHandle(t, exitFrame(&sandboxv1.RunExit{ExitCode: 0, Reason: "exit"}))

	first, firstErr := h.Wait()
	if firstErr != nil {
		t.Fatalf("first Wait() error = %v, want nil", firstErr)
	}

	done := make(chan struct{})
	var second *Result
	var secondErr error
	go func() {
		defer close(done)
		second, secondErr = h.Wait()
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("second Wait blocked; the terminal outcome must be cached")
	}

	if secondErr != nil {
		t.Errorf("second Wait() error = %v, want nil", secondErr)
	}
	if first != second {
		t.Error("repeated Wait calls must return the same cached result")
	}
}

func TestRunHandleWaitReportsStreamErrors(t *testing.T) {
	h := newFrameHandle(t)
	sentinel := errors.New("stream reset")
	h.errCh <- sentinel

	result, err := h.Wait()

	if !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the underlying stream error", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want nil alongside a stream error", result)
	}
}
