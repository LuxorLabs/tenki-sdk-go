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

type previewURLHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	create func(*connect.Request[sandboxv1.CreatePreviewUrlRequest]) (*connect.Response[sandboxv1.CreatePreviewUrlResponse], error)
	list   func(*connect.Request[sandboxv1.ListPreviewUrlsRequest]) (*connect.Response[sandboxv1.ListPreviewUrlsResponse], error)
}

func (h *previewURLHandler) CreatePreviewUrl(_ context.Context, req *connect.Request[sandboxv1.CreatePreviewUrlRequest]) (*connect.Response[sandboxv1.CreatePreviewUrlResponse], error) {
	if h.create == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
	}
	return h.create(req)
}

func (h *previewURLHandler) ListPreviewUrls(_ context.Context, req *connect.Request[sandboxv1.ListPreviewUrlsRequest]) (*connect.Response[sandboxv1.ListPreviewUrlsResponse], error) {
	if h.list == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
	}
	return h.list(req)
}

func newPreviewURLTestClient(t *testing.T, handler *previewURLHandler) *Client {
	t.Helper()
	mux := http.NewServeMux()
	path, service := sandboxv1connect.NewSandboxServiceHandler(handler)
	mux.Handle(path, service)
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

func TestPreviewURLRequestsInferWorkspaceAndOmitProject(t *testing.T) {
	t.Parallel()
	const workspaceID = "01900000-0000-7000-8000-0000000000aa"
	handler := &previewURLHandler{}
	handler.create = func(req *connect.Request[sandboxv1.CreatePreviewUrlRequest]) (*connect.Response[sandboxv1.CreatePreviewUrlResponse], error) {
		if req.Msg.GetWorkspaceId() != "" || req.Msg.GetProjectId() != "" {
			t.Fatalf("unexpected scope: workspace=%q project=%q", req.Msg.GetWorkspaceId(), req.Msg.GetProjectId())
		}
		return connect.NewResponse(&sandboxv1.CreatePreviewUrlResponse{PreviewUrl: &sandboxv1.PreviewUrl{Id: "preview-1", WorkspaceId: workspaceID}}), nil
	}
	handler.list = func(req *connect.Request[sandboxv1.ListPreviewUrlsRequest]) (*connect.Response[sandboxv1.ListPreviewUrlsResponse], error) {
		if req.Msg.GetWorkspaceId() != "" || req.Msg.GetProjectId() != "" {
			t.Fatalf("unexpected scope: workspace=%q project=%q", req.Msg.GetWorkspaceId(), req.Msg.GetProjectId())
		}
		if req.Msg.GetPageToken() == "next" {
			return connect.NewResponse(&sandboxv1.ListPreviewUrlsResponse{
				PreviewUrls: []*sandboxv1.PreviewUrl{{Id: "preview-2", WorkspaceId: workspaceID}},
			}), nil
		}
		return connect.NewResponse(&sandboxv1.ListPreviewUrlsResponse{
			PreviewUrls:   []*sandboxv1.PreviewUrl{{Id: "preview-1", WorkspaceId: workspaceID}},
			NextPageToken: "next",
		}), nil
	}
	client := newPreviewURLTestClient(t, handler)
	if _, err := client.CreatePreviewURL(context.Background(), "demo", nil, nil); err != nil {
		t.Fatalf("create workspace preview URL: %v", err)
	}
	items, err := client.ListPreviewURLs(context.Background())
	if err != nil {
		t.Fatalf("list workspace preview URLs: %v", err)
	}
	if len(items) != 2 || items[0].ID != "preview-1" || items[1].ID != "preview-2" {
		t.Fatalf("unexpected preview URLs: %#v", items)
	}
}
