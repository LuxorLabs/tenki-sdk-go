package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// gatedKillHandler models a guest whose process outlives the signal frame: the
// exit frame is withheld until releaseExit is closed. Holding those two events
// apart is what lets a test tell "signal sent" from "process dead".
type gatedKillHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler

	// exitWithoutSignal makes the process exit on its own, so a test can drive
	// the already-exited path without ever sending a signal.
	exitWithoutSignal bool
	signalSeen        chan struct{}
	releaseExit       chan struct{}
	releaseOnce       sync.Once

	mu      sync.Mutex
	signals []sandboxv1.RunSignal_Sig
}

func (h *gatedKillHandler) Run(
	_ context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{Pid: 4242}},
	}}); err != nil {
		return err
	}
	if h.exitWithoutSignal {
		return h.sendExit(stream, 0, "", "exit")
	}
	for {
		next, err := stream.Receive()
		if err != nil {
			return err
		}
		if sig := next.GetFrame().GetSignal(); sig != nil {
			h.mu.Lock()
			h.signals = append(h.signals, sig.GetSignal())
			h.mu.Unlock()
			break
		}
	}
	close(h.signalSeen)
	<-h.releaseExit
	// Mirror the real guest: a signal-killed process reports ExitCode -1 (not
	// the shell's 128+9) and Go's own signal name "killed", not "KILL".
	// See guestagent/run.go runExitFromWait.
	return h.sendExit(stream, -1, "killed", "signaled")
}

// release unblocks the handler. Safe to call more than once so a test can
// release explicitly and still register it as a cleanup.
func (h *gatedKillHandler) release() {
	h.releaseOnce.Do(func() { close(h.releaseExit) })
}

func (h *gatedKillHandler) sendExit(
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
	code int32,
	signal string,
	reason string,
) error {
	return stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Exit{Exit: &sandboxv1.RunExit{
			ExitCode: code,
			Signal:   signal,
			Reason:   reason,
		}},
	}})
}

func newGatedKillServer(t *testing.T, handler *gatedKillHandler) string {
	t.Helper()

	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(handler)
	mux.Handle(path, svc)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	return server.URL
}

// Kill() sends SIGKILL and returns as soon as the frame is on the wire — it
// deliberately does NOT block until the process is gone (FR-009a: Wait() is the
// only thing that resolves at the exit frame). Locking this in so a future
// "make kill wait" change has to be an explicit decision, not an accident.
func TestRunHandleKillSendsSigkillWithoutWaitingForExit(t *testing.T) {
	t.Parallel()

	handler := &gatedKillHandler{
		signalSeen:  make(chan struct{}),
		releaseExit: make(chan struct{}),
	}
	client := newStreamTestClient(t, &streamTestHandler{})
	session := newStreamTestSession(client, newGatedKillServer(t, handler))
	// Always unblock the handler, even if an assertion below fails first —
	// otherwise its goroutine stays parked on releaseExit for the rest of the
	// test binary's life. Registered after the gated server so LIFO cleanup
	// order releases the handler before that server is closed.
	t.Cleanup(handler.release)

	proc, err := session.Command([]string{"sleep", "3600"}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Stream starts a pumpStdin goroutine that blocks on the stdin pipe until it
	// is closed; without this it leaks for the life of the test binary.
	defer func() { _ = proc.Stdin.Close() }()
	if proc.PID != 4242 {
		t.Fatalf("PID = %d, want 4242", proc.PID)
	}

	if err := proc.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	select {
	case <-handler.signalSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("guest never received the signal frame")
	}
	handler.mu.Lock()
	signals := append([]sandboxv1.RunSignal_Sig(nil), handler.signals...)
	handler.mu.Unlock()
	if len(signals) != 1 || signals[0] != sandboxv1.RunSignal_SIG_KILL {
		t.Fatalf("signals = %#v, want [SIG_KILL]", signals)
	}

	// Kill() has already returned while the exit frame is still gated, so the
	// process is provably still running at this point.
	waitDone := make(chan *Result, 1)
	go func() {
		res, waitErr := proc.Wait()
		if waitErr != nil {
			t.Errorf("Wait: %v", waitErr)
			close(waitDone)
			return
		}
		waitDone <- res
	}()
	select {
	case <-waitDone:
		t.Fatal("Wait returned before the guest reported exit")
	case <-time.After(150 * time.Millisecond):
	}

	handler.release()
	select {
	case res := <-waitDone:
		if res == nil {
			t.Fatal("Wait failed")
		}
		if res.ExitCode != -1 || res.Status != CommandStatusFailed {
			t.Fatalf("exit = %d status = %s, want -1 FAILED", res.ExitCode, res.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the guest reported exit")
	}
}

// Killing an already-exited process is best-effort: Kill() usually reports the
// stream error left behind by the finished run, but nothing guarantees it.
// pumpResponses only closes the request side in a deferred call that races
// Wait() returning (run.go: `h.waitCh <- result` happens first), so a caller
// must handle either outcome — it must not treat a nil error as "still alive".
// The Python and TypeScript SDKs no-op silently here instead; each SDK's README
// documents its own behaviour.
func TestRunHandleKillAfterExitIsBestEffort(t *testing.T) {
	t.Parallel()

	handler := &gatedKillHandler{exitWithoutSignal: true}
	client := newStreamTestClient(t, &streamTestHandler{})
	session := newStreamTestSession(client, newGatedKillServer(t, handler))

	proc, err := session.Command([]string{"true"}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = proc.Stdin.Close() }() // else pumpStdin leaks, as above
	res, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}

	// Either outcome is acceptable; what matters is that it does not panic or
	// block. Asserting a non-nil error here would be asserting a race.
	if err := proc.Kill(); err != nil {
		t.Logf("Kill after exit reported the closed stream: %v", err)
	} else {
		t.Log("Kill after exit was silently dropped")
	}
}
