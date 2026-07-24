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

type registryHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	deleteRegistryImageVersionFn func(*connect.Request[sandboxv1.DeleteRegistryImageVersionRequest]) (*connect.Response[sandboxv1.DeleteRegistryImageVersionResponse], error)
}

func (h *registryHandler) DeleteRegistryImageVersion(
	_ context.Context,
	req *connect.Request[sandboxv1.DeleteRegistryImageVersionRequest],
) (*connect.Response[sandboxv1.DeleteRegistryImageVersionResponse], error) {
	if h.deleteRegistryImageVersionFn != nil {
		return h.deleteRegistryImageVersionFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func newRegistryTestClient(t *testing.T, h *registryHandler) *Client {
	t.Helper()

	mux := http.NewServeMux()
	path, service := sandboxv1connect.NewSandboxServiceHandler(h)
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

func TestDeleteRegistryImageVersion(t *testing.T) {
	t.Parallel()

	const (
		imageID    = "01900000-0000-7000-8000-000000000001"
		snapshotID = "01900000-0000-7000-8000-000000000002"
	)

	handler := &registryHandler{}
	handler.deleteRegistryImageVersionFn = func(req *connect.Request[sandboxv1.DeleteRegistryImageVersionRequest]) (*connect.Response[sandboxv1.DeleteRegistryImageVersionResponse], error) {
		if req.Msg.ImageId != imageID {
			t.Fatalf("image_id = %q, want %q", req.Msg.ImageId, imageID)
		}
		if req.Msg.SnapshotId != snapshotID {
			t.Fatalf("snapshot_id = %q, want %q", req.Msg.SnapshotId, snapshotID)
		}
		return connect.NewResponse(&sandboxv1.DeleteRegistryImageVersionResponse{
			ImageId:    req.Msg.ImageId,
			SnapshotId: req.Msg.SnapshotId,
		}), nil
	}

	result, err := newRegistryTestClient(t, handler).DeleteRegistryImageVersion(
		context.Background(),
		"  "+imageID+"  ",
		"  "+snapshotID+"  ",
	)
	if err != nil {
		t.Fatalf("DeleteRegistryImageVersion: %v", err)
	}
	if result.ImageID != imageID {
		t.Fatalf("result image ID = %q, want %q", result.ImageID, imageID)
	}
	if result.SnapshotID != snapshotID {
		t.Fatalf("result snapshot ID = %q, want %q", result.SnapshotID, snapshotID)
	}
}

func TestDeleteRegistryImageVersionMapsErrors(t *testing.T) {
	tests := []struct {
		name    string
		code    connect.Code
		message string
		want    error
	}{
		{
			name:    "permission denied",
			code:    connect.CodePermissionDenied,
			message: "registry image edit denied",
			want:    ErrPermissionDenied,
		},
		{
			name:    "registry image not found",
			code:    connect.CodeNotFound,
			message: "registry image version not found",
			want:    ErrRegistryImageNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := &registryHandler{}
			handler.deleteRegistryImageVersionFn = func(*connect.Request[sandboxv1.DeleteRegistryImageVersionRequest]) (*connect.Response[sandboxv1.DeleteRegistryImageVersionResponse], error) {
				return nil, connect.NewError(tc.code, errors.New(tc.message))
			}

			_, err := newRegistryTestClient(t, handler).DeleteRegistryImageVersion(
				context.Background(),
				"01900000-0000-7000-8000-000000000001",
				"01900000-0000-7000-8000-000000000002",
			)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
