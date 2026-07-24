package sandbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

// VolumeState mirrors persistent volume lifecycle states from the service contract.
type VolumeState string

const (
	VolumeStateUnspecified VolumeState = "UNSPECIFIED"
	VolumeStateAvailable   VolumeState = "AVAILABLE"
	VolumeStateDeleting    VolumeState = "DELETING"
	VolumeStateDeleted     VolumeState = "DELETED"
	VolumeStateInUse       VolumeState = "IN_USE"
)

const (
	Byte int64 = 1

	KB int64 = 1000 * Byte
	MB int64 = 1000 * KB
	GB int64 = 1000 * MB
	TB int64 = 1000 * GB

	KiB int64 = 1024 * Byte
	MiB int64 = 1024 * KiB
	GiB int64 = 1024 * MiB
	TiB int64 = 1024 * GiB
)

// VolumeAttachment is an active attachment of a volume to a session.
type VolumeAttachment struct {
	ID        string
	VolumeID  string
	SessionID string
	MountPath string
	ReadOnly  bool
	State     string
}

// Volume is the SDK view of a persistent sandbox volume.
type Volume struct {
	ID                string
	WorkspaceID       string
	Name              string
	SizeBytes         int64
	State             VolumeState
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Tags              []string
	ActiveAttachments []VolumeAttachment
}

// IsDeletable returns true when the volume has no active attachments and can be deleted without error.
func (v *Volume) IsDeletable() bool {
	return v.State == VolumeStateAvailable && len(v.ActiveAttachments) == 0
}

// CreateVolume creates a workspace-scoped persistent volume.
func (c *Client) CreateVolume(ctx context.Context, opts ...CreateVolumeOption) (*Volume, error) {
	cfg := defaultCreateVolumeConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyCreateVolume(&cfg)
	}
	req := &sandboxv1.CreateVolumeRequest{
		Name:      cfg.name,
		SizeBytes: cfg.sizeBytes,
	}
	if workspaceID := strings.TrimSpace(cfg.workspaceID); workspaceID != "" {
		req.WorkspaceId = workspaceID
	}

	resp, err := c.sandbox.CreateVolume(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return volumeFromProto(resp.Msg.Volume), nil
}

// GetVolume fetches a single persistent volume by ID.
func (c *Client) GetVolume(ctx context.Context, volumeID string) (*Volume, error) {
	resp, err := c.sandbox.GetVolume(ctx, connect.NewRequest(&sandboxv1.GetVolumeRequest{
		VolumeId: volumeID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	vol := volumeFromProto(resp.Msg.Volume)
	if vol != nil {
		vol.ActiveAttachments = volumeAttachmentsFromProto(resp.Msg.ActiveAttachments)
	}
	return vol, nil
}

// ListVolumes lists persistent volumes owned by the Workspace API key.
func (c *Client) ListVolumes(ctx context.Context, opts ...ListOption) ([]*Volume, error) {
	cfg := defaultListConfig()
	for _, opt := range opts {
		if opt != nil {
			opt.applyList(&cfg)
		}
	}
	var volumes []*Volume
	var pageToken string
	for {
		resp, err := c.sandbox.ListVolumes(ctx, connect.NewRequest(&sandboxv1.ListVolumesRequest{
			WorkspaceId: cfg.workspaceID,
			PageSize:    automaticPageSize,
			PageToken:   pageToken,
		}))
		if err != nil {
			return nil, mapError(err)
		}
		volumes = append(volumes, volumesFromProto(resp.Msg.Volumes)...)
		pageToken, err = advancePageToken(pageToken, resp.Msg.NextPageToken)
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			return volumes, nil
		}
	}
}

// DeleteVolume soft-deletes one persistent volume.
// Retries up to 60 s when the volume has a sync-pending attachment (node-agent still uploading).
func (c *Client) DeleteVolume(ctx context.Context, volumeID string) error {
	const maxElapsed = 60 * time.Second
	backoff := time.Second
	deadline := time.Now().Add(maxElapsed)

	for {
		_, err := c.sandbox.DeleteVolume(ctx, connect.NewRequest(&sandboxv1.DeleteVolumeRequest{
			VolumeId: volumeID,
		}))
		mapped := mapError(err)
		if !errors.Is(mapped, ErrVolumeSyncPending) || time.Now().After(deadline) {
			return mapped
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

// ResizeVolume updates one persistent volume's size.
func (c *Client) ResizeVolume(ctx context.Context, volumeID string, newSizeBytes int64) (*Volume, error) {
	resp, err := c.sandbox.ResizeVolume(ctx, connect.NewRequest(&sandboxv1.ResizeVolumeRequest{
		VolumeId:     volumeID,
		NewSizeBytes: newSizeBytes,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return volumeFromProto(resp.Msg.Volume), nil
}

// UpdateVolume applies mutable volume fields.
func (c *Client) UpdateVolume(ctx context.Context, volumeID string, opts ...UpdateVolumeOption) (*Volume, error) {
	cfg := updateVolumeConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyUpdateVolume(&cfg)
	}

	req := &sandboxv1.UpdateVolumeRequest{VolumeId: strings.TrimSpace(volumeID)}
	if cfg.name != nil {
		req.Name = cfg.name
	}
	if cfg.tagsSet {
		if len(cfg.tags) == 0 {
			req.ClearTags = true
		} else {
			req.Tags = append([]string(nil), cfg.tags...)
		}
	}

	resp, err := c.sandbox.UpdateVolume(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return volumeFromProto(resp.Msg.Volume), nil
}

func volumeFromProto(protoVolume *sandboxv1.Volume) *Volume {
	if protoVolume == nil {
		return nil
	}

	return &Volume{
		ID:          protoVolume.Id,
		WorkspaceID: protoVolume.WorkspaceId,
		Name:        protoVolume.Name,
		SizeBytes:   protoVolume.SizeBytes,
		State:       volumeStateFromProto(protoVolume.State),
		CreatedAt:   parseVolumeTime(protoVolume.CreatedAt),
		UpdatedAt:   parseVolumeTime(protoVolume.UpdatedAt),
		Tags:        append([]string(nil), protoVolume.Tags...),
	}
}

func volumeAttachmentFromProto(a *sandboxv1.VolumeAttachment) VolumeAttachment {
	if a == nil {
		return VolumeAttachment{}
	}
	return VolumeAttachment{
		ID:        a.Id,
		VolumeID:  a.VolumeId,
		SessionID: a.SessionId,
		MountPath: a.MountPath,
		ReadOnly:  a.Readonly,
		State:     a.State,
	}
}

func volumeAttachmentsFromProto(as []*sandboxv1.VolumeAttachment) []VolumeAttachment {
	result := make([]VolumeAttachment, 0, len(as))
	for _, a := range as {
		if a != nil {
			result = append(result, volumeAttachmentFromProto(a))
		}
	}
	return result
}

func volumesFromProto(protoVolumes []*sandboxv1.Volume) []*Volume {
	volumes := make([]*Volume, 0, len(protoVolumes))
	for _, protoVolume := range protoVolumes {
		volumes = append(volumes, volumeFromProto(protoVolume))
	}
	return volumes
}

// IsReady returns true if the volume is available for use.
func (s VolumeState) IsReady() bool {
	return s == VolumeStateAvailable
}

// IsTerminal returns true if the volume has reached a final state.
func (s VolumeState) IsTerminal() bool {
	return s == VolumeStateDeleted
}

func volumeStateFromProto(state sandboxv1.VolumeState) VolumeState {
	switch state {
	case sandboxv1.VolumeState_VOLUME_STATE_AVAILABLE:
		return VolumeStateAvailable
	case sandboxv1.VolumeState_VOLUME_STATE_DELETING:
		return VolumeStateDeleting
	case sandboxv1.VolumeState_VOLUME_STATE_DELETED:
		return VolumeStateDeleted
	case sandboxv1.VolumeState_VOLUME_STATE_IN_USE:
		return VolumeStateInUse
	default:
		return VolumeStateUnspecified
	}
}

func parseVolumeTime(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
