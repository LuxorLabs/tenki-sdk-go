package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

// volumeHandler implements mock handlers for volume RPCs.
type volumeHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	t *testing.T

	createVolumeFn func(*connect.Request[sandboxv1.CreateVolumeRequest]) (*connect.Response[sandboxv1.CreateVolumeResponse], error)
	getSessionFn   func(*connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error)
	getVolumeFn    func(*connect.Request[sandboxv1.GetVolumeRequest]) (*connect.Response[sandboxv1.GetVolumeResponse], error)
	listVolumesFn  func(*connect.Request[sandboxv1.ListVolumesRequest]) (*connect.Response[sandboxv1.ListVolumesResponse], error)
	deleteVolumeFn func(*connect.Request[sandboxv1.DeleteVolumeRequest]) (*connect.Response[sandboxv1.DeleteVolumeResponse], error)
	resizeVolumeFn func(*connect.Request[sandboxv1.ResizeVolumeRequest]) (*connect.Response[sandboxv1.ResizeVolumeResponse], error)
	attachVolumeFn func(*connect.Request[sandboxv1.AttachVolumeRequest]) (*connect.Response[sandboxv1.AttachVolumeResponse], error)
	detachVolumeFn func(*connect.Request[sandboxv1.DetachVolumeRequest]) (*connect.Response[sandboxv1.DetachVolumeResponse], error)
}

func (h *volumeHandler) GetSession(_ context.Context, req *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
	if h.getSessionFn != nil {
		return h.getSessionFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *volumeHandler) CreateVolume(_ context.Context, req *connect.Request[sandboxv1.CreateVolumeRequest]) (*connect.Response[sandboxv1.CreateVolumeResponse], error) {
	if h.createVolumeFn != nil {
		return h.createVolumeFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *volumeHandler) GetVolume(_ context.Context, req *connect.Request[sandboxv1.GetVolumeRequest]) (*connect.Response[sandboxv1.GetVolumeResponse], error) {
	if h.getVolumeFn != nil {
		return h.getVolumeFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *volumeHandler) ListVolumes(_ context.Context, req *connect.Request[sandboxv1.ListVolumesRequest]) (*connect.Response[sandboxv1.ListVolumesResponse], error) {
	if h.listVolumesFn != nil {
		return h.listVolumesFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *volumeHandler) DeleteVolume(_ context.Context, req *connect.Request[sandboxv1.DeleteVolumeRequest]) (*connect.Response[sandboxv1.DeleteVolumeResponse], error) {
	if h.deleteVolumeFn != nil {
		return h.deleteVolumeFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *volumeHandler) ResizeVolume(_ context.Context, req *connect.Request[sandboxv1.ResizeVolumeRequest]) (*connect.Response[sandboxv1.ResizeVolumeResponse], error) {
	if h.resizeVolumeFn != nil {
		return h.resizeVolumeFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *volumeHandler) AttachVolume(_ context.Context, req *connect.Request[sandboxv1.AttachVolumeRequest]) (*connect.Response[sandboxv1.AttachVolumeResponse], error) {
	if h.attachVolumeFn != nil {
		return h.attachVolumeFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *volumeHandler) DetachVolume(_ context.Context, req *connect.Request[sandboxv1.DetachVolumeRequest]) (*connect.Response[sandboxv1.DetachVolumeResponse], error) {
	if h.detachVolumeFn != nil {
		return h.detachVolumeFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

// newVolumeTestServer creates an httptest TLS server with the given handler and returns the server + client.
func newVolumeTestServer(t *testing.T, h *volumeHandler) (*httptest.Server, *Client) {
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

	return server, client
}

func TestCreateVolume(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.createVolumeFn = func(req *connect.Request[sandboxv1.CreateVolumeRequest]) (*connect.Response[sandboxv1.CreateVolumeResponse], error) {
		if req.Msg.GetWorkspaceId() != "" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		if req.Msg.GetName() != "my-vol" {
			t.Fatalf("unexpected name: %q", req.Msg.GetName())
		}
		if req.Msg.GetSizeBytes() != 10*GB {
			t.Fatalf("unexpected size_bytes: %d", req.Msg.GetSizeBytes())
		}
		return connect.NewResponse(&sandboxv1.CreateVolumeResponse{
			Volume: &sandboxv1.Volume{
				Id:          "vol-001",
				WorkspaceId: "ws-001",
				Name:        "my-vol",
				SizeBytes:   10 * GB,
				State:       sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE,
				CreatedAt:   "2026-03-09T00:00:00Z",
				UpdatedAt:   "2026-03-09T00:00:00Z",
			},
		}), nil
	}

	_, client := newVolumeTestServer(t, h)
	vol, err := client.CreateVolume(context.Background(),
		WithVolumeName("my-vol"),
		WithVolumeSize(10*GB),
	)
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if vol.ID != "vol-001" {
		t.Fatalf("unexpected id: %q", vol.ID)
	}
	if vol.WorkspaceID != "ws-001" {
		t.Fatalf("unexpected workspace_id: %q", vol.WorkspaceID)
	}
	if vol.Name != "my-vol" {
		t.Fatalf("unexpected name: %q", vol.Name)
	}
	if vol.SizeBytes != 10*GB {
		t.Fatalf("unexpected size_bytes: %d", vol.SizeBytes)
	}
	if vol.State != VolumeStateAvailable {
		t.Fatalf("unexpected state: %q", vol.State)
	}
	if vol.CreatedAt.IsZero() {
		t.Fatal("expected non-zero created_at")
	}
}

func TestGetVolume(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.getVolumeFn = func(req *connect.Request[sandboxv1.GetVolumeRequest]) (*connect.Response[sandboxv1.GetVolumeResponse], error) {
			if req.Msg.GetVolumeId() != "vol-001" {
				t.Fatalf("unexpected volume_id: %q", req.Msg.GetVolumeId())
			}
			return connect.NewResponse(&sandboxv1.GetVolumeResponse{
				Volume: &sandboxv1.Volume{
					Id:          "vol-001",
					WorkspaceId: "ws-001",
					Name:        "data",
					SizeBytes:   5 * GiB,
					State:       sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE,
					CreatedAt:   "2026-03-09T10:00:00Z",
					UpdatedAt:   "2026-03-09T10:00:00Z",
				},
			}), nil
		}

		_, client := newVolumeTestServer(t, h)
		vol, err := client.GetVolume(context.Background(), "vol-001")
		if err != nil {
			t.Fatalf("GetVolume: %v", err)
		}
		if vol.ID != "vol-001" {
			t.Fatalf("unexpected id: %q", vol.ID)
		}
		if vol.Name != "data" {
			t.Fatalf("unexpected name: %q", vol.Name)
		}
	})

	t.Run("in use", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.getVolumeFn = func(_ *connect.Request[sandboxv1.GetVolumeRequest]) (*connect.Response[sandboxv1.GetVolumeResponse], error) {
			return connect.NewResponse(&sandboxv1.GetVolumeResponse{
				Volume: &sandboxv1.Volume{
					Id: "vol-shared", State: sandboxv1.VolumeState_VOLUME_STATE_IN_USE, Tags: []string{"shared"},
				},
				ActiveAttachments: []*sandboxv1.VolumeAttachment{
					{SessionId: "sess-1", MountPath: "/one", Readonly: true, State: "ATTACHED"},
					{SessionId: "sess-2", MountPath: "/two", Readonly: true, State: "ATTACHED"},
				},
			}), nil
		}

		_, client := newVolumeTestServer(t, h)
		vol, err := client.GetVolume(context.Background(), "vol-shared")
		if err != nil {
			t.Fatalf("GetVolume: %v", err)
		}
		if vol.State != VolumeStateInUse {
			t.Fatalf("expected IN_USE, got %q", vol.State)
		}
		if len(vol.ActiveAttachments) != 2 || !vol.ActiveAttachments[0].ReadOnly || !vol.ActiveAttachments[1].ReadOnly {
			t.Fatalf("expected two read-only attachments: %#v", vol.ActiveAttachments)
		}
		if len(vol.Tags) != 1 || vol.Tags[0] != "shared" {
			t.Fatalf("unexpected tags: %#v", vol.Tags)
		}
		if vol.IsDeletable() {
			t.Fatal("IN_USE volume should not be deletable")
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.getVolumeFn = func(_ *connect.Request[sandboxv1.GetVolumeRequest]) (*connect.Response[sandboxv1.GetVolumeResponse], error) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("volume not found"))
		}

		_, client := newVolumeTestServer(t, h)
		_, err := client.GetVolume(context.Background(), "vol-missing")
		if !errors.Is(err, ErrVolumeNotFound) {
			t.Fatalf("expected ErrVolumeNotFound, got %v", err)
		}
	})
}

func TestListVolumes(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.listVolumesFn = func(req *connect.Request[sandboxv1.ListVolumesRequest]) (*connect.Response[sandboxv1.ListVolumesResponse], error) {
		if req.Msg.GetWorkspaceId() != "" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		return connect.NewResponse(&sandboxv1.ListVolumesResponse{
			Volumes: []*sandboxv1.Volume{
				{Id: "vol-001", WorkspaceId: "ws-001", Name: "alpha", SizeBytes: 1 * GiB, State: sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE},
				{Id: "vol-002", WorkspaceId: "ws-001", Name: "beta", SizeBytes: 2 * GiB, State: sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE},
				{Id: "vol-003", WorkspaceId: "ws-001", Name: "gamma", SizeBytes: 5 * GiB, State: sandboxv1.VolumeState_VOLUME_STATE_DELETING},
			},
		}), nil
	}

	_, client := newVolumeTestServer(t, h)
	vols, err := client.ListVolumes(context.Background())
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(vols) != 3 {
		t.Fatalf("expected 3 volumes, got %d", len(vols))
	}
	if vols[0].Name != "alpha" || vols[1].Name != "beta" || vols[2].Name != "gamma" {
		t.Fatalf("unexpected volume names: %q %q %q", vols[0].Name, vols[1].Name, vols[2].Name)
	}
	if vols[2].State != VolumeStateDeleting {
		t.Fatalf("expected DELETING state for third volume, got %q", vols[2].State)
	}
}

func TestDeleteVolume(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.deleteVolumeFn = func(req *connect.Request[sandboxv1.DeleteVolumeRequest]) (*connect.Response[sandboxv1.DeleteVolumeResponse], error) {
			if req.Msg.GetVolumeId() != "vol-001" {
				t.Fatalf("unexpected volume_id: %q", req.Msg.GetVolumeId())
			}
			return connect.NewResponse(&sandboxv1.DeleteVolumeResponse{}), nil
		}

		_, client := newVolumeTestServer(t, h)
		err := client.DeleteVolume(context.Background(), "vol-001")
		if err != nil {
			t.Fatalf("DeleteVolume: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.deleteVolumeFn = func(_ *connect.Request[sandboxv1.DeleteVolumeRequest]) (*connect.Response[sandboxv1.DeleteVolumeResponse], error) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("volume not found"))
		}

		_, client := newVolumeTestServer(t, h)
		err := client.DeleteVolume(context.Background(), "vol-missing")
		if !errors.Is(err, ErrVolumeNotFound) {
			t.Fatalf("expected ErrVolumeNotFound, got %v", err)
		}
	})
}

func TestResizeVolume(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.resizeVolumeFn = func(req *connect.Request[sandboxv1.ResizeVolumeRequest]) (*connect.Response[sandboxv1.ResizeVolumeResponse], error) {
		if req.Msg.GetVolumeId() != "vol-001" {
			t.Fatalf("unexpected volume_id: %q", req.Msg.GetVolumeId())
		}
		if req.Msg.GetNewSizeBytes() != 20*GiB {
			t.Fatalf("unexpected new_size_bytes: %d", req.Msg.GetNewSizeBytes())
		}
		return connect.NewResponse(&sandboxv1.ResizeVolumeResponse{
			Volume: &sandboxv1.Volume{
				Id:          "vol-001",
				WorkspaceId: "ws-001",
				Name:        "data",
				SizeBytes:   20 * GiB,
				State:       sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE,
				CreatedAt:   "2026-03-09T00:00:00Z",
				UpdatedAt:   "2026-03-09T12:00:00Z",
			},
		}), nil
	}

	_, client := newVolumeTestServer(t, h)
	vol, err := client.ResizeVolume(context.Background(), "vol-001", 20*GiB)
	if err != nil {
		t.Fatalf("ResizeVolume: %v", err)
	}
	if vol.SizeBytes != 20*GiB {
		t.Fatalf("unexpected size_bytes: %d", vol.SizeBytes)
	}
}

func TestAttachVolume(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.attachVolumeFn = func(req *connect.Request[sandboxv1.AttachVolumeRequest]) (*connect.Response[sandboxv1.AttachVolumeResponse], error) {
			if req.Msg.GetSessionId() != "session-1" {
				t.Fatalf("unexpected session_id: %q", req.Msg.GetSessionId())
			}
			vol := req.Msg.GetVolume()
			if vol.GetVolumeId() != "vol-001" {
				t.Fatalf("unexpected volume_id: %q", vol.GetVolumeId())
			}
			if vol.GetMountPath() != "/data" {
				t.Fatalf("unexpected mount_path: %q", vol.GetMountPath())
			}
			if vol.GetReadonly() {
				t.Fatal("expected readonly=false")
			}
			return connect.NewResponse(&sandboxv1.AttachVolumeResponse{
				Attachment: &sandboxv1.VolumeAttachment{
					Id:        "att-001",
					VolumeId:  "vol-001",
					SessionId: "session-1",
					MountPath: "/data",
					Readonly:  false,
				},
			}), nil
		}

		_, client := newVolumeTestServer(t, h)
		session := &Session{client: client, ID: "session-1"}

		err := session.AttachVolume(context.Background(), "vol-001", "/data")
		if err != nil {
			t.Fatalf("AttachVolume: %v", err)
		}
		if len(session.VolumeMounts) != 1 {
			t.Fatalf("expected 1 volume mount, got %d", len(session.VolumeMounts))
		}
		if session.VolumeMounts[0].VolumeID != "vol-001" {
			t.Fatalf("unexpected volume_id in mount: %q", session.VolumeMounts[0].VolumeID)
		}
		if session.VolumeMounts[0].MountPath != "/data" {
			t.Fatalf("unexpected mount_path: %q", session.VolumeMounts[0].MountPath)
		}
	})

	t.Run("readonly", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.attachVolumeFn = func(req *connect.Request[sandboxv1.AttachVolumeRequest]) (*connect.Response[sandboxv1.AttachVolumeResponse], error) {
			vol := req.Msg.GetVolume()
			if !vol.GetReadonly() {
				t.Fatal("expected readonly=true")
			}
			return connect.NewResponse(&sandboxv1.AttachVolumeResponse{
				Attachment: &sandboxv1.VolumeAttachment{
					Id:        "att-002",
					VolumeId:  "vol-001",
					SessionId: "session-1",
					MountPath: "/ro-data",
					Readonly:  true,
				},
			}), nil
		}

		_, client := newVolumeTestServer(t, h)
		session := &Session{client: client, ID: "session-1"}

		err := session.AttachVolume(context.Background(), "vol-001", "/ro-data", WithReadOnly())
		if err != nil {
			t.Fatalf("AttachVolume readonly: %v", err)
		}
		if len(session.VolumeMounts) != 1 {
			t.Fatalf("expected 1 volume mount, got %d", len(session.VolumeMounts))
		}
		if !session.VolumeMounts[0].ReadOnly {
			t.Fatal("expected ReadOnly=true in mount")
		}
	})
}

func TestDetachVolume(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.detachVolumeFn = func(req *connect.Request[sandboxv1.DetachVolumeRequest]) (*connect.Response[sandboxv1.DetachVolumeResponse], error) {
		if req.Msg.GetSessionId() != "session-1" {
			t.Fatalf("unexpected session_id: %q", req.Msg.GetSessionId())
		}
		if req.Msg.GetVolumeId() != "vol-001" {
			t.Fatalf("unexpected volume_id: %q", req.Msg.GetVolumeId())
		}
		return connect.NewResponse(&sandboxv1.DetachVolumeResponse{}), nil
	}
	h.getSessionFn = func(req *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
		if req.Msg.GetSessionId() != "session-1" {
			t.Fatalf("unexpected get session_id: %q", req.Msg.GetSessionId())
		}
		return connect.NewResponse(&sandboxv1.GetSessionResponse{
			Session: &sandboxv1.SandboxSession{
				Id: "session-1",
				VolumeAttachments: []*sandboxv1.VolumeAttachment{
					{VolumeId: "vol-001", MountPath: "/data", State: "DETACHED"},
					{VolumeId: "vol-002", MountPath: "/other", State: "ATTACHED"},
				},
			},
		}), nil
	}

	_, client := newVolumeTestServer(t, h)
	session := &Session{
		client: client,
		ID:     "session-1",
		VolumeMounts: []VolumeMount{
			{VolumeID: "vol-001", MountPath: "/data"},
			{VolumeID: "vol-002", MountPath: "/other"},
		},
	}

	err := session.DetachVolume(context.Background(), "vol-001")
	if err != nil {
		t.Fatalf("DetachVolume: %v", err)
	}
	if len(session.VolumeMounts) != 1 {
		t.Fatalf("expected 1 remaining mount, got %d", len(session.VolumeMounts))
	}
	if session.VolumeMounts[0].VolumeID != "vol-002" {
		t.Fatalf("wrong volume remained: %q", session.VolumeMounts[0].VolumeID)
	}
}

func TestDetachVolumeForce(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.detachVolumeFn = func(req *connect.Request[sandboxv1.DetachVolumeRequest]) (*connect.Response[sandboxv1.DetachVolumeResponse], error) {
		if !req.Msg.GetForceDetach() {
			t.Fatal("expected force_detach=true")
		}
		return connect.NewResponse(&sandboxv1.DetachVolumeResponse{}), nil
	}
	h.getSessionFn = func(req *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
		return connect.NewResponse(&sandboxv1.GetSessionResponse{
			Session: &sandboxv1.SandboxSession{
				Id: "session-1",
				VolumeAttachments: []*sandboxv1.VolumeAttachment{
					{VolumeId: "vol-001", State: "DETACHED"},
				},
			},
		}), nil
	}

	_, client := newVolumeTestServer(t, h)
	session := &Session{client: client, ID: "session-1"}

	if err := session.DetachVolume(context.Background(), "vol-001", WithForceDetach()); err != nil {
		t.Fatalf("DetachVolume force: %v", err)
	}
}

func TestDetachVolumeWaitsPastSyncPending(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.detachVolumeFn = func(req *connect.Request[sandboxv1.DetachVolumeRequest]) (*connect.Response[sandboxv1.DetachVolumeResponse], error) {
		return connect.NewResponse(&sandboxv1.DetachVolumeResponse{}), nil
	}
	var polls int
	h.getSessionFn = func(req *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
		polls++
		state := "SYNC_PENDING"
		if polls > 1 {
			state = "DETACHED"
		}
		return connect.NewResponse(&sandboxv1.GetSessionResponse{
			Session: &sandboxv1.SandboxSession{
				Id: "session-1",
				VolumeAttachments: []*sandboxv1.VolumeAttachment{
					{VolumeId: "vol-001", State: state},
				},
			},
		}), nil
	}

	_, client := newVolumeTestServer(t, h)
	session := &Session{
		client: client,
		ID:     "session-1",
		VolumeMounts: []VolumeMount{
			{VolumeID: "vol-001", MountPath: "/data"},
		},
	}

	if err := session.DetachVolume(context.Background(), "vol-001"); err != nil {
		t.Fatalf("DetachVolume sync_pending: %v", err)
	}
	if polls < 2 {
		t.Fatalf("expected detach wait to poll past sync_pending")
	}
}

func TestDetachVolumeNoWait(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.detachVolumeFn = func(req *connect.Request[sandboxv1.DetachVolumeRequest]) (*connect.Response[sandboxv1.DetachVolumeResponse], error) {
		return connect.NewResponse(&sandboxv1.DetachVolumeResponse{}), nil
	}
	h.getSessionFn = func(req *connect.Request[sandboxv1.GetSessionRequest]) (*connect.Response[sandboxv1.GetSessionResponse], error) {
		t.Fatal("GetSession should not be called when wait timeout is disabled")
		return nil, nil
	}

	_, client := newVolumeTestServer(t, h)
	session := &Session{client: client, ID: "session-1"}

	if err := session.DetachVolume(context.Background(), "vol-001", WithDetachWaitTimeout(0)); err != nil {
		t.Fatalf("DetachVolume no wait: %v", err)
	}
}

func TestVolumeErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "volume not found",
			err:  connect.NewError(connect.CodeNotFound, errors.New("volume not found")),
			want: ErrVolumeNotFound,
		},
		{
			name: "volume limit exceeded",
			err:  connect.NewError(connect.CodeResourceExhausted, errors.New("volume limit exceeded")),
			want: ErrVolumeLimitExceeded,
		},
		{
			name: "volume is attached",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("volume is attached to session")),
			want: ErrVolumeInUse,
		},
		{
			name: "volume sync pending (old message)",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("volume reuse blocked by SYNC_PENDING")),
			want: ErrVolumeSyncPending,
		},
		{
			name: "volume sync pending (new message)",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("volume sync pending")),
			want: ErrVolumeSyncPending,
		},
		{
			name: "volume is in use",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("volume is in use")),
			want: ErrVolumeInUse,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mapped := mapError(tc.err)
			if !errors.Is(mapped, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, mapped)
			}
		})
	}
}

func TestParseVolumeTime(t *testing.T) {
	t.Parallel()

	ts := parseVolumeTime("2026-03-09T12:30:45.123456789Z")
	expected := time.Date(2026, 3, 9, 12, 30, 45, 123456789, time.UTC)
	if !ts.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, ts)
	}

	if !parseVolumeTime("").IsZero() {
		t.Fatal("expected zero time for empty string")
	}
	if !parseVolumeTime("not-a-date").IsZero() {
		t.Fatal("expected zero time for invalid string")
	}
}

func TestGetVolumeActiveAttachments(t *testing.T) {
	t.Parallel()

	t.Run("populated when sync_pending", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.getVolumeFn = func(_ *connect.Request[sandboxv1.GetVolumeRequest]) (*connect.Response[sandboxv1.GetVolumeResponse], error) {
			return connect.NewResponse(&sandboxv1.GetVolumeResponse{
				Volume: &sandboxv1.Volume{
					Id:    "vol-001",
					State: sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE,
				},
				ActiveAttachments: []*sandboxv1.VolumeAttachment{
					{Id: "att-001", VolumeId: "vol-001", SessionId: "sess-001", MountPath: "/data", State: "SYNC_PENDING"},
				},
			}), nil
		}

		_, client := newVolumeTestServer(t, h)
		vol, err := client.GetVolume(context.Background(), "vol-001")
		if err != nil {
			t.Fatalf("GetVolume: %v", err)
		}
		if len(vol.ActiveAttachments) != 1 {
			t.Fatalf("expected 1 active attachment, got %d", len(vol.ActiveAttachments))
		}
		if vol.ActiveAttachments[0].State != "SYNC_PENDING" {
			t.Fatalf("expected SYNC_PENDING, got %q", vol.ActiveAttachments[0].State)
		}
		if vol.ActiveAttachments[0].SessionID != "sess-001" {
			t.Fatalf("unexpected session_id: %q", vol.ActiveAttachments[0].SessionID)
		}
		if vol.IsDeletable() {
			t.Fatal("volume with active attachments should not be deletable")
		}
	})

	t.Run("empty when no attachments", func(t *testing.T) {
		t.Parallel()
		h := &volumeHandler{t: t}
		h.getVolumeFn = func(_ *connect.Request[sandboxv1.GetVolumeRequest]) (*connect.Response[sandboxv1.GetVolumeResponse], error) {
			return connect.NewResponse(&sandboxv1.GetVolumeResponse{
				Volume: &sandboxv1.Volume{
					Id:    "vol-002",
					State: sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE,
				},
			}), nil
		}

		_, client := newVolumeTestServer(t, h)
		vol, err := client.GetVolume(context.Background(), "vol-002")
		if err != nil {
			t.Fatalf("GetVolume: %v", err)
		}
		if len(vol.ActiveAttachments) != 0 {
			t.Fatalf("expected 0 active attachments, got %d", len(vol.ActiveAttachments))
		}
		if !vol.IsDeletable() {
			t.Fatal("volume with no active attachments should be deletable")
		}
	})
}

func TestDeleteVolumeRetriesOnSyncPending(t *testing.T) {
	t.Parallel()

	var calls int
	h := &volumeHandler{t: t}
	h.deleteVolumeFn = func(_ *connect.Request[sandboxv1.DeleteVolumeRequest]) (*connect.Response[sandboxv1.DeleteVolumeResponse], error) {
		calls++
		if calls < 3 {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("volume sync pending"))
		}
		return connect.NewResponse(&sandboxv1.DeleteVolumeResponse{}), nil
	}

	_, client := newVolumeTestServer(t, h)
	err := client.DeleteVolume(context.Background(), "vol-001")
	if err != nil {
		t.Fatalf("DeleteVolume expected success after retries, got: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (2 sync_pending + 1 success), got %d", calls)
	}
}

func TestDeleteVolumeSyncPendingContextCancelled(t *testing.T) {
	t.Parallel()

	h := &volumeHandler{t: t}
	h.deleteVolumeFn = func(_ *connect.Request[sandboxv1.DeleteVolumeRequest]) (*connect.Response[sandboxv1.DeleteVolumeResponse], error) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("volume sync pending"))
	}

	_, client := newVolumeTestServer(t, h)

	// Context expires during the retry sleep — should propagate the context error.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.DeleteVolume(ctx, "vol-001")
	if err == nil {
		t.Fatal("expected an error after context cancelled, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
}
