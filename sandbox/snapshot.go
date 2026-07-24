package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SnapshotState mirrors snapshot lifecycle states from the service contract.
type SnapshotState string

type SnapshotType string

// SnapshotDurabilityState mirrors snapshot R2-upload durability from the service contract.
type SnapshotDurabilityState string

const (
	SnapshotStateUnspecified SnapshotState = "UNSPECIFIED"
	SnapshotStateCreating    SnapshotState = "CREATING"
	SnapshotStateReady       SnapshotState = "READY"
	SnapshotStateFailed      SnapshotState = "FAILED"
	SnapshotStateDeleting    SnapshotState = "DELETING"
	SnapshotStateDeleted     SnapshotState = "DELETED"

	SnapshotTypeUnspecified SnapshotType = "UNSPECIFIED"
	SnapshotTypeUser        SnapshotType = "USER"
	SnapshotTypePause       SnapshotType = "PAUSE"
	SnapshotTypeTemplate    SnapshotType = "TEMPLATE"

	SnapshotDurabilityStateUnspecified       SnapshotDurabilityState = "UNSPECIFIED"
	SnapshotDurabilityStateLocalReady        SnapshotDurabilityState = "LOCAL_READY"
	SnapshotDurabilityStateDurable           SnapshotDurabilityState = "DURABLE"
	SnapshotDurabilityStatePropagationFailed SnapshotDurabilityState = "PROPAGATION_FAILED"
	// DurableCeph means the snapshot is cluster-durable in Ceph (cross-node
	// usable on capability-matched hosts) but not yet uploaded to R2.
	SnapshotDurabilityStateDurableCeph SnapshotDurabilityState = "DURABLE_CEPH"
)

// Snapshot is the SDK view of a sandbox snapshot.
type Snapshot struct {
	ID              string
	SessionID       string
	WorkspaceID     string
	Name            string
	State           SnapshotState
	SizeBytes       int64
	CompressedBytes int64
	MemoryBytes     int64
	BaseImageID     string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	Tags            []string
	Type            SnapshotType
	CPUCores        int32
	MemoryMB        int32
	DiskSizeGB      int32
	// FailureReason is populated when State is FAILED; empty otherwise.
	FailureReason string
	// DurabilityState reports whether the snapshot bytes have been uploaded to R2.
	// LOCAL_READY means the snapshot exists on its origin host's disk but is not yet durable.
	// DURABLE means R2 holds a copy and the snapshot can restore on any host.
	DurabilityState SnapshotDurabilityState
	// PropagationError is the last durability-propagation error (e.g. a failed
	// R2 upload). It may be set without demoting DurabilityState for a
	// cluster-durable (DURABLE_CEPH) snapshot, so callers can distinguish a
	// stalled R2 upload from a snapshot that is still progressing.
	PropagationError string
	// RawImageAvailable is true when the snapshot has a standalone compressed
	// rootfs disk image artifact.
	RawImageAvailable bool
}

// SnapshotDownloadURL is a short-lived object-store URL for one snapshot file.
type SnapshotDownloadURL struct {
	URL       string
	ExpiresAt time.Time
}

// CreateSnapshot creates a snapshot for one session.
func (c *Client) CreateSnapshot(ctx context.Context, sessionID, name string, expiresAt *time.Time) (*Snapshot, error) {
	return c.createSnapshot(ctx, sessionID, name, expiresAt, false)
}

// CreateSnapshotAsync creates a snapshot and returns as soon as the snapshot is accepted.
func (c *Client) CreateSnapshotAsync(ctx context.Context, sessionID, name string, expiresAt *time.Time) (*Snapshot, error) {
	return c.createSnapshot(ctx, sessionID, name, expiresAt, true)
}

func (c *Client) createSnapshot(ctx context.Context, sessionID, name string, expiresAt *time.Time, async bool) (*Snapshot, error) {
	req := &sandboxv1.CreateSnapshotRequest{
		SessionId: sessionID,
		Async:     async,
	}
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		req.Name = &trimmed
	}
	if expiresAt != nil {
		req.ExpiresAt = timestamppb.New(*expiresAt)
	}

	resp, err := c.sandbox.CreateSnapshot(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return snapshotFromProto(resp.Msg.Snapshot), nil
}

// GetSnapshot fetches one snapshot by ID.
func (c *Client) GetSnapshot(ctx context.Context, snapshotID string) (*Snapshot, error) {
	resp, err := c.sandbox.GetSnapshot(ctx, connect.NewRequest(&sandboxv1.GetSnapshotRequest{
		SnapshotId: strings.TrimSpace(snapshotID),
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return snapshotFromProto(resp.Msg.Snapshot), nil
}

// GetSnapshotDownloadURL returns a short-lived download URL for a template snapshot's raw image.
func (c *Client) GetSnapshotDownloadURL(ctx context.Context, snapshotID string) (*SnapshotDownloadURL, error) {
	resp, err := c.sandbox.GetSnapshotDownloadURL(ctx, connect.NewRequest(&sandboxv1.GetSnapshotDownloadURLRequest{
		SnapshotId: strings.TrimSpace(snapshotID),
	}))
	if err != nil {
		return nil, mapError(err)
	}
	out := &SnapshotDownloadURL{
		URL: resp.Msg.GetUrl(),
	}
	if resp.Msg.GetExpiresAt() != nil {
		out.ExpiresAt = resp.Msg.GetExpiresAt().AsTime()
	}
	return out, nil
}

var snapshotDurablePollInterval = 2 * time.Second

// WaitForSnapshotDurable polls until the snapshot's R2 upload is complete.
// Returns the snapshot when DurabilityState == DURABLE, ErrSnapshotFailed when the snapshot
// itself failed, or ErrSnapshotNotDurable when durability ended at PROPAGATION_FAILED.
func (c *Client) WaitForSnapshotDurable(ctx context.Context, snapshotID string) (*Snapshot, error) {
	for {
		snapshot, err := c.GetSnapshot(ctx, snapshotID)
		if err != nil {
			return nil, err
		}
		switch snapshot.State {
		case SnapshotStateFailed:
			if snapshot.FailureReason != "" {
				return snapshot, fmt.Errorf("%w: %s", ErrSnapshotFailed, snapshot.FailureReason)
			}
			return snapshot, ErrSnapshotFailed
		case SnapshotStateDeleted, SnapshotStateDeleting:
			return snapshot, ErrSnapshotNotFound
		}
		switch snapshot.DurabilityState {
		case SnapshotDurabilityStateDurable:
			return snapshot, nil
		case SnapshotDurabilityStateDurableCeph:
			// Cluster-durable in Ceph (cross-node usable) but not yet R2-durable.
			// Keep polling for full R2 durability, unless the R2 upload has
			// stalled (propagation_error set) — it will not complete, so don't
			// spin until the caller's deadline.
			if snapshot.PropagationError != "" {
				return snapshot, fmt.Errorf("%w: cluster-durable in Ceph but R2 upload failed: %s", ErrSnapshotNotDurable, snapshot.PropagationError)
			}
		case SnapshotDurabilityStatePropagationFailed:
			return snapshot, ErrSnapshotNotDurable
		case SnapshotDurabilityStateUnspecified:
			// READY snapshot with no durability tracking predates the feature
			// (or the engine never recorded a durability_state). Treat as
			// non-durable rather than spinning until the caller's deadline.
			if snapshot.State == SnapshotStateReady {
				return snapshot, fmt.Errorf("%w: durability state unspecified; snapshot may predate durability tracking", ErrSnapshotNotDurable)
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(snapshotDurablePollInterval):
		}
	}
}

// ListSnapshots lists snapshots owned by the Workspace API key.
func (c *Client) ListSnapshots(ctx context.Context, opts ...ListOption) ([]*Snapshot, error) {
	cfg := defaultListConfig()
	for _, opt := range opts {
		if opt != nil {
			opt.applyList(&cfg)
		}
	}
	var snapshots []*Snapshot
	var pageToken string
	for {
		resp, err := c.sandbox.ListWorkspaceSnapshots(ctx, connect.NewRequest(&sandboxv1.ListWorkspaceSnapshotsRequest{
			WorkspaceId: cfg.workspaceID,
			PageSize:    automaticPageSize,
			PageToken:   pageToken,
		}))
		if err != nil {
			return nil, mapError(err)
		}
		snapshots = append(snapshots, snapshotsFromProto(resp.Msg.Snapshots)...)
		pageToken, err = advancePageToken(pageToken, resp.Msg.NextPageToken)
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			return snapshots, nil
		}
	}
}

// ListSessionSnapshots lists snapshots created from one session.
func (c *Client) ListSessionSnapshots(ctx context.Context, sessionID string) ([]*Snapshot, error) {
	var snapshots []*Snapshot
	var pageToken string
	for {
		resp, err := c.sandbox.ListSessionSnapshots(ctx, connect.NewRequest(&sandboxv1.ListSessionSnapshotsRequest{
			SessionId: strings.TrimSpace(sessionID),
			PageSize:  automaticPageSize,
			PageToken: pageToken,
		}))
		if err != nil {
			return nil, mapError(err)
		}
		snapshots = append(snapshots, snapshotsFromProto(resp.Msg.Snapshots)...)
		pageToken, err = advancePageToken(pageToken, resp.Msg.NextPageToken)
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			return snapshots, nil
		}
	}
}

// ListDanglingSnapshots lists snapshots whose source session is gone or terminating.
func (c *Client) ListDanglingSnapshots(ctx context.Context) ([]*Snapshot, error) {
	var snapshots []*Snapshot
	var pageToken string
	for {
		resp, err := c.sandbox.ListDanglingSnapshots(ctx, connect.NewRequest(&sandboxv1.ListDanglingSnapshotsRequest{
			PageSize:  automaticPageSize,
			PageToken: pageToken,
		}))
		if err != nil {
			return nil, mapError(err)
		}
		snapshots = append(snapshots, snapshotsFromProto(resp.Msg.Snapshots)...)
		pageToken, err = advancePageToken(pageToken, resp.Msg.NextPageToken)
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			return snapshots, nil
		}
	}
}

// DeleteSnapshot deletes one snapshot.
func (c *Client) DeleteSnapshot(ctx context.Context, snapshotID string) (*Snapshot, error) {
	resp, err := c.sandbox.DeleteSnapshot(ctx, connect.NewRequest(&sandboxv1.DeleteSnapshotRequest{
		SnapshotId: snapshotID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return snapshotFromProto(resp.Msg.Snapshot), nil
}

// UpdateSnapshot applies mutable snapshot fields.
func (c *Client) UpdateSnapshot(ctx context.Context, snapshotID string, opts ...UpdateSnapshotOption) (*Snapshot, error) {
	cfg := updateSnapshotConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyUpdateSnapshot(&cfg)
	}

	req := &sandboxv1.UpdateSnapshotRequest{SnapshotId: strings.TrimSpace(snapshotID)}
	if cfg.name != nil {
		req.Name = cfg.name
	}
	if cfg.expiresAtSet && cfg.expiresAt == nil {
		req.ClearExpiresAt = true
	} else if cfg.expiresAtSet && cfg.expiresAt != nil {
		req.ExpiresAt = timestamppb.New(*cfg.expiresAt)
	}
	if cfg.tagsSet {
		if len(cfg.tags) == 0 {
			req.ClearTags = true
		} else {
			req.Tags = append([]string(nil), cfg.tags...)
		}
	}

	resp, err := c.sandbox.UpdateSnapshot(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return snapshotFromProto(resp.Msg.Snapshot), nil
}

func snapshotFromProto(protoSnapshot *sandboxv1.Snapshot) *Snapshot {
	if protoSnapshot == nil {
		return nil
	}

	snapshot := &Snapshot{
		ID:                protoSnapshot.Id,
		SessionID:         protoSnapshot.SessionId,
		State:             snapshotStateFromProto(protoSnapshot.State),
		SizeBytes:         protoSnapshot.SizeBytes,
		CompressedBytes:   protoSnapshot.CompressedBytes,
		MemoryBytes:       protoSnapshot.MemoryBytes,
		BaseImageID:       protoSnapshot.BaseImageId,
		Tags:              append([]string(nil), protoSnapshot.Tags...),
		Type:              snapshotTypeFromProto(protoSnapshot.Type),
		CPUCores:          protoSnapshot.CpuCores,
		MemoryMB:          protoSnapshot.MemoryMb,
		DiskSizeGB:        protoSnapshot.DiskSizeGb,
		FailureReason:     protoSnapshot.GetFailureReason(),
		DurabilityState:   snapshotDurabilityStateFromProto(protoSnapshot.GetDurabilityState()),
		PropagationError:  protoSnapshot.GetPropagationError(),
		RawImageAvailable: protoSnapshot.GetRawImageAvailable(),
	}
	if protoSnapshot.WorkspaceId != nil {
		snapshot.WorkspaceID = *protoSnapshot.WorkspaceId
	}
	if protoSnapshot.Name != nil {
		snapshot.Name = *protoSnapshot.Name
	}
	if protoSnapshot.CreatedAt != nil {
		snapshot.CreatedAt = protoSnapshot.CreatedAt.AsTime()
	}
	if protoSnapshot.ExpiresAt != nil {
		snapshot.ExpiresAt = protoSnapshot.ExpiresAt.AsTime()
	}
	return snapshot
}

func snapshotsFromProto(protoSnapshots []*sandboxv1.Snapshot) []*Snapshot {
	snapshots := make([]*Snapshot, 0, len(protoSnapshots))
	for _, protoSnapshot := range protoSnapshots {
		snapshots = append(snapshots, snapshotFromProto(protoSnapshot))
	}
	return snapshots
}

// IsReady returns true if the snapshot is usable for restore.
func (s SnapshotState) IsReady() bool {
	return s == SnapshotStateReady
}

// IsTerminal returns true if the snapshot has reached a final state.
func (s SnapshotState) IsTerminal() bool {
	return s == SnapshotStateFailed || s == SnapshotStateDeleted
}

func snapshotStateFromProto(state sandboxv1.SnapshotState) SnapshotState {
	switch state {
	case sandboxv1.SnapshotState_SNAPSHOT_STATE_CREATING:
		return SnapshotStateCreating
	case sandboxv1.SnapshotState_SNAPSHOT_STATE_READY:
		return SnapshotStateReady
	case sandboxv1.SnapshotState_SNAPSHOT_STATE_FAILED:
		return SnapshotStateFailed
	case sandboxv1.SnapshotState_SNAPSHOT_STATE_DELETING:
		return SnapshotStateDeleting
	case sandboxv1.SnapshotState_SNAPSHOT_STATE_DELETED:
		return SnapshotStateDeleted
	default:
		return SnapshotStateUnspecified
	}
}

func snapshotDurabilityStateFromProto(state sandboxv1.SnapshotDurabilityState) SnapshotDurabilityState {
	switch state {
	case sandboxv1.SnapshotDurabilityState_SNAPSHOT_DURABILITY_STATE_LOCAL_READY:
		return SnapshotDurabilityStateLocalReady
	case sandboxv1.SnapshotDurabilityState_SNAPSHOT_DURABILITY_STATE_DURABLE:
		return SnapshotDurabilityStateDurable
	case sandboxv1.SnapshotDurabilityState_SNAPSHOT_DURABILITY_STATE_DURABLE_CEPH:
		return SnapshotDurabilityStateDurableCeph
	case sandboxv1.SnapshotDurabilityState_SNAPSHOT_DURABILITY_STATE_PROPAGATION_FAILED:
		return SnapshotDurabilityStatePropagationFailed
	default:
		return SnapshotDurabilityStateUnspecified
	}
}

// IsDurable returns true if the snapshot's R2 upload is complete.
func (s SnapshotDurabilityState) IsDurable() bool {
	return s == SnapshotDurabilityStateDurable
}

func snapshotTypeFromProto(state sandboxv1.SnapshotType) SnapshotType {
	switch state {
	case sandboxv1.SnapshotType_SNAPSHOT_TYPE_USER:
		return SnapshotTypeUser
	case sandboxv1.SnapshotType_SNAPSHOT_TYPE_PAUSE:
		return SnapshotTypePause
	case sandboxv1.SnapshotType_SNAPSHOT_TYPE_TEMPLATE:
		return SnapshotTypeTemplate
	default:
		return SnapshotTypeUnspecified
	}
}
