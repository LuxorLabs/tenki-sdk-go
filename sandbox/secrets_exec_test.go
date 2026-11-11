package sandbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type refreshEngineHandler struct {
	routingEngineHandler
	mu       sync.Mutex
	requests []*sandboxv1.RefreshSessionSecretsRequest
}

func (h *refreshEngineHandler) RefreshSessionSecrets(
	_ context.Context, req *connect.Request[sandboxv1.RefreshSessionSecretsRequest],
) (*connect.Response[sandboxv1.RefreshSessionSecretsResponse], error) {
	h.mu.Lock()
	h.requests = append(h.requests, req.Msg)
	h.mu.Unlock()
	return connect.NewResponse(&sandboxv1.RefreshSessionSecretsResponse{
		Changed:    true,
		WorkflowId: "sandbox-runtime-secret-refresh-session-1",
		Versions:   []*sandboxv1.RuntimeSecretVersionRef{{SecretName: "TOKEN", Version: 2, EnvironmentKey: "API_TOKEN"}},
	}), nil
}

type privilegedWriteHandler struct {
	dataPlaneRunTestHandler
	starts []*sandboxv1.WriteFileStreamStart
	data   []byte
}

func (h *privilegedWriteHandler) WriteFileStream(
	_ context.Context, stream *connect.ClientStream[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse], error) {
	for stream.Receive() {
		frame := stream.Msg().GetFrame()
		h.mu.Lock()
		if start := frame.GetStart(); start != nil {
			h.starts = append(h.starts, start)
		}
		h.data = append(h.data, frame.GetData()...)
		h.mu.Unlock()
	}
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse{
		Response: &sandboxv1.WriteFileStreamResponse{},
	}), stream.Err()
}

func newSecretsTestSession(t *testing.T, engine sandboxv1connect.SandboxServiceHandler, dataPlane sandboxv1connect.SandboxSessionDataPlaneServiceHandler) *Session {
	t.Helper()
	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(dataPlane)
	mux.Handle(path, svc)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	switch h := engine.(type) {
	case *refreshEngineHandler:
		h.dataPlaneEndpoint = server.URL
	case *routingEngineHandler:
		h.dataPlaneEndpoint = server.URL
	}
	engineURL, engineHTTPClient := newRoutingEngineTestServer(t, engine)
	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(engineURL), WithHTTPClient(engineHTTPClient))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session
}

func TestExecWithSecretsSetsTheRunFlag(t *testing.T) {
	runHandler := &dataPlaneRunTestHandler{t: t}
	session := newSecretsTestSession(t, &routingEngineHandler{sessionID: "session-1"}, runHandler)
	if _, err := session.Exec(context.Background(), "env", WithSecrets()); err != nil {
		t.Fatalf("exec with secrets: %v", err)
	}
	if _, err := session.Exec(context.Background(), "env"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if _, err := session.Command([]string{"env"}, RunOptions{WithSecrets: true}).Exec(context.Background()); err != nil {
		t.Fatalf("command with secrets: %v", err)
	}
	runHandler.mu.Lock()
	defer runHandler.mu.Unlock()
	if len(runHandler.starts) != 3 {
		t.Fatalf("expected three Run starts, got %d", len(runHandler.starts))
	}
	if !runHandler.starts[0].GetWithSecrets() || runHandler.starts[1].GetWithSecrets() || !runHandler.starts[2].GetWithSecrets() {
		t.Fatalf("with_secrets = %v, %v, %v; want true, false, true",
			runHandler.starts[0].GetWithSecrets(), runHandler.starts[1].GetWithSecrets(), runHandler.starts[2].GetWithSecrets())
	}
}

func TestWriteStreamPrivilegedSetsTheStartFlag(t *testing.T) {
	writeHandler := &privilegedWriteHandler{dataPlaneRunTestHandler: dataPlaneRunTestHandler{t: t}}
	session := newSecretsTestSession(t, &routingEngineHandler{sessionID: "session-1"}, writeHandler)
	for _, privileged := range []bool{true, false} {
		w, err := session.WriteStream(context.Background(), "/etc/app.conf", WriteStreamOptions{Mode: 0o644, Truncate: true, Privileged: privileged})
		if err != nil {
			t.Fatalf("write stream: %v", err)
		}
		if _, err := io.Copy(w, strings.NewReader("config")); err != nil {
			t.Fatalf("copy: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	if len(writeHandler.starts) != 2 || !writeHandler.starts[0].GetPrivileged() || writeHandler.starts[1].GetPrivileged() {
		t.Fatalf("unexpected write starts: %v", writeHandler.starts)
	}
	if got := writeHandler.starts[0].GetPath(); got != "/etc/app.conf" {
		t.Fatalf("path = %q", got)
	}
}

func TestRefreshSecretsReturnsVersionsWithoutValues(t *testing.T) {
	engine := &refreshEngineHandler{routingEngineHandler: routingEngineHandler{sessionID: "session-1"}}
	session := newSecretsTestSession(t, engine, &dataPlaneRunTestHandler{t: t})
	refresh, err := session.RefreshSecrets(context.Background())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !refresh.Changed || refresh.WorkflowID != "sandbox-runtime-secret-refresh-session-1" {
		t.Fatalf("unexpected refresh: %+v", refresh)
	}
	want := SecretVersion{SecretName: "TOKEN", Version: 2, EnvironmentKey: "API_TOKEN"}
	if len(refresh.Versions) != 1 || refresh.Versions[0] != want {
		t.Fatalf("versions = %+v, want [%+v]", refresh.Versions, want)
	}
	if len(engine.requests) != 1 || engine.requests[0].GetSessionId() != "session-1" {
		t.Fatalf("unexpected refresh requests: %v", engine.requests)
	}
}
