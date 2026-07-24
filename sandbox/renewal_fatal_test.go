package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// fixedErrCredentialEngine fails every CreateSessionCredential with a fixed error.
type fixedErrCredentialEngine struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	err   error
	calls atomic.Int32
}

func (h *fixedErrCredentialEngine) CreateSessionCredential(
	_ context.Context,
	_ *connect.Request[sandboxv1.CreateSessionCredentialRequest],
) (*connect.Response[sandboxv1.CreateSessionCredentialResponse], error) {
	h.calls.Add(1)
	return nil, h.err
}

func newRenewalLoopTestSession(t *testing.T, mintErr error) (*Session, *fixedErrCredentialEngine) {
	t.Helper()
	engine := &fixedErrCredentialEngine{err: mintErr}
	mux := http.NewServeMux()
	path, handler := sandboxv1connect.NewSandboxServiceHandler(engine)
	mux.Handle(path, handler)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)

	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	session := &Session{client: client, ID: "session-1"}
	// Near-future expiry so the first renewal fires within ~10ms.
	session.dataPlaneExpiresAt = time.Now().Add(20 * time.Millisecond)
	return session, engine
}

// A session terminated elsewhere must not leave its renewal goroutine spinning
// forever — the loop must exit on fatal (non-recoverable) mint errors so the
// goroutine and its data-plane transport become collectable. Regression for
// CLO-3609 (the OOM: orphaned sessions retried CreateSessionCredential forever).
func TestCredentialRenewalLoopStopsOnFatalError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{"session not found", connect.NewError(connect.CodeNotFound, errors.New("session not found"))},
		{"session terminated", connect.NewError(connect.CodeFailedPrecondition, errors.New("session terminated"))},
		{"session paused", connect.NewError(connect.CodeFailedPrecondition, errors.New("session not RUNNING (state=PAUSED)"))},
		{"unauthorized", connect.NewError(connect.CodeUnauthenticated, errors.New("token revoked"))},
		{"permission denied", connect.NewError(connect.CodePermissionDenied, errors.New("forbidden"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session, engine := newRenewalLoopTestSession(t, tc.err)
			done := make(chan struct{})
			go func() {
				session.credentialRenewalLoop(context.Background())
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("renewal loop did not stop on fatal error (calls=%d)", engine.calls.Load())
			}
		})
	}
}

// Transient / not-yet-ready mint failures must keep the loop alive.
func TestCredentialRenewalLoopRetriesTransientError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{"unavailable", connect.NewError(connect.CodeUnavailable, errors.New("edge temporarily down"))},
		{"not ready", connect.NewError(connect.CodeFailedPrecondition, errors.New("session not ready"))},
		{"creating", connect.NewError(connect.CodeFailedPrecondition, errors.New("session not RUNNING (state=CREATING)"))},
		{"resuming", connect.NewError(connect.CodeFailedPrecondition, errors.New("session not RUNNING (state=RESUMING)"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session, _ := newRenewalLoopTestSession(t, tc.err)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				session.credentialRenewalLoop(ctx)
				close(done)
			}()
			// Still running after a window — a transient error is not fatal.
			select {
			case <-done:
				cancel()
				t.Fatal("renewal loop stopped on a transient error")
			case <-time.After(400 * time.Millisecond):
			}
			// And it must unwind promptly once cancelled.
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("renewal loop did not stop on context cancel")
			}
		})
	}
}
