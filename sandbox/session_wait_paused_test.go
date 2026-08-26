package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

func newWaitPausedSession(client *Client) *Session {
	return newSession(client, &sandboxv1.SandboxSession{
		Id:        waitResumedTestSessionID,
		State:     sandboxv1.SessionState_SESSION_STATE_PAUSING,
		OwnerType: "SERVICE",
		OwnerId:   "self",
	})
}

func TestWaitPausedReachesPaused(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{states: []sandboxv1.SessionState{
		sandboxv1.SessionState_SESSION_STATE_PAUSING,
		sandboxv1.SessionState_SESSION_STATE_PAUSED,
	}}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	session := newWaitPausedSession(client)
	if err := session.WaitPaused(context.Background(), time.Second); err != nil {
		t.Fatalf("WaitPaused: %v", err)
	}
	if session.State != SessionStatePaused {
		t.Fatalf("state = %s, want %s", session.State, SessionStatePaused)
	}
}

func TestWaitPausedReturnsTypedFailureOnRollback(t *testing.T) {
	t.Parallel()

	handler := &waitResumedHandler{states: []sandboxv1.SessionState{
		sandboxv1.SessionState_SESSION_STATE_PAUSING,
		sandboxv1.SessionState_SESSION_STATE_RUNNING,
	}}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	err := newWaitPausedSession(client).WaitPaused(context.Background(), time.Second)
	if !errors.Is(err, ErrPauseFailed) {
		t.Fatalf("WaitPaused error = %v, want ErrPauseFailed", err)
	}
}

func TestWaitPausedNonPositiveTimeoutWaitsForContext(t *testing.T) {
	t.Parallel()

	for _, timeout := range []time.Duration{0, -time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			handler := &waitResumedHandler{states: []sandboxv1.SessionState{
				sandboxv1.SessionState_SESSION_STATE_PAUSING,
			}}
			server, client := newWaitSessionTestServer(t, handler)
			defer server.Close()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := newWaitPausedSession(client).WaitPaused(ctx, timeout)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("WaitPaused error = %v, want context.Canceled", err)
			}
		})
	}
}
