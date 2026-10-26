package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const forkTestSessionID = "11111111-1111-4111-8111-111111111111"

type forkSessionHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	request *sandboxv1.ForkSessionRequest
	resp    *sandboxv1.ForkSessionResponse
	err     error
}

func (h *forkSessionHandler) ForkSession(
	_ context.Context, req *connect.Request[sandboxv1.ForkSessionRequest],
) (*connect.Response[sandboxv1.ForkSessionResponse], error) {
	h.request = req.Msg
	if h.err != nil {
		return nil, h.err
	}
	return connect.NewResponse(h.resp), nil
}

func newForkTestSession(t *testing.T, handler *forkSessionHandler) *Session {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(sandboxv1connect.NewSandboxServiceHandler(handler))
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return newSession(client, &sandboxv1.SandboxSession{Id: forkTestSessionID, State: sandboxv1.SessionState_SESSION_STATE_RUNNING})
}

func forkChildProto(id, name string) *sandboxv1.SandboxSession {
	return &sandboxv1.SandboxSession{Id: id, Name: name, State: sandboxv1.SessionState_SESSION_STATE_CREATING}
}

func TestSessionForkSendsOptionsAndReturnsChildren(t *testing.T) {
	t.Parallel()
	handler := &forkSessionHandler{resp: &sandboxv1.ForkSessionResponse{
		SnapshotId: "snap-1",
		Sessions:   []*sandboxv1.SandboxSession{forkChildProto("child-1", "eval-1"), forkChildProto("child-2", "eval-2")},
	}}
	session := newForkTestSession(t, handler)

	children, err := session.Fork(context.Background(), ForkOptions{Count: 2, NamePrefix: "eval", Wait: true})

	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	got := handler.request
	if got.GetSessionId() != forkTestSessionID || got.GetCount() != 2 || got.GetNamePrefix() != "eval" || !got.GetWait() {
		t.Fatalf("request = %+v, want session %s, count 2, prefix eval, wait", got, forkTestSessionID)
	}
	if len(children) != 2 || children[0].ID != "child-1" || children[1].Name != "eval-2" {
		t.Fatalf("children = %+v, want child-1 and eval-2", children)
	}
	if children[0].State != SessionStateCreating {
		t.Fatalf("child state = %s, want %s", children[0].State, SessionStateCreating)
	}
}

func TestSessionForkZeroOptionsLeaveDefaultsToServer(t *testing.T) {
	t.Parallel()
	handler := &forkSessionHandler{resp: &sandboxv1.ForkSessionResponse{SnapshotId: "snap-1"}}
	session := newForkTestSession(t, handler)

	if _, err := session.Fork(context.Background(), ForkOptions{}); err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if handler.request.GetCount() != 0 || handler.request.GetNamePrefix() != "" || handler.request.GetWait() {
		t.Fatalf("request = %+v, want zero count, empty prefix, no wait", handler.request)
	}
}

func TestSessionForkPartialFailureCarriesCreatedChildren(t *testing.T) {
	t.Parallel()
	connectErr := connect.NewError(connect.CodeResourceExhausted, errors.New("workspace concurrent session quota exceeded"))
	detail, err := connect.NewErrorDetail(&sandboxv1.ForkSessionResponse{
		SnapshotId: "snap-9",
		Sessions:   []*sandboxv1.SandboxSession{forkChildProto("child-1", "web-fork-1")},
	})
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	connectErr.AddDetail(detail)
	session := newForkTestSession(t, &forkSessionHandler{err: connectErr})

	_, err = session.Fork(context.Background(), ForkOptions{Count: 3})

	var forkErr *ForkFailedError
	if !errors.As(err, &forkErr) {
		t.Fatalf("Fork error = %v, want *ForkFailedError", err)
	}
	if forkErr.SnapshotID != "snap-9" || len(forkErr.Sessions) != 1 || forkErr.Sessions[0].ID != "child-1" {
		t.Fatalf("fork error = %+v, want snap-9 with child-1", forkErr)
	}
	if !errors.Is(err, ErrForkFailed) || !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("Fork error = %v, want ErrForkFailed wrapping ErrQuotaExceeded", err)
	}
}

func TestSessionForkRejectionWithoutSnapshotMapsNormally(t *testing.T) {
	t.Parallel()
	session := newForkTestSession(t, &forkSessionHandler{
		err: connect.NewError(connect.CodeNotFound, errors.New("session not found")),
	})

	_, err := session.Fork(context.Background(), ForkOptions{})

	if !errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrForkFailed) {
		t.Fatalf("Fork error = %v, want ErrSessionNotFound only", err)
	}
}
