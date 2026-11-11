package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const waitResumedTestSessionID = "019e84bc-6df8-765f-8507-2734f87156c7"

var waitResumedPausedAt = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

// waitResumedHandler serves GetSession from a scripted state sequence; the
// last entry repeats once the script is exhausted.
type waitResumedHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	states           []sandboxv1.SessionState
	pausedAts        []time.Time
	pauseSnapshotIDs []string
	lastResumeError  string
	getCalls         atomic.Int32
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
	if call < len(h.pausedAts) && !h.pausedAts[call].IsZero() {
		session.PausedAt = timestamppb.New(h.pausedAts[call])
	}
	if call < len(h.pauseSnapshotIDs) && h.pauseSnapshotIDs[call] != "" {
		session.PauseSnapshotId = &h.pauseSnapshotIDs[call]
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

func TestWaitResumedTreatsConcurrentPauseAsLanded(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{states: []sandboxv1.SessionState{
		sandboxv1.SessionState_SESSION_STATE_RESUMING,
		sandboxv1.SessionState_SESSION_STATE_PAUSING,
		sandboxv1.SessionState_SESSION_STATE_PAUSED,
	}}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	if err := session.WaitResumed(context.Background(), 10*time.Second); err != nil {
		t.Fatalf("WaitResumed: %v", err)
	}
	if session.State != SessionStatePausing {
		t.Fatalf("state = %s, want %s", session.State, SessionStatePausing)
	}
}

func TestWaitResumedIgnoresStalePausingReadBeforeResuming(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{
		states: []sandboxv1.SessionState{
			sandboxv1.SessionState_SESSION_STATE_PAUSING,
			sandboxv1.SessionState_SESSION_STATE_RESUMING,
			sandboxv1.SessionState_SESSION_STATE_PAUSED,
		},
		pausedAts:       []time.Time{waitResumedPausedAt, waitResumedPausedAt, waitResumedPausedAt},
		lastResumeError: "restore failed",
	}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	err := newWaitResumedSession(client).WaitResumed(context.Background(), 10*time.Second)
	if !errors.Is(err, ErrResumeFailed) {
		t.Fatalf("WaitResumed error = %v, want ErrResumeFailed", err)
	}
}

func TestWaitResumedTreatsPauseBetweenPollsAsLanded(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{
		states: []sandboxv1.SessionState{
			sandboxv1.SessionState_SESSION_STATE_RESUMING,
			sandboxv1.SessionState_SESSION_STATE_PAUSED,
		},
		pausedAts: []time.Time{waitResumedPausedAt, waitResumedPausedAt.Add(time.Hour)},
	}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	if err := session.WaitResumed(context.Background(), 10*time.Second); err != nil {
		t.Fatalf("WaitResumed: %v", err)
	}
	if session.State != SessionStatePaused {
		t.Fatalf("state = %s, want %s", session.State, SessionStatePaused)
	}
}

func TestWaitResumedReportsRevertAfterSnapshotMigrationAsFailure(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{
		states: []sandboxv1.SessionState{
			sandboxv1.SessionState_SESSION_STATE_RESUMING,
			sandboxv1.SessionState_SESSION_STATE_PAUSED,
		},
		pausedAts:        []time.Time{waitResumedPausedAt, waitResumedPausedAt},
		pauseSnapshotIDs: []string{"restored-snapshot", "migrated-snapshot"},
	}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitResumedSession(client)
	if err := session.WaitResumed(context.Background(), 10*time.Second); !errors.Is(err, ErrResumeFailed) {
		t.Fatalf("WaitResumed error = %v, want ErrResumeFailed", err)
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
		pausedAts:       []time.Time{waitResumedPausedAt, waitResumedPausedAt},
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

func TestWaitResumedNonPositiveTimeoutFailsFast(t *testing.T) {
	t.Parallel()

	for _, timeout := range []time.Duration{0, -time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			handler := &waitResumedHandler{states: []sandboxv1.SessionState{
				sandboxv1.SessionState_SESSION_STATE_RESUMING,
			}}
			server, client := newWaitSessionTestServer(t, handler)
			defer server.Close()

			err := newWaitResumedSession(client).WaitResumed(context.Background(), timeout)
			if !errors.Is(err, ErrResumeWaitTimeout) || !strings.Contains(err.Error(), "timeout waiting for session") {
				t.Fatalf("WaitResumed error = %v, want immediate timeout", err)
			}
			if calls := handler.getCalls.Load(); calls != 0 {
				t.Fatalf("GetSession calls = %d, want 0", calls)
			}
		})
	}
}
