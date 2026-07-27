package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type routingEngineHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	sessionID         string
	dataPlaneEndpoint string
}

func (h *routingEngineHandler) CreateSession(context.Context, *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{
		Session: &sandboxv1.SandboxSession{
			Id:        h.sessionID,
			State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
			OwnerType: "SERVICE",
			OwnerId:   "self",
		},
		DataPlaneEndpoint: h.dataPlaneEndpoint,
		Credential: &sandboxv1.SessionCredential{
			Credential: "test-session-cert",
			ExpiresAt:  timestamppb.New(time.Now().Add(time.Hour)),
		},
	}), nil
}

func (h *routingEngineHandler) ExecuteCommand(context.Context, *connect.Request[sandboxv1.ExecuteCommandRequest]) (*connect.Response[sandboxv1.ExecuteCommandResponse], error) {
	return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("deprecated engine exec path must not be used"))
}

func (h *routingEngineHandler) StreamCommandOutput(context.Context, *connect.Request[sandboxv1.StreamCommandOutputRequest], *connect.ServerStream[sandboxv1.StreamCommandOutputResponse]) error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("deprecated engine stream path must not be used"))
}

func TestSDKExecUsesDataPlaneRun(t *testing.T) {
	t.Parallel()

	runHandler, dataPlaneEndpoint := newDataPlaneRunTestServer(t)
	engineURL, engineHTTPClient := newRoutingEngineTestServer(t, &routingEngineHandler{
		sessionID:         "session-1",
		dataPlaneEndpoint: dataPlaneEndpoint,
	})
	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(engineURL), WithHTTPClient(engineHTTPClient))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	result, err := session.Exec(context.Background(), "echo", WithArgs("sc004"))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if result.ExitCode != 0 || string(result.Stdout) != "out" || string(result.Stderr) != "err" {
		t.Fatalf("unexpected data-plane result: %#v", result)
	}
	if len(runHandler.starts) != 1 {
		t.Fatalf("expected one data-plane Run start, got %d", len(runHandler.starts))
	}
	if got := runHandler.starts[0].GetCmd(); len(got) != 2 || got[0] != "echo" || got[1] != "sc004" {
		t.Fatalf("unexpected data-plane command: %#v", got)
	}
}

func TestSDKDataPlaneWarmUsesProbeHeader(t *testing.T) {
	t.Parallel()

	runHandler, dataPlaneEndpoint := newDataPlaneRunTestServer(t)
	engineURL, engineHTTPClient := newRoutingEngineTestServer(t, &routingEngineHandler{
		sessionID:         "session-1",
		dataPlaneEndpoint: dataPlaneEndpoint,
	})
	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(engineURL), WithHTTPClient(engineHTTPClient))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.Create(context.Background(), WithWorkspaceID("ws-1")); err != nil {
		t.Fatalf("create session: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runHandler.mu.Lock()
		found := slices.Contains(runHandler.statProbeHeaders, dataPlaneProbeWarmup)
		runHandler.mu.Unlock()
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("data-plane warmup did not carry the probe header")
}

func newRoutingEngineTestServer(t *testing.T, handler sandboxv1connect.SandboxServiceHandler) (string, *http.Client) {
	t.Helper()

	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(handler)
	mux.Handle(path, svc)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server.URL, server.Client()
}
