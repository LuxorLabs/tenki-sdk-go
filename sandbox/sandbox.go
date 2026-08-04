package sandbox

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	headerAuthorization    = "Authorization"
	headerClientFamily     = "X-Tenki-Client-Family"
	headerClientGeneration = "X-Tenki-Client-Generation"
	defaultCreateOwnerType = "SERVICE"
	defaultCreateOwnerID   = "self"
)

// Client is an idiomatic wrapper over SandboxService Connect client.
type Client struct {
	authToken      string
	baseURL        string
	gatewayAddress string
	// gatewayAddressExplicit is true when the operator pinned the
	// gatewayAddress via WithGatewayAddress / TENKI_SANDBOX_GATEWAY_URL /
	// config. When false (i.e. we fell back to deriveGatewayAddress), the
	// SSH method will first try to discover the SSH WS bridge URL by
	// calling engine.ListActiveSSHGateways, which lets staging/prod
	// announce the new Edge-fronted bridge without operator-side config
	// edits. Cached gateway address survives across SSH calls.
	gatewayAddressExplicit bool
	httpClient             *http.Client
	connectOpts            []connect.ClientOption
	dataPlaneReadyTimeout  time.Duration
	sandbox                sandboxv1connect.SandboxServiceClient
	sshGateway             sandboxv1connect.SSHGatewayClientServiceClient
}

var (
	// ErrMissingAuthToken is returned when no auth token is provided via WithAuthToken or env vars.
	ErrMissingAuthToken = errors.New("sandbox: missing auth token - set TENKI_AUTH_TOKEN or TENKI_API_KEY or use WithAuthToken")
	// ErrInvalidAuthToken is returned when a nonempty auth token does not start with "tk_".
	ErrInvalidAuthToken = errors.New("sandbox: invalid auth token - API keys and service tokens must start with tk_")
)

// New creates a new sandbox SDK client.
//
// Auth token resolution: WithAuthToken option > TENKI_AUTH_TOKEN env var > TENKI_API_KEY env var > error.
// Base URL resolution: WithBaseURL option > TENKI_API_ENDPOINT env var > TENKI_API_URL env var > https://api.tenki.cloud.
// Gateway URL resolution: WithGatewayAddress option > TENKI_SANDBOX_GATEWAY_URL env var > derived from base URL.
func New(opts ...Option) (*Client, error) {
	cfg := defaultClientConfig()

	// Apply env var fallbacks before options.
	if envURL := os.Getenv(EnvAPIEndpoint); envURL != "" {
		cfg.baseURL = envURL
	} else if envURL := os.Getenv(EnvAPIURL); envURL != "" {
		cfg.baseURL = envURL
	}
	if envGatewayURL := os.Getenv(EnvGatewayURL); envGatewayURL != "" {
		cfg.gatewayAddress = envGatewayURL
	}
	if envKey := os.Getenv(EnvAuthToken); envKey != "" {
		cfg.authToken = envKey
	} else if envKey := os.Getenv(EnvAPIKey); envKey != "" {
		cfg.authToken = envKey
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.apply(&cfg)
	}

	authToken := strings.TrimSpace(cfg.authToken)
	if authToken == "" {
		return nil, ErrMissingAuthToken
	}
	if !strings.HasPrefix(authToken, "tk_") {
		return nil, ErrInvalidAuthToken
	}
	cfg.authToken = authToken
	gatewayAddressExplicit := strings.TrimSpace(cfg.gatewayAddress) != ""
	if !gatewayAddressExplicit {
		cfg.gatewayAddress = deriveGatewayAddress(cfg.baseURL)
	}

	prewarmControlPlane := cfg.httpClient == nil
	if cfg.httpClient == nil {
		transport := &http2.Transport{
			ReadIdleTimeout: 30 * time.Second,
			PingTimeout:     5 * time.Second,
		}
		if strings.HasPrefix(cfg.baseURL, "http://") {
			transport.AllowHTTP = true
			transport.DialTLS = func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			}
		}
		cfg.httpClient = &http.Client{Timeout: cfg.httpTimeout, Transport: transport}
	}

	interceptor := &authInterceptor{authToken: cfg.authToken}

	connectOpts := append(
		[]connect.ClientOption{connect.WithInterceptors(interceptor), connect.WithGRPC()},
		cfg.connectOpts...,
	)

	sandboxClient := sandboxv1connect.NewSandboxServiceClient(cfg.httpClient, cfg.baseURL, connectOpts...)
	sshGatewayClient := sandboxv1connect.NewSSHGatewayClientServiceClient(cfg.httpClient, cfg.baseURL, connectOpts...)

	client := &Client{
		authToken:              cfg.authToken,
		baseURL:                cfg.baseURL,
		gatewayAddress:         cfg.gatewayAddress,
		gatewayAddressExplicit: gatewayAddressExplicit,
		httpClient:             cfg.httpClient,
		connectOpts:            append([]connect.ClientOption(nil), cfg.connectOpts...),
		dataPlaneReadyTimeout:  cfg.dataPlaneReadyTimeout,
		sandbox:                sandboxClient,
		sshGateway:             sshGatewayClient,
	}
	if prewarmControlPlane {
		go prewarmControlPlaneConnection(cfg.httpClient, cfg.baseURL)
	}
	return client, nil
}

func prewarmControlPlaneConnection(httpClient *http.Client, baseURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, baseURL, nil)
	if err != nil {
		return
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// Close closes idle HTTP connections held by the underlying transport.
func (c *Client) Close() error {
	if c == nil || c.httpClient == nil || c.httpClient.Transport == nil {
		return nil
	}

	if transport, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}

	return nil
}

// Create creates a sandbox session and returns an SDK Session wrapper.
//
// If readiness fails after admission, the returned *WaitReadyFailedError carries
// the live session so callers can close it or continue waiting.
func (c *Client) Create(ctx context.Context, opts ...CreateOption) (*Session, error) {
	cfg := defaultCreateConfig(c)
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyCreate(&cfg)
	}
	if err := validateCreateResources(cfg.cpuCores, cfg.memoryMB, cfg.diskSizeGB); err != nil {
		return nil, err
	}
	sourceCount := 0
	for _, source := range []string{cfg.image, cfg.snapshotID, cfg.templateSpecID} {
		if source != "" {
			sourceCount++
		}
	}
	if sourceCount > 1 {
		return nil, errors.New("sandbox: only one of image, snapshot, or template spec may be set")
	}
	req := &sandboxv1.CreateSessionRequest{
		OwnerType:         defaultCreateOwnerType,
		OwnerId:           defaultCreateOwnerID,
		Name:              cfg.name,
		AllowInbound:      &cfg.allowInbound,
		AllowOutbound:     &cfg.allowOutbound,
		Metadata:          cloneStringMap(cfg.metadata),
		Tags:              append([]string(nil), cfg.tags...),
		Env:               cloneStringMap(cfg.env),
		SshAuthorizedKeys: append([]string(nil), cfg.sshKeys...),
		EnableOpencode:    cfg.enableOpenCode,
		CloneRepoUrl:      cfg.cloneRepoURL,
		SetupEnv:          cloneStringMap(cfg.setupEnv),
		SetupSecrets:      cloneStringMap(cfg.setupSecrets),
	}
	if len(cfg.volumes) > 0 {
		req.Volumes = make([]*sandboxv1.VolumeMount, 0, len(cfg.volumes))
		for _, volume := range cfg.volumes {
			if volume == nil {
				continue
			}
			req.Volumes = append(req.Volumes, &sandboxv1.VolumeMount{
				VolumeId:  volume.VolumeID,
				MountPath: volume.MountPath,
				Readonly:  volume.ReadOnly,
			})
		}
	}

	if cfg.maxDuration != nil {
		req.MaxDuration = durationpb.New(*cfg.maxDuration)
	}
	if cfg.idleTimeout != nil {
		minutes := int32(cfg.idleTimeout.Round(time.Minute) / time.Minute)
		if *cfg.idleTimeout <= 0 {
			minutes = 0
		}
		req.IdleTimeoutMinutes = &minutes
	}
	if cfg.pauseRetention != nil && *cfg.pauseRetention > 0 {
		req.PauseRetention = durationpb.New(*cfg.pauseRetention)
	}
	if cfg.waitReady && cfg.waitForRuntime {
		req.WaitReady = true
	}
	req.WaitForRuntime = cfg.waitForRuntime
	if cfg.sticky {
		req.Sticky = true
	}
	hasSource := cfg.image != "" || cfg.snapshotID != "" || cfg.templateSpecID != ""
	if cfg.cpuCores != nil && (!hasSource || cfg.cpuCoresSet) {
		req.CpuCores = cfg.cpuCores
	}
	if cfg.memoryMB != nil && (!hasSource || cfg.memoryMBSet) {
		req.MemoryMb = cfg.memoryMB
	}
	if cfg.diskSizeGB != nil && (!hasSource || cfg.diskSizeGBSet) {
		req.DiskSizeGb = cfg.diskSizeGB
	}
	if cfg.image != "" {
		req.RegistryRef = &cfg.image
	} else if cfg.snapshotID != "" {
		req.SnapshotId = &cfg.snapshotID
	} else if cfg.templateSpecID != "" {
		req.TemplateSpecId = &cfg.templateSpecID
	}
	if workspaceID := strings.TrimSpace(cfg.workspaceID); workspaceID != "" {
		req.WorkspaceId = &workspaceID
	}

	if !cfg.waitReady && !cfg.waitForRuntime {
		resp, err := c.sandbox.CreateSession(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, mapCreateSessionError(c, err)
		}
		return newSessionFromCreate(c, resp.Msg), nil
	}

	waitTimeout := effectiveCreateWaitTimeout(cfg)
	createCtx, cancel := c.heldCreateContext(ctx, waitTimeout)
	defer cancel()
	resp, err := c.sandbox.CreateSession(createCtx, connect.NewRequest(req))
	if err != nil {
		return nil, mapCreateSessionError(c, err)
	}
	sess := newSessionFromCreate(c, resp.Msg)
	if sess.State.IsReady() {
		return sess, nil
	}
	if sess.State.IsTerminal() {
		return nil, sess.terminalStateError()
	}
	// Normal readiness waits on the stream so it can expose and prewarm the data plane during provisioning.
	if err := sess.WaitReady(ctx, waitTimeout); err != nil {
		if sess.State.IsTerminal() {
			return nil, err
		}
		return nil, &WaitReadyFailedError{Session: sess, Err: err}
	}
	return sess, nil
}

func effectiveCreateWaitTimeout(cfg createConfig) time.Duration {
	if cfg.templateSpecID != "" && !cfg.waitTimeoutSet {
		return DefaultTemplateSpecCreateTimeout
	}
	return cfg.waitTimeout
}

func mapCreateSessionError(client *Client, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		for _, rawDetail := range connectErr.Details() {
			detail, detailErr := rawDetail.Value()
			if detailErr != nil {
				continue
			}
			failure, ok := detail.(*sandboxv1.TemplateRuntimeFailure)
			if !ok {
				continue
			}
			return &TemplateRuntimeFailedError{
				Session: newSession(client, failure.GetSession()),
				Reason:  failure.GetReason(),
				Err:     err,
			}
		}
	}
	return mapError(err)
}

// List lists sessions owned by the Workspace API key.
func (c *Client) List(ctx context.Context, opts ...ListOption) ([]*Session, error) {
	cfg := defaultListConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyList(&cfg)
	}
	var sessions []*Session
	var pageToken string
	for {
		resp, err := c.sandbox.ListWorkspaceSandboxes(ctx, connect.NewRequest(&sandboxv1.ListWorkspaceSandboxesRequest{
			WorkspaceId:       cfg.workspaceID,
			PageSize:          automaticPageSize,
			PageToken:         pageToken,
			Tags:              append([]string(nil), cfg.tags...),
			Sticky:            cfg.sticky,
			IncludeTerminated: cfg.includeTerminated,
		}))
		if err != nil {
			return nil, mapError(err)
		}
		sessions = append(sessions, sessionsFromProto(c, resp.Msg.Sessions)...)
		pageToken, err = advancePageToken(pageToken, resp.Msg.NextPageToken)
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			return sessions, nil
		}
	}
}

func sessionsFromProto(client *Client, protoSessions []*sandboxv1.SandboxSession) []*Session {
	sessions := make([]*Session, 0, len(protoSessions))
	for _, protoSession := range protoSessions {
		sessions = append(sessions, newSession(client, protoSession))
	}
	return sessions
}

// Session returns a local session handle for an already-known session ID without an API lookup.
func (c *Client) Session(sessionID string) (*Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("sandbox: session id is required")
	}
	return newSession(c, &sandboxv1.SandboxSession{Id: sessionID}), nil
}

// Get fetches a single session by ID.
func (c *Client) Get(ctx context.Context, sessionID string) (*Session, error) {
	resp, err := c.sandbox.GetSession(ctx, connect.NewRequest(&sandboxv1.GetSessionRequest{
		SessionId: sessionID,
	}))
	if err != nil {
		return nil, mapError(err)
	}

	return newSession(c, resp.Msg.Session), nil
}

// CreateAndWait is kept for compatibility: Create already waits by default.
// It is equivalent to Create with WithWaitTimeout(timeout).
func (c *Client) CreateAndWait(ctx context.Context, timeout time.Duration, opts ...CreateOption) (*Session, error) {
	return c.Create(ctx, append(opts, WithWaitReady(true), WithWaitTimeout(timeout))...)
}

// heldCreateContext bounds the held create under the transport timeout: the server returns 2s ahead
// of the request deadline, so the response beats the HTTP client cancel; WaitReady covers leftover budget.
func (c *Client) heldCreateContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	bound := timeout
	if c.httpClient != nil && c.httpClient.Timeout > 3*time.Second {
		if transportBound := c.httpClient.Timeout - time.Second; bound <= 0 || transportBound < bound {
			bound = transportBound
		}
	}
	if bound <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, bound)
}

// WaitSnapshotReady polls until a snapshot reaches READY or a terminal state.
func (c *Client) WaitSnapshotReady(ctx context.Context, snapshotID string, timeout time.Duration) (*Snapshot, error) {
	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		snap, err := c.GetSnapshot(ctx, snapshotID)
		if err != nil {
			return nil, err
		}
		if snap.State.IsReady() {
			return snap, nil
		}
		if snap.State.IsTerminal() {
			return nil, fmt.Errorf("snapshot entered terminal state: %s", snap.State)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollBackoff(attempt)):
		}
		attempt++
	}
	return nil, fmt.Errorf("timeout waiting for snapshot %s to become ready", snapshotID)
}

// CreateSnapshotAndWait creates a snapshot and waits for it to reach READY state.
func (c *Client) CreateSnapshotAndWait(ctx context.Context, sessionID, name string, expiresAt *time.Time, timeout time.Duration) (*Snapshot, error) {
	snap, err := c.CreateSnapshot(ctx, sessionID, name, expiresAt)
	if err != nil {
		return nil, err
	}
	return c.WaitSnapshotReady(ctx, snap.ID, timeout)
}

// WaitVolumeReady polls until a volume reaches AVAILABLE or a terminal state.
func (c *Client) WaitVolumeReady(ctx context.Context, volumeID string, timeout time.Duration) (*Volume, error) {
	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		vol, err := c.GetVolume(ctx, volumeID)
		if err != nil {
			return nil, err
		}
		if vol.State.IsReady() {
			return vol, nil
		}
		if vol.State.IsTerminal() {
			return nil, fmt.Errorf("volume entered terminal state: %s", vol.State)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollBackoff(attempt)):
		}
		attempt++
	}
	return nil, fmt.Errorf("timeout waiting for volume %s to become ready", volumeID)
}

type authInterceptor struct {
	authToken string
}

func (i *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			i.setHeaders(req.Header())
		}
		return next(ctx, req)
	}
}

func (i *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func (i *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		i.setHeaders(conn.RequestHeader())
		return conn
	}
}

func (i *authInterceptor) setHeaders(headerSetter interface {
	Set(string, string)
}) {
	setClientAuthHeaders(headerSetter, i.authToken)
}

func setClientAuthHeaders(headerSetter interface {
	Set(string, string)
}, authToken string) {
	headerSetter.Set(headerClientFamily, "go_sdk")
	headerSetter.Set(headerClientGeneration, "workspace_v1")
	headerSetter.Set(headerAuthorization, "Bearer "+authToken)
}

func (c *Client) setAuthHeaders(headers http.Header) {
	if c == nil {
		return
	}
	setClientAuthHeaders(headers, c.authToken)
}

func (c *Client) gatewaySSHURL(sessionID string) string {
	if c == nil || strings.TrimSpace(c.gatewayAddress) == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}

	u, err := url.Parse(c.gatewayAddress)
	if err != nil {
		return ""
	}

	u.Path = path.Join(strings.TrimRight(u.Path, "/"), "v1", "ssh", sessionID)
	u.RawQuery = ""
	u.Fragment = ""
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	return u.String()
}

func deriveGatewayAddress(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return ""
	}

	host := u.Hostname()
	port := u.Port()

	switch {
	case strings.HasPrefix(host, "api."):
		host = "sandbox-gateway." + strings.TrimPrefix(host, "api.")
	case strings.HasPrefix(host, "app."):
		host = "sandbox-gateway." + strings.TrimPrefix(host, "app.")
	default:
		host = "sandbox-gateway." + host
	}

	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
