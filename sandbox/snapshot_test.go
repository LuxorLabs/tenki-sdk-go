package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	getSnapshotFn            func(*connect.Request[sandboxv1.GetSnapshotRequest]) (*connect.Response[sandboxv1.GetSnapshotResponse], error)
}

func (h *snapshotHandler) GetSnapshot(_ context.Context, req *connect.Request[sandboxv1.GetSnapshotRequest]) (*connect.Response[sandboxv1.GetSnapshotResponse], error) {
	if h.getSnapshotFn != nil {
		return h.getSnapshotFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
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

func TestCreateSnapshotAndWaitSubmitsAsync(t *testing.T) {
	t.Parallel()

	h := &snapshotHandler{}
	h.createSnapshotFn = func(req *connect.Request[sandboxv1.CreateSnapshotRequest]) (*connect.Response[sandboxv1.CreateSnapshotResponse], error) {
		if !req.Msg.GetAsync() {
			t.Error("CreateSnapshotAndWait did not set async")
		}
		return connect.NewResponse(&sandboxv1.CreateSnapshotResponse{
			Snapshot: &sandboxv1.Snapshot{Id: "snap-001", SessionId: req.Msg.GetSessionId(), State: sandboxv1.SnapshotState_SNAPSHOT_STATE_CREATING},
		}), nil
	}
	h.getSnapshotFn = func(req *connect.Request[sandboxv1.GetSnapshotRequest]) (*connect.Response[sandboxv1.GetSnapshotResponse], error) {
		return connect.NewResponse(&sandboxv1.GetSnapshotResponse{
			Snapshot: &sandboxv1.Snapshot{Id: req.Msg.GetSnapshotId(), SessionId: "sess-001", State: sandboxv1.SnapshotState_SNAPSHOT_STATE_READY},
		}), nil
	}

	client := newSnapshotTestClient(t, h)
	snapshot, err := client.CreateSnapshotAndWait(context.Background(), "sess-001", "", nil, time.Minute)
	if err != nil {
		t.Fatalf("CreateSnapshotAndWait: %v", err)
	}
	if snapshot.ID != "snap-001" || !snapshot.State.IsReady() {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestWaitSnapshotReadyReportsFailureReason(t *testing.T) {
	t.Parallel()

	h := &snapshotHandler{}
	h.getSnapshotFn = func(req *connect.Request[sandboxv1.GetSnapshotRequest]) (*connect.Response[sandboxv1.GetSnapshotResponse], error) {
		return connect.NewResponse(&sandboxv1.GetSnapshotResponse{
			Snapshot: &sandboxv1.Snapshot{Id: req.Msg.GetSnapshotId(), State: sandboxv1.SnapshotState_SNAPSHOT_STATE_FAILED, FailureReason: "rootfs capture failed"},
		}), nil
	}

	client := newSnapshotTestClient(t, h)
	_, err := client.WaitSnapshotReady(context.Background(), "snap-001", time.Minute)
	if !errors.Is(err, ErrSnapshotFailed) {
		t.Fatalf("WaitSnapshotReady error = %v, want ErrSnapshotFailed", err)
	}
	if !strings.Contains(err.Error(), "rootfs capture failed") {
		t.Fatalf("WaitSnapshotReady error = %v, want failure reason", err)
	}
}

func TestWaitSnapshotReadyTimeoutExplainsHowToKeepWaiting(t *testing.T) {
	t.Parallel()

	h := &snapshotHandler{}
	h.getSnapshotFn = func(req *connect.Request[sandboxv1.GetSnapshotRequest]) (*connect.Response[sandboxv1.GetSnapshotResponse], error) {
		return connect.NewResponse(&sandboxv1.GetSnapshotResponse{
			Snapshot: &sandboxv1.Snapshot{Id: req.Msg.GetSnapshotId(), State: sandboxv1.SnapshotState_SNAPSHOT_STATE_CREATING},
		}), nil
	}

	client := newSnapshotTestClient(t, h)
	_, err := client.WaitSnapshotReady(context.Background(), "snap-001", 10*time.Millisecond)
	if !errors.Is(err, ErrSnapshotWaitTimeout) {
		t.Fatalf("WaitSnapshotReady error = %v, want ErrSnapshotWaitTimeout", err)
	}
	if !strings.Contains(err.Error(), "snap-001") || !strings.Contains(err.Error(), "WaitSnapshotReady") {
		t.Fatalf("WaitSnapshotReady error = %v, want a hint to keep waiting on snap-001", err)
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
