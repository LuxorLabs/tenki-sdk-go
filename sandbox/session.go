package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SessionState mirrors session lifecycle states from service contract.
type SessionState string

type RuntimeState string

// WildcardStatus reports the readiness of the workspace wildcard domain
// backing a wildcard-scheme preview URL (the workspace's per-region wildcard
// certificate). It mirrors the service contract's WildcardStatus enum. A
// wildcard-scheme preview URL exists immediately but only starts serving once
// its workspace domain is READY.
type WildcardStatus string

const (
	SessionStateUnspecified  SessionState = "UNSPECIFIED"
	SessionStateCreating     SessionState = "CREATING"
	SessionStateRunning      SessionState = "RUNNING"
	SessionStatePaused       SessionState = "PAUSED"
	SessionStateUserShutdown SessionState = "USER_SHUTDOWN"
	SessionStatePausing      SessionState = "PAUSING"
	SessionStateResuming     SessionState = "RESUMING"
	SessionStateTerminating  SessionState = "TERMINATING"
	SessionStateTerminated   SessionState = "TERMINATED"

	RuntimeStateUnspecified RuntimeState = "UNSPECIFIED"
	RuntimeStateStarting    RuntimeState = "STARTING"
	RuntimeStateReady       RuntimeState = "READY"
	RuntimeStateFailed      RuntimeState = "FAILED"
	RuntimeStateStopped     RuntimeState = "STOPPED"

	WildcardStatusUnspecified WildcardStatus = "UNSPECIFIED"
	WildcardStatusPending     WildcardStatus = "PENDING"
	WildcardStatusReady       WildcardStatus = "READY"
	WildcardStatusFailed      WildcardStatus = "FAILED"
	WildcardStatusDisabled    WildcardStatus = "DISABLED"
)

// Session is SDK view of sandbox session.
type Session struct {
	client *Client

	Git *Git

	dataPlaneMu         sync.RWMutex
	dataPlaneEndpoint   string
	dataPlaneCredential string
	dataPlaneExpiresAt  time.Time
	dataPlaneClient     sandboxv1connect.SandboxSessionDataPlaneServiceClient
	renewalCancel       context.CancelFunc

	// dataPlaneReadyMu guards a one-time readiness probe per data-plane client.
	// The per-session edge (Edge) route is published by the engine and applied
	// asynchronously, so the endpoint can be handed back before the route is
	// serving; early requests hit the edge's default 404. The gate probes until
	// the route serves so the first real op never races the edge apply.
	dataPlaneReadyMu        sync.Mutex
	dataPlaneVerifiedClient sandboxv1connect.SandboxSessionDataPlaneServiceClient

	// dataPlaneWarmOnce opens the connection once the endpoint is known so the first real op skips the handshake.
	dataPlaneWarmOnce sync.Once

	ID                        string
	Name                      string
	State                     SessionState
	OwnerType                 string
	OwnerID                   string
	WorkspaceID               string
	InboundEnabled            bool
	OutboundEnabled           bool
	CPUCores                  int32
	MemoryMB                  int32
	DiskSizeGB                int32
	TimeoutAt                 time.Time
	IdleTimeoutMinutes        *int32
	LastActivityAt            time.Time
	PausedAt                  time.Time
	PauseSnapshotID           string
	PauseRetention            *time.Duration
	PauseExpiresAt            *time.Time
	Sticky                    bool
	Metadata                  map[string]string
	Tags                      []string
	VolumeMounts              []VolumeMount
	PauseSnapshot             *Snapshot
	LastResumeError           string
	TerminalError             string
	RuntimeState              RuntimeState
	RuntimeError              string
	SourceRegistryImageID     string
	SourceSnapshotID          string
	SourceRegistryWorkspaceID string
	SourceRegistryRef         string
	SourceTemplateID          string
}

type ExposedPort struct {
	Port         int32
	PreviewURL   string
	ExpiresAt    *time.Time
	PreviewURLID string
	Slug         string
	// Wildcard reports whether subdomain support is enabled for this exposure.
	Wildcard bool
	// WildcardStatus reports readiness of the wildcard subdomain provisioning.
	// It is meaningful only when Wildcard is true.
	WildcardStatus WildcardStatus
	// WildcardStatusReason carries an actionable reason when WildcardStatus is
	// WildcardStatusFailed; empty otherwise.
	WildcardStatusReason string
}

const (
	volumeAttachmentStateDetached = "DETACHED"
)

func newSession(client *Client, protoSession *sandboxv1.SandboxSession) *Session {
	session := &Session{client: client}
	session.Git = &Git{session: session}
	session.apply(protoSession)
	return session
}

func newSessionFromCreate(client *Client, resp *sandboxv1.CreateSessionResponse) *Session {
	if resp == nil {
		return newSession(client, nil)
	}
	session := newSession(client, resp.GetSession())
	session.applyDataPlaneResponse(resp.GetDataPlaneEndpoint(), resp.GetCredential(), resp.GetRouteStatus())
	return session
}

func (s *Session) apply(protoSession *sandboxv1.SandboxSession) {
	if s == nil || protoSession == nil {
		return
	}

	s.ID = protoSession.Id
	s.Name = protoSession.Name
	s.State = sessionStateFromProto(protoSession.State)
	s.OwnerType = protoSession.OwnerType
	s.OwnerID = protoSession.OwnerId
	s.WorkspaceID = protoSession.WorkspaceId
	s.InboundEnabled = protoSession.InboundEnabled
	s.OutboundEnabled = protoSession.OutboundEnabled
	s.CPUCores = protoSession.CpuCores
	s.MemoryMB = protoSession.MemoryMb
	s.DiskSizeGB = protoSession.DiskSizeGb
	s.IdleTimeoutMinutes = protoSession.IdleTimeoutMinutes
	if protoSession.TimeoutAt != nil {
		s.TimeoutAt = protoSession.TimeoutAt.AsTime()
	}
	if protoSession.LastActivityAt != nil {
		s.LastActivityAt = protoSession.LastActivityAt.AsTime()
	}
	if protoSession.PausedAt != nil {
		s.PausedAt = protoSession.PausedAt.AsTime()
	}
	if protoSession.PauseSnapshotId != nil {
		s.PauseSnapshotID = protoSession.GetPauseSnapshotId()
	} else {
		s.PauseSnapshotID = ""
	}
	if protoSession.PauseRetention != nil {
		retention := protoSession.PauseRetention.AsDuration()
		s.PauseRetention = &retention
	} else {
		s.PauseRetention = nil
	}
	if protoSession.PauseExpiresAt != nil {
		expiresAt := protoSession.PauseExpiresAt.AsTime()
		s.PauseExpiresAt = &expiresAt
	} else {
		s.PauseExpiresAt = nil
	}
	s.Sticky = protoSession.Sticky
	s.LastResumeError = protoSession.LastResumeError
	s.TerminalError = protoSession.TerminalError
	s.RuntimeState = runtimeStateFromProto(protoSession.RuntimeState)
	s.RuntimeError = protoSession.GetRuntimeError()
	s.SourceRegistryImageID = protoSession.GetSourceRegistryImageId()
	s.SourceSnapshotID = protoSession.GetSourceSnapshotId()
	s.SourceRegistryWorkspaceID = protoSession.GetSourceRegistryWorkspaceId()
	s.SourceRegistryRef = protoSession.GetSourceRegistryRef()
	s.SourceTemplateID = protoSession.GetSourceTemplateId()
	s.Metadata = cloneStringMap(protoSession.Metadata)
	s.Tags = append(s.Tags[:0], protoSession.Tags...)
	s.PauseSnapshot = snapshotFromProto(protoSession.PauseSnapshot)
	s.VolumeMounts = s.VolumeMounts[:0]
	for _, attachment := range protoSession.VolumeAttachments {
		if attachment == nil {
			continue
		}
		s.VolumeMounts = append(s.VolumeMounts, VolumeMount{
			VolumeID:  attachment.VolumeId,
			MountPath: attachment.MountPath,
			ReadOnly:  attachment.Readonly,
			State:     attachment.State,
		})
	}
}

// IsReady returns true if the session can accept commands.
func (s SessionState) IsReady() bool {
	return s == SessionStateRunning
}

// IsTerminal returns true if the session has reached a final state.
func (s SessionState) IsTerminal() bool {
	return s == SessionStateTerminated || s == SessionStateTerminating
}

func sessionStateFromProto(state sandboxv1.SessionState) SessionState {
	switch state {
	case sandboxv1.SessionState_SESSION_STATE_CREATING:
		return SessionStateCreating
	case sandboxv1.SessionState_SESSION_STATE_RUNNING:
		return SessionStateRunning
	case sandboxv1.SessionState_SESSION_STATE_PAUSED:
		return SessionStatePaused
	case sandboxv1.SessionState_SESSION_STATE_USER_SHUTDOWN:
		return SessionStateUserShutdown
	case sandboxv1.SessionState_SESSION_STATE_PAUSING:
		return SessionStatePausing
	case sandboxv1.SessionState_SESSION_STATE_RESUMING:
		return SessionStateResuming
	case sandboxv1.SessionState_SESSION_STATE_TERMINATING:
		return SessionStateTerminating
	case sandboxv1.SessionState_SESSION_STATE_TERMINATED:
		return SessionStateTerminated
	default:
		return SessionStateUnspecified
	}
}

func wildcardStatusFromProto(status sandboxv1.WildcardStatus) WildcardStatus {
	switch status {
	case sandboxv1.WildcardStatus_WILDCARD_STATUS_PENDING:
		return WildcardStatusPending
	case sandboxv1.WildcardStatus_WILDCARD_STATUS_READY:
		return WildcardStatusReady
	case sandboxv1.WildcardStatus_WILDCARD_STATUS_FAILED:
		return WildcardStatusFailed
	case sandboxv1.WildcardStatus_WILDCARD_STATUS_DISABLED:
		return WildcardStatusDisabled
	default:
		return WildcardStatusUnspecified
	}
}

func runtimeStateFromProto(state sandboxv1.TemplateRuntimeState) RuntimeState {
	switch state {
	case sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_STARTING:
		return RuntimeStateStarting
	case sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_READY:
		return RuntimeStateReady
	case sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_FAILED:
		return RuntimeStateFailed
	case sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_STOPPED:
		return RuntimeStateStopped
	default:
		return RuntimeStateUnspecified
	}
}

func (s *Session) copyFrom(other *Session) {
	s.ID = other.ID
	s.Name = other.Name
	s.State = other.State
	s.OwnerType = other.OwnerType
	s.OwnerID = other.OwnerID
	s.InboundEnabled = other.InboundEnabled
	s.OutboundEnabled = other.OutboundEnabled
	s.CPUCores = other.CPUCores
	s.MemoryMB = other.MemoryMB
	s.DiskSizeGB = other.DiskSizeGB
	s.TimeoutAt = other.TimeoutAt
	s.IdleTimeoutMinutes = other.IdleTimeoutMinutes
	s.LastActivityAt = other.LastActivityAt
	s.PausedAt = other.PausedAt
	s.PauseSnapshotID = other.PauseSnapshotID
	s.PauseRetention = other.PauseRetention
	s.Sticky = other.Sticky
	s.Metadata = other.Metadata
	s.Tags = other.Tags
	s.VolumeMounts = other.VolumeMounts
	s.PauseSnapshot = other.PauseSnapshot
	s.LastResumeError = other.LastResumeError
	s.TerminalError = other.TerminalError
	s.RuntimeState = other.RuntimeState
	s.RuntimeError = other.RuntimeError
	s.SourceRegistryImageID = other.SourceRegistryImageID
	s.SourceSnapshotID = other.SourceSnapshotID
	s.SourceRegistryWorkspaceID = other.SourceRegistryWorkspaceID
	s.SourceRegistryRef = other.SourceRegistryRef
	s.SourceTemplateID = other.SourceTemplateID
}

func (s *Session) configureDataPlane(endpoint string, credential *sandboxv1.SessionCredential) {
	if s == nil {
		return
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	s.dataPlaneMu.Lock()
	defer s.dataPlaneMu.Unlock()
	if s.dataPlaneEndpoint != endpoint || s.dataPlaneClient == nil {
		if s.renewalCancel != nil {
			s.renewalCancel()
			s.renewalCancel = nil
		}
		s.dataPlaneEndpoint = endpoint
		s.dataPlaneClient = sandboxv1connect.NewSandboxSessionDataPlaneServiceClient(
			newDataPlaneHTTPClient(endpoint),
			endpoint,
			s.client.dataPlaneClientOptions(s)...,
		)
	}
	if credential != nil {
		s.setDataPlaneCredentialLocked(credential)
	}
}

func (s *Session) applyDataPlaneResponse(
	endpoint string,
	credential *sandboxv1.SessionCredential,
	routeStatus sandboxv1.DataPlaneRouteStatus,
) {
	if routeStatus == sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_FAILED {
		return
	}
	s.configureDataPlane(endpoint, credential)
	if routeStatus == sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_VERIFIED {
		s.markCurrentDataPlaneVerified()
		return
	}
	s.startDataPlaneWarm()
}

// dataPlaneWarmTimeout bounds the detached connection warm; it never gates a caller.
const dataPlaneWarmTimeout = 10 * time.Second

const dataPlaneProbeHeader = "X-Tenki-Dataplane-Probe"
const dataPlaneProbeWarmup = "warmup"

// startDataPlaneWarm opens the connection once an endpoint is known so the first real op skips the handshake. The probe may be rejected for want of a credential; the pooled connection is the point, so it must not mark the client verified.
func (s *Session) startDataPlaneWarm() {
	s.dataPlaneMu.RLock()
	client := s.dataPlaneClient
	s.dataPlaneMu.RUnlock()
	if client == nil {
		return
	}
	s.dataPlaneWarmOnce.Do(func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), dataPlaneWarmTimeout)
			defer cancel()
			req := connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceStatRequest{
				Request: &sandboxv1.StatRequest{SessionId: s.ID, Path: "/home/tenki"},
			})
			req.Header().Set(dataPlaneProbeHeader, dataPlaneProbeWarmup)
			_, _ = client.Stat(ctx, req)
		}()
	})
}

func (s *Session) markCurrentDataPlaneVerified() {
	if s == nil {
		return
	}
	s.dataPlaneMu.RLock()
	client := s.dataPlaneClient
	s.dataPlaneMu.RUnlock()
	if client == nil {
		return
	}
	s.dataPlaneReadyMu.Lock()
	s.dataPlaneVerifiedClient = client
	s.dataPlaneReadyMu.Unlock()
}

func (s *Session) setDataPlaneCredentialLocked(credential *sandboxv1.SessionCredential) {
	if credential == nil {
		return
	}
	s.dataPlaneCredential = strings.TrimSpace(credential.GetCredential())
	if credential.GetExpiresAt() != nil {
		s.dataPlaneExpiresAt = credential.GetExpiresAt().AsTime()
	} else {
		s.dataPlaneExpiresAt = time.Time{}
	}
	s.restartCredentialRenewalLocked()
}

func (s *Session) restartCredentialRenewalLocked() {
	if s.renewalCancel != nil {
		s.renewalCancel()
		s.renewalCancel = nil
	}
	if s.dataPlaneCredential == "" || s.dataPlaneExpiresAt.IsZero() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.renewalCancel = cancel
	go s.credentialRenewalLoop(ctx)
}

func (s *Session) stopDataPlaneRenewal() {
	if s == nil {
		return
	}
	s.dataPlaneMu.Lock()
	defer s.dataPlaneMu.Unlock()
	if s.renewalCancel != nil {
		s.renewalCancel()
		s.renewalCancel = nil
	}
}

func (s *Session) resetDataPlane() {
	if s == nil {
		return
	}
	s.dataPlaneMu.Lock()
	if s.renewalCancel != nil {
		s.renewalCancel()
		s.renewalCancel = nil
	}
	s.dataPlaneEndpoint = ""
	s.dataPlaneCredential = ""
	s.dataPlaneExpiresAt = time.Time{}
	s.dataPlaneClient = nil
	s.dataPlaneMu.Unlock()

	s.dataPlaneReadyMu.Lock()
	s.dataPlaneVerifiedClient = nil
	s.dataPlaneReadyMu.Unlock()
}

func (s *Session) dataPlane(ctx context.Context) (sandboxv1connect.SandboxSessionDataPlaneServiceClient, error) {
	s.dataPlaneMu.RLock()
	client := s.dataPlaneClient
	credential := s.dataPlaneCredential
	expiresAt := s.dataPlaneExpiresAt
	endpoint := s.dataPlaneEndpoint
	s.dataPlaneMu.RUnlock()
	// Empty endpoint = CreateSession was called pre-provision; bootstrap via CreateSessionCredential.
	if strings.TrimSpace(endpoint) == "" || client == nil {
		if err := s.renewDataPlaneCredential(ctx); err != nil {
			return nil, err
		}
		s.dataPlaneMu.RLock()
		client = s.dataPlaneClient
		endpoint = s.dataPlaneEndpoint
		s.dataPlaneMu.RUnlock()
		if strings.TrimSpace(endpoint) == "" || client == nil {
			return nil, errDataPlaneEndpointUnavailable
		}
		if err := s.ensureDataPlaneServing(ctx, client); err != nil {
			return nil, err
		}
		return client, nil
	}
	if credential == "" || (!expiresAt.IsZero() && time.Until(expiresAt) <= 10*time.Millisecond) {
		if err := s.renewDataPlaneCredential(ctx); err != nil {
			return nil, err
		}
	}
	s.dataPlaneMu.RLock()
	client = s.dataPlaneClient
	s.dataPlaneMu.RUnlock()
	if err := s.ensureDataPlaneServing(ctx, client); err != nil {
		return nil, err
	}
	return client, nil
}

// ensureDataPlaneServing probes the per-session edge route once per client
// until it stops returning an edge-not-ready 404, then caches the verified
// client. The engine publishes the Edge route synchronously but Edge applies
// the config asynchronously after the admin API returns, so the endpoint can
// be handed to the SDK before the route serves; without this gate the first
// (especially concurrent) data-plane ops race the apply and fail with
// `unimplemented: HTTP status 404`. A 404 means the request never reached the
// node-agent, so the probe is side-effect-free. Holding the mutex across the
// probe serializes cold-start callers behind a single probe.
func (s *Session) ensureDataPlaneServing(ctx context.Context, client sandboxv1connect.SandboxSessionDataPlaneServiceClient) error {
	if client == nil {
		return errDataPlaneEndpointUnavailable
	}
	s.dataPlaneReadyMu.Lock()
	defer s.dataPlaneReadyMu.Unlock()
	if s.dataPlaneVerifiedClient == client {
		return nil
	}
	var lastErr error
	readyCtx, cancel := dataPlaneReadyContext(ctx, s.client.dataPlaneReadyTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		// A Stat inside the guest workdir is the cheapest unary probe; any response
		// other than edge-not-ready proves the route is serving.
		_, err := client.Stat(readyCtx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceStatRequest{
			Request: &sandboxv1.StatRequest{SessionId: s.ID, Path: "/home/tenki"},
		}))
		if !isEdgeNotReady(err) {
			s.dataPlaneVerifiedClient = client
			return nil
		}
		lastErr = err
		if err := waitDataPlaneReadyBackoff(readyCtx, ctx, attempt, lastErr); err != nil {
			return err
		}
	}
}

func (s *Session) currentDataPlaneCredential() string {
	if s == nil {
		return ""
	}
	s.dataPlaneMu.RLock()
	defer s.dataPlaneMu.RUnlock()
	return s.dataPlaneCredential
}

func (s *Session) credentialRenewalLoop(ctx context.Context) {
	for {
		s.dataPlaneMu.RLock()
		expiresAt := s.dataPlaneExpiresAt
		s.dataPlaneMu.RUnlock()
		if expiresAt.IsZero() {
			return
		}
		wait := credentialRenewalDelay(time.Until(expiresAt))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := s.renewDataPlaneCredential(ctx); err != nil {
			// Stop on fatal (non-recoverable) errors so a session terminated
			// elsewhere can't leave this goroutine and its data-plane transport
			// retrying forever; transient and not-yet-ready states stay retryable.
			var notReady *DataPlaneNotReadyError
			fatal := errors.Is(err, ErrSessionNotFound) ||
				errors.Is(err, ErrSessionTerminated) ||
				errors.Is(err, ErrSessionExpired) ||
				errors.Is(err, ErrUnauthorized) ||
				errors.Is(err, ErrPermissionDenied) ||
				isPausedSessionStateError(err) ||
				(errors.As(err, &notReady) && notReady.Terminal)
			if fatal {
				return
			}
			backoff := credentialRenewalBackoff(time.Until(expiresAt))
			timer = time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func credentialRenewalDelay(untilExpiry time.Duration) time.Duration {
	if untilExpiry <= 0 {
		return 0
	}
	delay := untilExpiry / 2
	if delay < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	return delay
}

func credentialRenewalBackoff(untilExpiry time.Duration) time.Duration {
	if untilExpiry > 0 && untilExpiry < time.Second {
		return 10 * time.Millisecond
	}
	return time.Second
}

func (s *Session) renewDataPlaneCredential(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("sandbox: nil session")
	}
	readyCtx, cancel := dataPlaneReadyContext(ctx, s.client.dataPlaneReadyTimeout)
	defer cancel()
	var lastErr error
	for attempt := 0; ; attempt++ {
		resp, err := s.client.sandbox.CreateSessionCredential(readyCtx, connect.NewRequest(&sandboxv1.CreateSessionCredentialRequest{
			SessionId: s.ID,
		}))
		if err != nil {
			mapped := mapError(err)
			lastErr = mapped
			if !isRetryableCredentialReadinessError(mapped) {
				return mapped
			}
		} else if resp == nil || resp.Msg == nil || resp.Msg.GetCredential() == nil {
			return errors.New("sandbox: missing session credential")
		} else {
			switch resp.Msg.GetRouteStatus() {
			case sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_FAILED:
				return terminalDataPlaneNotReadyError(nil)
			case sandboxv1.DataPlaneRouteStatus_DATA_PLANE_ROUTE_STATUS_NOT_READY:
				lastErr = dataPlaneNotReadyError(nil)
			default:
				if endpoint := strings.TrimSpace(resp.Msg.GetDataPlaneEndpoint()); endpoint != "" {
					s.applyDataPlaneResponse(endpoint, resp.Msg.GetCredential(), resp.Msg.GetRouteStatus())
					return nil
				}
				s.dataPlaneMu.Lock()
				s.setDataPlaneCredentialLocked(resp.Msg.GetCredential())
				s.dataPlaneMu.Unlock()
				return nil
			}
		}
		if err := waitDataPlaneReadyBackoff(readyCtx, ctx, attempt, lastErr); err != nil {
			return err
		}
	}
}

func waitDataPlaneReadyBackoff(readyCtx, parent context.Context, attempt int, lastErr error) error {
	timer := time.NewTimer(dataPlaneReadyBackoff(attempt))
	defer timer.Stop()
	select {
	case <-readyCtx.Done():
		if errors.Is(parent.Err(), context.Canceled) {
			return parent.Err()
		}
		return dataPlaneNotReadyError(lastErr)
	case <-timer.C:
		return nil
	}
}

func isRetryableCredentialReadinessError(err error) bool {
	if err == nil {
		return false
	}
	if IsDataPlaneNotReady(err) {
		return true
	}
	return errors.Is(err, ErrInvalidState) && !isPausedSessionStateError(err)
}

// reauthOnUnauthenticated re-mints the credential on an Unauthenticated reject and
// reports whether to retry once (safe: the op was rejected before it ran).
func (s *Session) reauthOnUnauthenticated(ctx context.Context, err error) bool {
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		return false
	}
	return s.renewDataPlaneCredential(ctx) == nil
}

// WaitReady waits until the session reaches RUNNING or a terminal state.
func (s *Session) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if err := s.waitReadyStream(ctx, timeout); err != nil {
		if !isRetryableWaitStreamError(err) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return err
		}
		return s.waitReadyPoll(ctx, remaining)
	}
	return nil
}

func isRetryableWaitStreamError(err error) bool {
	if err == nil {
		return false
	}
	switch connect.CodeOf(err) {
	case connect.CodeUnimplemented, connect.CodeUnavailable, connect.CodeDeadlineExceeded:
		return true
	case connect.CodeInternal:
		msg := strings.ToLower(err.Error())
		return strings.Contains(msg, "stream reset") ||
			strings.Contains(msg, "stream error") ||
			strings.Contains(msg, "internal_error") ||
			strings.Contains(msg, "http2") ||
			strings.Contains(msg, "unexpected eof") ||
			strings.Contains(msg, "connection reset by peer") ||
			strings.Contains(msg, "server closed") ||
			strings.Contains(msg, "timeout")
	default:
		return errors.Is(err, context.DeadlineExceeded)
	}
}

func (s *Session) waitReadyStream(ctx context.Context, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := s.client.sandbox.WaitSession(waitCtx, connect.NewRequest(&sandboxv1.WaitSessionRequest{SessionId: s.ID}))
	if err != nil {
		return mapError(err)
	}
	defer stream.Close()

	for stream.Receive() {
		msg := stream.Msg()
		updated := newSession(s.client, msg.GetSession())
		s.copyFrom(updated)
		s.applyDataPlaneResponse(msg.GetDataPlaneEndpoint(), msg.GetCredential(), msg.GetRouteStatus())
		if s.State.IsReady() {
			return nil
		}
		if s.State.IsTerminal() {
			return s.terminalStateError()
		}
	}
	if err := stream.Err(); err != nil {
		return mapError(err)
	}
	if s.State.IsReady() {
		return nil
	}
	if s.State.IsTerminal() {
		return s.terminalStateError()
	}
	return fmt.Errorf("timeout waiting for session %s to become ready", s.ID)
}

func (s *Session) waitReadyPoll(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		updated, err := s.client.Get(ctx, s.ID)
		if err != nil {
			if connect.CodeOf(err) == connect.CodeNotFound {
				// Tolerate not-found during polling (read-replica lag).
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pollBackoff(attempt)):
				}
				attempt++
				continue
			}
			return err
		}
		s.copyFrom(updated)
		if s.State.IsReady() {
			return nil
		}
		if s.State.IsTerminal() {
			return s.terminalStateError()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollBackoff(attempt)):
		}
		attempt++
	}
	return fmt.Errorf("timeout waiting for session %s to become ready", s.ID)
}

// resumeRevertGrace tolerates stale stopped-state reads (read-replica lag)
// right after Resume before WaitResumed has observed RESUMING from the server.
const resumeRevertGrace = 3 * time.Second

// WaitResumed waits until an in-flight resume reaches RUNNING or fails.
// Unlike WaitReady — which only treats TERMINATED/TERMINATING as terminal and
// would spin until timeout — a session that reverts from RESUMING back to a
// stopped state (PAUSED/USER_SHUTDOWN) returns ErrResumeFailed carrying the
// server-side last_resume_error.
func (s *Session) WaitResumed(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	start := time.Now()
	sawResuming := false
	attempt := 0
	for time.Now().Before(deadline) {
		updated, err := s.client.Get(ctx, s.ID)
		if err != nil {
			if connect.CodeOf(err) == connect.CodeNotFound {
				// Tolerate not-found during polling (read-replica lag).
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pollBackoff(attempt)):
				}
				attempt++
				continue
			}
			return err
		}
		s.copyFrom(updated)
		switch {
		case s.State.IsReady():
			return nil
		case s.State.IsTerminal():
			return s.terminalStateError()
		case s.State == SessionStateResuming:
			sawResuming = true
		case s.State == SessionStatePaused || s.State == SessionStateUserShutdown:
			// A stopped state is a resume failure once we saw RESUMING from
			// the server, or after the replica-lag grace window has passed.
			if sawResuming || time.Since(start) > resumeRevertGrace {
				return s.resumeFailedError()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollBackoff(attempt)):
		}
		attempt++
	}
	return fmt.Errorf("timeout waiting for session %s to resume", s.ID)
}

func (s *Session) resumeFailedError() error {
	if s != nil && strings.TrimSpace(s.LastResumeError) != "" {
		return fmt.Errorf("%w: session %s reverted to %s: %s", ErrResumeFailed, s.ID, s.State, s.LastResumeError)
	}
	return fmt.Errorf("%w: session %s reverted to %s", ErrResumeFailed, s.ID, s.State)
}

func (s *Session) terminalStateError() error {
	if s != nil && strings.TrimSpace(s.TerminalError) != "" {
		return fmt.Errorf("session entered terminal state: %s: %s", s.State, s.TerminalError)
	}
	return fmt.Errorf("session entered terminal state: %s", s.State)
}

// Refresh re-fetches session state from the server.
func (s *Session) Refresh(ctx context.Context) error {
	updated, err := s.client.Get(ctx, s.ID)
	if err != nil {
		return err
	}
	s.copyFrom(updated)
	return nil
}

// CloseIfOpen terminates the session if it is not already terminated. Safe to call multiple times.
func (s *Session) CloseIfOpen(ctx context.Context) error {
	if s.State.IsTerminal() {
		return nil
	}
	return s.Close(ctx)
}

// Exec executes one command, waits for completion, and returns collected output.
// For incremental consumption, use Stream.
//
// Deprecated: use Session.Command(...).Exec instead.
func (s *Session) Exec(ctx context.Context, command string, opts ...ExecOption) (*Result, error) {
	cfg := defaultExecConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyExec(&cfg)
	}

	runHandle, err := s.Command(append([]string{command}, cfg.args...), RunOptions{
		Env:     cfg.env,
		Dir:     cfg.dir,
		Timeout: cfg.timeout,
	}).Stream(ctx)
	var stream *Stream
	if err == nil {
		_ = runHandle.Stdin.Close()
		stream = streamFromRunHandle(runHandle)
	}
	if err != nil {
		return nil, err
	}

	outputs := make([]Output, 0, 8)
	for {
		output, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if cfg.onOutput != nil {
			cfg.onOutput(output)
		}
		outputs = append(outputs, output)
	}

	result, err := stream.Wait()
	if err != nil {
		return nil, err
	}
	result.Outputs = outputs
	result.Stdout = nil
	result.Stderr = nil
	for _, output := range outputs {
		if output.IsStderr {
			result.Stderr = append(result.Stderr, output.Data...)
			continue
		}
		result.Stdout = append(result.Stdout, output.Data...)
	}
	return result, nil
}

// Stream starts a command immediately and returns incremental stdout/stderr output.
// Completion is reported separately through Wait. Stream rejects WithOnOutput;
// callers should consume chunks with Next instead.
//
// Deprecated: use Session.Command(...).Stream instead.
func (s *Session) Stream(ctx context.Context, command string, opts ...ExecOption) (*Stream, error) {
	cfg := defaultExecConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyExec(&cfg)
	}
	if cfg.onOutput != nil {
		return nil, errors.New("sandbox: Stream does not support WithOnOutput; use Exec with WithOnOutput instead")
	}
	runHandle, err := s.Command(append([]string{command}, cfg.args...), RunOptions{
		Env:     cfg.env,
		Dir:     cfg.dir,
		Timeout: cfg.timeout,
	}).Stream(ctx)
	if err != nil {
		return nil, err
	}
	_ = runHandle.Stdin.Close()
	return streamFromRunHandle(runHandle), nil
}

// Pause suspends this session and refreshes local session state from the response.
func (s *Session) Pause(ctx context.Context) error {
	resp, err := s.client.sandbox.PauseSession(ctx, connect.NewRequest(&sandboxv1.PauseSessionRequest{SessionId: s.ID}))
	if err != nil {
		return mapError(err)
	}
	if resp != nil && resp.Msg != nil && resp.Msg.Session != nil {
		s.apply(resp.Msg.Session)
	}
	s.resetDataPlane()
	return nil
}

// Resume resumes a paused session and refreshes local session state from the response.
func (s *Session) Resume(ctx context.Context) error {
	resp, err := s.client.sandbox.ResumeSession(ctx, connect.NewRequest(&sandboxv1.ResumeSessionRequest{SessionId: s.ID}))
	if err != nil {
		return mapError(err)
	}
	if resp != nil && resp.Msg != nil && resp.Msg.Session != nil {
		s.apply(resp.Msg.Session)
	}
	s.resetDataPlane()
	return nil
}

// Close terminates this session.
func (s *Session) Close(ctx context.Context) error {
	s.stopDataPlaneRenewal()
	resp, err := s.client.sandbox.TerminateSession(ctx, connect.NewRequest(&sandboxv1.TerminateSessionRequest{
		SessionId: s.ID,
	}))
	if err != nil {
		return mapError(err)
	}
	if resp == nil || resp.Msg == nil {
		return nil
	}
	s.apply(resp.Msg.Session)
	return nil
}

// Extend extends this session timeout by additional duration.
func (s *Session) Extend(ctx context.Context, additional time.Duration) error {
	resp, err := s.client.sandbox.ExtendSession(ctx, connect.NewRequest(&sandboxv1.ExtendSessionRequest{
		SessionId:          s.ID,
		AdditionalDuration: durationpb.New(additional),
	}))
	if err != nil {
		return mapError(err)
	}
	if resp == nil || resp.Msg == nil {
		return nil
	}
	s.apply(resp.Msg.Session)
	return nil
}

// WriteFile writes content into a file inside the session.
func (s *Session) WriteFile(ctx context.Context, path string, data []byte) error {
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return err
	}
	_, err = dp.WriteFile(ctx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileRequest{Request: &sandboxv1.WriteFileRequest{
		SessionId: s.ID,
		Path:      path,
		Content:   data,
	}}))
	return mapError(err)
}

// ReadFile reads file content from inside the session.
func (s *Session) ReadFile(ctx context.Context, path string) ([]byte, error) {
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := dp.ReadFile(ctx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceReadFileRequest{Request: &sandboxv1.ReadFileRequest{
		SessionId: s.ID,
		Path:      path,
	}}))
	if err != nil {
		return nil, mapError(err)
	}
	return append([]byte(nil), resp.Msg.GetResponse().GetContent()...), nil
}

// ExposePort publishes a guest port through the preview gateway.
func (s *Session) ExposePort(ctx context.Context, port int32, opts ...ExposePortOption) (*ExposedPort, error) {
	cfg := defaultExposeConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyExpose(&cfg)
	}

	req := &sandboxv1.ExposePortRequest{
		SessionId: s.ID,
		Port:      port,
	}
	if cfg.expiresAt != nil {
		req.ExpiresAt = timestamppb.New(*cfg.expiresAt)
	}
	if cfg.slug != "" {
		req.Slug = &cfg.slug
	}

	resp, err := s.client.sandbox.ExposePort(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}

	if resp == nil || resp.Msg == nil {
		return &ExposedPort{Port: port}, nil
	}

	return &ExposedPort{
		Port:                 resp.Msg.Port,
		PreviewURL:           resp.Msg.PreviewUrl,
		ExpiresAt:            protoTimestampPtr(resp.Msg.ExpiresAt),
		PreviewURLID:         resp.Msg.GetPreviewUrlId(),
		Slug:                 resp.Msg.GetSlug(),
		Wildcard:             resp.Msg.GetWildcard(),
		WildcardStatus:       wildcardStatusFromProto(resp.Msg.GetWildcardStatus()),
		WildcardStatusReason: resp.Msg.GetWildcardStatusReason(),
	}, nil
}

// UnexposePort removes one preview port mapping.
func (s *Session) UnexposePort(ctx context.Context, port int32) error {
	_, err := s.client.sandbox.UnexposePort(ctx, connect.NewRequest(&sandboxv1.UnexposePortRequest{
		SessionId: s.ID,
		Port:      port,
	}))
	return mapError(err)
}

// ListExposedPorts returns all exposed ports for this session.
func (s *Session) ListExposedPorts(ctx context.Context) ([]ExposedPort, error) {
	resp, err := s.client.sandbox.ListExposedPorts(ctx, connect.NewRequest(&sandboxv1.ListExposedPortsRequest{
		SessionId: s.ID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	if resp == nil || resp.Msg == nil {
		return []ExposedPort{}, nil
	}

	ports := make([]ExposedPort, 0, len(resp.Msg.Ports))
	for _, p := range resp.Msg.Ports {
		if p == nil {
			continue
		}
		ports = append(ports, ExposedPort{
			Port:                 p.Port,
			PreviewURL:           p.PreviewUrl,
			ExpiresAt:            protoTimestampPtr(p.ExpiresAt),
			PreviewURLID:         p.GetPreviewUrlId(),
			Slug:                 p.GetSlug(),
			Wildcard:             p.GetWildcard(),
			WildcardStatus:       wildcardStatusFromProto(p.GetWildcardStatus()),
			WildcardStatusReason: p.GetWildcardStatusReason(),
		})
	}
	return ports, nil
}

func protoTimestampPtr(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}

// UpdateSSHAuthorizedKeys replaces authorized SSH keys for this session.
func (s *Session) UpdateSSHAuthorizedKeys(ctx context.Context, keys []string) error {
	_, err := s.client.sandbox.UpdateSSHAuthorizedKeys(ctx, connect.NewRequest(&sandboxv1.UpdateSSHAuthorizedKeysRequest{
		SessionId:         s.ID,
		SshAuthorizedKeys: append([]string(nil), keys...),
	}))
	return mapError(err)
}

// Update applies mutable session fields.
func (s *Session) Update(ctx context.Context, opts ...UpdateSessionOption) error {
	cfg := updateSessionConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyUpdateSession(&cfg)
	}

	req := &sandboxv1.UpdateSessionRequest{SessionId: s.ID}
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
	if cfg.sticky != nil {
		req.Sticky = cfg.sticky
	}

	resp, err := s.client.sandbox.UpdateSession(ctx, connect.NewRequest(req))
	if err != nil {
		return mapError(err)
	}
	if resp != nil && resp.Msg != nil && resp.Msg.Session != nil {
		s.apply(resp.Msg.Session)
	}
	return nil
}

// UpdateTags replaces tags for this session.
func (s *Session) UpdateTags(ctx context.Context, tags ...string) error {
	return s.Update(ctx, WithTags(tags...))
}

// AttachVolume hot-attaches one persistent volume to the running session.
func (s *Session) AttachVolume(ctx context.Context, volumeID string, mountPath string, opts ...VolumeOption) error {
	cfg := volumeConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyVolume(&cfg)
	}

	resp, err := s.client.sandbox.AttachVolume(ctx, connect.NewRequest(&sandboxv1.AttachVolumeRequest{
		SessionId: s.ID,
		Volume: &sandboxv1.VolumeMount{
			VolumeId:  volumeID,
			MountPath: mountPath,
			Readonly:  cfg.readonly,
		},
	}))
	if err != nil {
		return mapError(err)
	}
	if resp != nil && resp.Msg != nil && resp.Msg.Attachment != nil {
		s.VolumeMounts = append(s.VolumeMounts, VolumeMount{
			VolumeID:  resp.Msg.Attachment.VolumeId,
			MountPath: resp.Msg.Attachment.MountPath,
			ReadOnly:  resp.Msg.Attachment.Readonly,
		})
	}
	return nil
}

// DetachVolume detaches one persistent volume from the session.
func (s *Session) DetachVolume(ctx context.Context, volumeID string, opts ...DetachVolumeOption) error {
	cfg := defaultDetachVolumeConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyDetachVolume(&cfg)
	}

	_, err := s.client.sandbox.DetachVolume(ctx, connect.NewRequest(&sandboxv1.DetachVolumeRequest{
		SessionId:   s.ID,
		VolumeId:    volumeID,
		ForceDetach: cfg.force,
	}))
	if err != nil {
		return mapError(err)
	}
	if cfg.waitTimeout > 0 {
		if err := s.waitVolumeDetached(ctx, volumeID, cfg.waitTimeout); err != nil {
			return err
		}
	}

	filtered := s.VolumeMounts[:0]
	for _, volume := range s.VolumeMounts {
		if volume.VolumeID == volumeID {
			continue
		}
		filtered = append(filtered, volume)
	}
	s.VolumeMounts = filtered
	return nil
}

func (s *Session) waitVolumeDetached(ctx context.Context, volumeID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		updated, err := s.client.Get(ctx, s.ID)
		if err != nil {
			return err
		}
		s.copyFrom(updated)
		if s.isVolumeDetached(volumeID) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollBackoff(attempt)):
		}
		attempt++
	}
	return fmt.Errorf("timeout waiting for volume %s to detach from session %s", volumeID, s.ID)
}

func (s *Session) isVolumeDetached(volumeID string) bool {
	for _, volume := range s.VolumeMounts {
		if volume.VolumeID != volumeID {
			continue
		}
		return volume.State == volumeAttachmentStateDetached
	}
	// Treat a missing attachment as detached because the engine may stop returning
	// the record after the detach completes.
	return true
}
