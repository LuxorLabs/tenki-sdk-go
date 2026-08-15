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

type snapshotHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	createSnapshotFn         func(*connect.Request[sandboxv1.CreateSnapshotRequest]) (*connect.Response[sandboxv1.CreateSnapshotResponse], error)
	listSessionSnapshotsFn   func(*connect.Request[sandboxv1.ListSessionSnapshotsRequest]) (*connect.Response[sandboxv1.ListSessionSnapshotsResponse], error)
	listDanglingSnapshotsFn  func(*connect.Request[sandboxv1.ListDanglingSnapshotsRequest]) (*connect.Response[sandboxv1.ListDanglingSnapshotsResponse], error)
	listWorkspaceSnapshotsFn func(*connect.Request[sandboxv1.ListWorkspaceSnapshotsRequest]) (*connect.Response[sandboxv1.ListWorkspaceSnapshotsResponse], error)
}

func (h *snapshotHandler) CreateSnapshot(_ context.Context, req *connect.Request[sandboxv1.CreateSnapshotRequest]) (*connect.Response[sandboxv1.CreateSnapshotResponse], error) {
	if h.createSnapshotFn != nil {
		return h.createSnapshotFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *snapshotHandler) ListSessionSnapshots(_ context.Context, req *connect.Request[sandboxv1.ListSessionSnapshotsRequest]) (*connect.Response[sandboxv1.ListSessionSnapshotsResponse], error) {
	if h.listSessionSnapshotsFn != nil {
		return h.listSessionSnapshotsFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *snapshotHandler) ListDanglingSnapshots(_ context.Context, req *connect.Request[sandboxv1.ListDanglingSnapshotsRequest]) (*connect.Response[sandboxv1.ListDanglingSnapshotsResponse], error) {
	if h.listDanglingSnapshotsFn != nil {
		return h.listDanglingSnapshotsFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *snapshotHandler) ListWorkspaceSnapshots(_ context.Context, req *connect.Request[sandboxv1.ListWorkspaceSnapshotsRequest]) (*connect.Response[sandboxv1.ListWorkspaceSnapshotsResponse], error) {
	if h.listWorkspaceSnapshotsFn != nil {
		return h.listWorkspaceSnapshotsFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func newSnapshotTestClient(t *testing.T, h *snapshotHandler) *Client {
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

func TestCreateSnapshotAsyncSetsAsyncRequest(t *testing.T) {
	t.Parallel()

	h := &snapshotHandler{}
	h.createSnapshotFn = func(req *connect.Request[sandboxv1.CreateSnapshotRequest]) (*connect.Response[sandboxv1.CreateSnapshotResponse], error) {
		if !req.Msg.GetAsync() {
			t.Fatal("CreateSnapshotAsync did not set async")
		}
		return connect.NewResponse(&sandboxv1.CreateSnapshotResponse{
			Snapshot: &sandboxv1.Snapshot{Id: "snap-001", SessionId: req.Msg.GetSessionId()},
		}), nil
	}

	client := newSnapshotTestClient(t, h)
	snapshot, err := client.CreateSnapshotAsync(context.Background(), "sess-001", "", nil)
	if err != nil {
		t.Fatalf("CreateSnapshotAsync: %v", err)
	}
	if snapshot.ID != "snap-001" || snapshot.SessionID != "sess-001" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestListSnapshots(t *testing.T) {
	t.Parallel()

	workspaceID := "ws-001"
	h := &snapshotHandler{}
	h.listWorkspaceSnapshotsFn = func(req *connect.Request[sandboxv1.ListWorkspaceSnapshotsRequest]) (*connect.Response[sandboxv1.ListWorkspaceSnapshotsResponse], error) {
		if req.Msg.GetWorkspaceId() != "" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		return connect.NewResponse(&sandboxv1.ListWorkspaceSnapshotsResponse{
			Snapshots: []*sandboxv1.Snapshot{
				{Id: "snap-001", SessionId: "sess-001", WorkspaceId: &workspaceID},
			},
		}), nil
	}

	client := newSnapshotTestClient(t, h)
	snapshots, err := client.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != "snap-001" || snapshots[0].WorkspaceID != "ws-001" {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}
}

func TestListSnapshotsSupportsExplicitWorkspaceScopeForServiceCredentials(t *testing.T) {
	t.Parallel()

	h := &snapshotHandler{}
	h.listWorkspaceSnapshotsFn = func(req *connect.Request[sandboxv1.ListWorkspaceSnapshotsRequest]) (*connect.Response[sandboxv1.ListWorkspaceSnapshotsResponse], error) {
		if req.Msg.GetWorkspaceId() != "ws-001" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		return connect.NewResponse(&sandboxv1.ListWorkspaceSnapshotsResponse{}), nil
	}

	client := newSnapshotTestClient(t, h)
	if _, err := client.ListSnapshots(context.Background(), WithWorkspaceID("ws-001")); err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
}

func TestListSessionSnapshots(t *testing.T) {
	t.Parallel()

	h := &snapshotHandler{}
	h.listSessionSnapshotsFn = func(req *connect.Request[sandboxv1.ListSessionSnapshotsRequest]) (*connect.Response[sandboxv1.ListSessionSnapshotsResponse], error) {
		if req.Msg.GetSessionId() != "sess-001" {
			t.Fatalf("unexpected session_id: %q", req.Msg.GetSessionId())
		}
		return connect.NewResponse(&sandboxv1.ListSessionSnapshotsResponse{
			Snapshots: []*sandboxv1.Snapshot{
				{Id: "snap-001", SessionId: "sess-001"},
			},
		}), nil
	}

	client := newSnapshotTestClient(t, h)
	snapshots, err := client.ListSessionSnapshots(context.Background(), "sess-001")
	if err != nil {
		t.Fatalf("ListSessionSnapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != "snap-001" || snapshots[0].SessionID != "sess-001" {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}
}

func TestListDanglingSnapshots(t *testing.T) {
	t.Parallel()

	h := &snapshotHandler{}
	h.listDanglingSnapshotsFn = func(req *connect.Request[sandboxv1.ListDanglingSnapshotsRequest]) (*connect.Response[sandboxv1.ListDanglingSnapshotsResponse], error) {
		if req.Msg.GetPageSize() != 100 {
			t.Fatalf("unexpected page_size: %d", req.Msg.GetPageSize())
		}
		return connect.NewResponse(&sandboxv1.ListDanglingSnapshotsResponse{
			Snapshots: []*sandboxv1.Snapshot{
				{Id: "snap-001", SessionId: "sess-001"},
			},
		}), nil
	}

	client := newSnapshotTestClient(t, h)
	snapshots, err := client.ListDanglingSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListDanglingSnapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != "snap-001" {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}
}
