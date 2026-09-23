package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type cleanupRunHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	reason   string
	canceled chan struct{}
}

func (h *cleanupRunHandler) Run(ctx context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	frames := []*sandboxv1.RunResponse{
		{Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{Pid: 42}}},
		exitFrame(&sandboxv1.RunExit{Reason: h.reason}),
	}
	for _, frame := range frames {
		if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: frame}); err != nil {
			return err
		}
	}
	<-ctx.Done()
	close(h.canceled)
	return ctx.Err()
}

func TestRunExitCancelsTheRPC(t *testing.T) {
	for _, reason := range []string{"exit", "capability_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			handler := &cleanupRunHandler{reason: reason, canceled: make(chan struct{})}
			mux := http.NewServeMux()
			path, service := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(handler)
			mux.Handle(path, service)
			server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := newStreamTestClient(t, &streamTestHandler{})
			session := newStreamTestSession(client, server.URL)
			_, err := session.Command([]string{"true"}).Exec(ctx)
			if (reason == "exit") != (err == nil) {
				t.Fatalf("Exec error = %v for %s", err, reason)
			}
			select {
			case <-handler.canceled:
			case <-time.After(time.Second):
				t.Fatal("completed command retained its RPC")
			}
		})
	}
}
