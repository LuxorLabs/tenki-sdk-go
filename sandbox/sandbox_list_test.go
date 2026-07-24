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
)

type sandboxListHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	listWorkspaceSandboxesFn func(*connect.Request[sandboxv1.ListWorkspaceSandboxesRequest]) (*connect.Response[sandboxv1.ListWorkspaceSandboxesResponse], error)
}

func (h *sandboxListHandler) ListWorkspaceSandboxes(_ context.Context, req *connect.Request[sandboxv1.ListWorkspaceSandboxesRequest]) (*connect.Response[sandboxv1.ListWorkspaceSandboxesResponse], error) {
	if h.listWorkspaceSandboxesFn != nil {
		return h.listWorkspaceSandboxesFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func newSandboxListTestClient(t *testing.T, h *sandboxListHandler) *Client {
	t.Helper()

	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(h)
	mux.Handle(path, svc)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestList(t *testing.T) {
	t.Parallel()

	h := &sandboxListHandler{}
	h.listWorkspaceSandboxesFn = func(req *connect.Request[sandboxv1.ListWorkspaceSandboxesRequest]) (*connect.Response[sandboxv1.ListWorkspaceSandboxesResponse], error) {
		if req.Msg.GetWorkspaceId() != "" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		if req.Msg.GetPageToken() == "next" {
			return connect.NewResponse(&sandboxv1.ListWorkspaceSandboxesResponse{
				Sessions: []*sandboxv1.SandboxSession{{Id: "sess-002", WorkspaceId: "ws-001"}},
			}), nil
		}
		return connect.NewResponse(&sandboxv1.ListWorkspaceSandboxesResponse{
			Sessions: []*sandboxv1.SandboxSession{
				{Id: "sess-001", WorkspaceId: "ws-001"},
			},
			NextPageToken: "next",
		}), nil
	}

	client := newSandboxListTestClient(t, h)
	sessions, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 2 || sessions[0].ID != "sess-001" || sessions[1].ID != "sess-002" {
		t.Fatalf("unexpected sessions: %#v", sessions)
	}
}

func TestListSupportsExplicitWorkspaceScopeForServiceCredentials(t *testing.T) {
	t.Parallel()

	h := &sandboxListHandler{}
	h.listWorkspaceSandboxesFn = func(req *connect.Request[sandboxv1.ListWorkspaceSandboxesRequest]) (*connect.Response[sandboxv1.ListWorkspaceSandboxesResponse], error) {
		if req.Msg.GetWorkspaceId() != "ws-001" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		return connect.NewResponse(&sandboxv1.ListWorkspaceSandboxesResponse{}), nil
	}

	client := newSandboxListTestClient(t, h)
	if _, err := client.List(context.Background(), WithWorkspaceID("ws-001")); err != nil {
		t.Fatalf("List: %v", err)
	}
}
