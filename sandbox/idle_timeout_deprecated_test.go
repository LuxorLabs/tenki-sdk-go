package sandbox

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

type idleTimeoutTestHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	createRequest *sandboxv1.CreateSessionRequest
}

func (h *idleTimeoutTestHandler) CreateSession(_ context.Context, req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	h.createRequest = req.Msg
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{
		Session: &sandboxv1.SandboxSession{Id: "session-1"},
	}), nil
}

func TestWithIdleTimeoutIsNotSentOnTheWire(t *testing.T) {
	handler := &idleTimeoutTestHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()

	_, err := client.Create(context.Background(), WithIdleTimeout(15*time.Minute), WithWaitReady(false))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if handler.createRequest == nil {
		t.Fatal("CreateSession was not called")
	}
	if handler.createRequest.IdleTimeoutMinutes != nil {
		t.Fatalf("idle_timeout_minutes got %d, want unset", handler.createRequest.GetIdleTimeoutMinutes())
	}
}

func TestWithIdleTimeoutWarnsOnce(t *testing.T) {
	idleTimeoutDeprecationOnce = sync.Once{}
	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = writer

	WithIdleTimeout(15 * time.Minute)
	WithIdleTimeout(30 * time.Minute)

	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	os.Stderr = original
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}

	if got := strings.Count(string(out), "WithIdleTimeout is deprecated"); got != 1 {
		t.Fatalf("deprecation notices got %d, want 1: %q", got, string(out))
	}
}
