package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

const waitResumedTestSessionID = "019e84bc-6df8-765f-8507-2734f87156c7"

// waitResumedHandler serves GetSession from a scripted state sequence; the
// last entry repeats once the script is exhausted.
type waitResumedHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	states          []sandboxv1.SessionState
	lastResumeError string
	getCalls        atomic.Int32
}

func (h *waitResumedHandler) GetSession(context.Context, *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
	call := int(h.getCalls.Add(1)) - 1
	if call >= len(h.states) {
		call = len(h.states) - 1
	}
	state := h.states[call]
	session := &sandboxv1.SandboxSession{
		Id:        waitResumedTestSessionID,
		State:     state,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	}
	if state == sandboxv1.SessionState_SESSION_STATE_PAUSED || state == sandboxv1.SessionState_SESSION_STATE_USER_SHUTDOWN {
		session.LastResumeError = h.lastResumeError
	}
	return connect.NewResponse(&sandboxv1.GetSessionResponse{Session: session}), nil
}

func newWaitResumedSession(client *Client) *Session {
	return newSession(client, &sandboxv1.SandboxSession{
		Id:        waitResumedTestSessionID,
		State:     sandboxv1.SessionState_SESSION_STATE_RESUMING,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	})
}

func TestWaitResumedReachesRunning(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{states: []sandboxv1.SessionState{
		sandboxv1.SessionState_SESSION_STATE_RESUMING,
		sandboxv1.SessionState_SESSION_STATE_RUNNING,
	}}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	if err := session.WaitResumed(context.Background(), 10*time.Second); err != nil {
		t.Fatalf("WaitResumed: %v", err)
	}
	if session.State != SessionStateRunning {
		t.Fatalf("state = %s, want %s", session.State, SessionStateRunning)
	}
}

func TestWaitResumedReturnsTypedFailureOnRevertToPaused(t *testing.T) {
	t.Parallel()

	restoreErr := "snapshot restore failed: reidentify guest network failed after 3 attempts: " +
		"sh: 57: cannot create /etc/netplan/50-cloud-init.yaml: Structure needs cleaning"
	handler := &waitResumedHandler{
		states: []sandboxv1.SessionState{
			sandboxv1.SessionState_SESSION_STATE_RESUMING,
			sandboxv1.SessionState_SESSION_STATE_PAUSED,
		},
		lastResumeError: restoreErr,
	}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	err := session.WaitResumed(context.Background(), 10*time.Second)
	if !errors.Is(err, ErrResumeFailed) {
		t.Fatalf("WaitResumed error = %v, want ErrResumeFailed", err)
	}
	if !strings.Contains(err.Error(), restoreErr) {
		t.Fatalf("error = %q, want last_resume_error included", err)
	}
	if session.State != SessionStatePaused {
		t.Fatalf("state = %s, want %s", session.State, SessionStatePaused)
	}
}

func TestWaitResumedToleratesStalePausedReadBeforeResuming(t *testing.T) {
	t.Parallel()

	// First read still shows the stale pre-resume PAUSED row (replica lag);
	// WaitResumed must keep polling instead of reporting a resume failure.
	handler := &waitResumedHandler{
		states: []sandboxv1.SessionState{
			sandboxv1.SessionState_SESSION_STATE_PAUSED,
			sandboxv1.SessionState_SESSION_STATE_RESUMING,
			sandboxv1.SessionState_SESSION_STATE_RUNNING,
		},
		lastResumeError: "error from a previous, unrelated resume attempt",
	}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	if err := session.WaitResumed(context.Background(), 10*time.Second); err != nil {
		t.Fatalf("WaitResumed: %v", err)
	}
	if session.State != SessionStateRunning {
		t.Fatalf("state = %s, want %s", session.State, SessionStateRunning)
	}
}

func TestWaitResumedFailsAfterGraceWithoutObservingResuming(t *testing.T) {
	t.Parallel()

	// The resume reverted before the first poll: RESUMING is never observed,
	// but a stopped state persisting past the grace window is a real failure.
	handler := &waitResumedHandler{
		states:          []sandboxv1.SessionState{sandboxv1.SessionState_SESSION_STATE_PAUSED},
		lastResumeError: "resume dispatch failed: host unreachable",
	}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	err := session.WaitResumed(context.Background(), 10*time.Second)
	if !errors.Is(err, ErrResumeFailed) {
		t.Fatalf("WaitResumed error = %v, want ErrResumeFailed", err)
	}
	if !strings.Contains(err.Error(), "host unreachable") {
		t.Fatalf("error = %q, want last_resume_error included", err)
	}
}

func TestWaitResumedSurfacesTerminalState(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{states: []sandboxv1.SessionState{
		sandboxv1.SessionState_SESSION_STATE_TERMINATED,
	}}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	err := session.WaitResumed(context.Background(), 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "terminal state") {
		t.Fatalf("WaitResumed error = %v, want terminal state error", err)
	}
}
