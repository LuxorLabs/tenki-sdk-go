package sandbox

import (
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
)

const (
	defaultBaseURL = "https://api.tenki.cloud"
	// Held create/snapshot calls use budgets up to DefaultRestoreTimeout.
	// Keep the transport timeout above those operation-level deadlines.
	defaultClientTimeout         = DefaultRestoreTimeout + 30*time.Second
	defaultDataPlaneReadyTimeout = 60 * time.Second

	// EnvAPIEndpoint overrides the default base URL when set.
	EnvAPIEndpoint = "TENKI_API_ENDPOINT"
	// EnvAPIURL overrides the default base URL when set.
	EnvAPIURL = "TENKI_API_URL"
	// EnvGatewayURL overrides the default gateway base URL when set.
	EnvGatewayURL = "TENKI_SANDBOX_GATEWAY_URL"
	// EnvAuthToken provides the auth token when not passed via WithAuthToken.
	EnvAuthToken = "TENKI_AUTH_TOKEN"
	// EnvAPIKey provides the auth token when not passed via WithAuthToken.
	EnvAPIKey             = "TENKI_API_KEY"
	defaultCPUCores       = int32(2)
	defaultMemoryMB       = int32(4096)
	defaultDiskSizeGB     = int32(5)
	openCodeAPIKeyEnvVar  = "OPENCODE_API_KEY"
	openCodeNpmEnvVar     = "OPENCODE_PROVIDER_NPM"
	openCodeBaseURLEnvVar = "OPENCODE_PROVIDER_BASE_URL"
	githubTokenEnvVar     = "GH_TOKEN"
	gitTokenEnvVar        = "GIT_TOKEN"
)

const (
	DefaultSessionCreateTimeout      = 3 * time.Minute
	DefaultTemplateSpecCreateTimeout = 2 * time.Hour
	DefaultSnapshotCreateTimeout     = 5 * time.Minute
	DefaultRestoreTimeout            = 5 * time.Minute
	// DefaultExecTimeout is a suggested value for WithTimeout, not applied automatically.
	DefaultExecTimeout         = 30 * time.Second
	DefaultVolumeDetachTimeout = 2 * time.Minute
)

type clientConfig struct {
	baseURL               string
	gatewayAddress        string
	authToken             string
	httpClient            *http.Client
	httpTimeout           time.Duration
	dataPlaneReadyTimeout time.Duration
	connectOpts           []connect.ClientOption
	warningHandler        WarningHandler
}

func defaultClientConfig() clientConfig {
	return clientConfig{
		baseURL:               defaultBaseURL,
		httpTimeout:           defaultClientTimeout,
		dataPlaneReadyTimeout: defaultDataPlaneReadyTimeout,
		warningHandler:        defaultWarningHandler,
	}
}

// WithWarningHandler replaces warning emission. Pass nil to suppress warnings.
func WithWarningHandler(handler WarningHandler) Option {
	return optionFunc(func(cfg *clientConfig) {
		cfg.warningHandler = handler
	})
}

// Option configures Client behavior.
type Option interface {
	apply(*clientConfig)
}

type optionFunc func(*clientConfig)

func (f optionFunc) apply(cfg *clientConfig) {
	f(cfg)
}

// WithBaseURL sets sandbox service base URL.
func WithBaseURL(baseURL string) Option {
	return optionFunc(func(cfg *clientConfig) {
		cfg.baseURL = baseURL
	})
}

// WithGatewayAddress sets the sandbox gateway base URL used for SSH websocket transport.
func WithGatewayAddress(addr string) Option {
	return optionFunc(func(cfg *clientConfig) {
		cfg.gatewayAddress = addr
	})
}

// WithAuthToken sets the API key or service token used for authentication.
func WithAuthToken(token string) Option {
	return optionFunc(func(cfg *clientConfig) {
		cfg.authToken = token
	})
}

// WithHTTPClient sets custom HTTP client.
func WithHTTPClient(httpClient *http.Client) Option {
	return optionFunc(func(cfg *clientConfig) {
		cfg.httpClient = httpClient
	})
}

// WithHTTPTimeout sets HTTP timeout for default HTTP client.
func WithHTTPTimeout(timeout time.Duration) Option {
	return optionFunc(func(cfg *clientConfig) {
		cfg.httpTimeout = timeout
	})
}

// WithDataPlaneReadyTimeout sets the wall-clock budget used to wait for the
// data-plane edge route to become serving.
func WithDataPlaneReadyTimeout(timeout time.Duration) Option {
	return optionFunc(func(cfg *clientConfig) {
		if timeout > 0 {
			cfg.dataPlaneReadyTimeout = timeout
		}
	})
}

// WithConnectClientOptions appends connect client options.
func WithConnectClientOptions(opts ...connect.ClientOption) Option {
	return optionFunc(func(cfg *clientConfig) {
		cfg.connectOpts = append(cfg.connectOpts, opts...)
	})
}

// DetachVolumeOption configures Session.DetachVolume behavior.
type DetachVolumeOption interface {
	applyDetachVolume(*detachVolumeConfig)
}

type detachVolumeOptionFunc func(*detachVolumeConfig)

func (f detachVolumeOptionFunc) applyDetachVolume(cfg *detachVolumeConfig) {
	f(cfg)
}

// WithForceDetach bypasses stuck SYNC_PENDING cleanup and marks the attachment detached immediately.
func WithForceDetach() DetachVolumeOption {
	return detachVolumeOptionFunc(func(cfg *detachVolumeConfig) {
		cfg.force = true
	})
}

// WithDetachWaitTimeout overrides how long Session.DetachVolume waits for the attachment to leave the session.
// RW volumes may transition to `SYNC_PENDING` before later becoming `DETACHED` once sync-back completes.
// Use `0` to return immediately after the detach RPC succeeds.
func WithDetachWaitTimeout(timeout time.Duration) DetachVolumeOption {
	return detachVolumeOptionFunc(func(cfg *detachVolumeConfig) {
		cfg.waitTimeout = timeout
	})
}

type createConfig struct {
	allowInbound   bool
	allowOutbound  bool
	maxDuration    *time.Duration
	idleTimeout    *time.Duration
	pauseRetention *time.Duration
	cpuCores       *int32
	cpuCoresSet    bool
	memoryMB       *int32
	memoryMBSet    bool
	diskSizeGB     *int32
	diskSizeGBSet  bool
	name           string
	snapshotID     string
	image          string
	templateSpecID string
	setupEnv       map[string]string
	setupSecrets   map[string]string
	workspaceID    string
	metadata       map[string]string
	tags           []string
	env            map[string]string
	sshKeys        []string
	enableOpenCode bool
	cloneRepoURL   string
	volumes        []*VolumeMount
	sticky         bool
	waitReady      bool
	waitForRuntime bool
	waitTimeout    time.Duration
	waitTimeoutSet bool
}

type createVolumeConfig struct {
	workspaceID string
	name        string
	sizeBytes   int64
}

type templateConfig struct {
	workspaceID      string
	name             string
	nameSet          bool
	baseImageID      string
	baseImageIDSet   bool
	setupScript      string
	setupScriptSet   bool
	startCmd         *string
	startCmdSet      bool
	env              map[string]string
	envSet           bool
	tags             []string
	tagsSet          bool
	resources        *TemplateResources
	resourcesSet     bool
	parentTemplateID string
	parentImage      string
	spec             *TemplateSpec
}

type listConfig struct {
	workspaceID       string
	tags              []string
	sticky            *bool
	includeTerminated bool
}

type previewURLConfig struct {
	workspaceID string
}

type volumeConfig struct {
	readonly bool
}

type detachVolumeConfig struct {
	force       bool
	waitTimeout time.Duration
}

type exposeConfig struct {
	expiresAt *time.Time
	slug      string
}

type updateSessionConfig struct {
	name        *string
	tags        []string
	tagsSet     bool
	sticky      *bool
	maxDuration *time.Duration
}

type updateSnapshotConfig struct {
	name         *string
	expiresAt    *time.Time
	expiresAtSet bool
	tags         []string
	tagsSet      bool
}

type updateVolumeConfig struct {
	name    *string
	tags    []string
	tagsSet bool
}

// VolumeMount configures one session volume attachment.
type VolumeMount struct {
	VolumeID  string
	MountPath string
	ReadOnly  bool
	State     string
}

type nameOption struct {
	name string
}

func (o nameOption) applyCreate(cfg *createConfig) {
	cfg.name = strings.TrimSpace(o.name)
}

func (o nameOption) applyUpdateSession(cfg *updateSessionConfig) {
	name := strings.TrimSpace(o.name)
	cfg.name = &name
}

func (o nameOption) applyUpdateSnapshot(cfg *updateSnapshotConfig) {
	name := strings.TrimSpace(o.name)
	cfg.name = &name
}

func (o nameOption) applyUpdateVolume(cfg *updateVolumeConfig) {
	name := strings.TrimSpace(o.name)
	cfg.name = &name
}

// WithName sets a human-readable name on create and update requests.
func WithName(name string) interface {
	CreateOption
	UpdateSessionOption
	UpdateSnapshotOption
	UpdateVolumeOption
} {
	return nameOption{name: name}
}

// WithSnapshot restores a new session from one snapshot instead of cold-booting from a base image.
func WithSnapshot(snapshotID string) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.snapshotID = strings.TrimSpace(snapshotID)
	})
}

// ImageSource is a sandbox launch image: a raw registry ref string or a
// *RegistryImage resource object (for example a successful build's Image).
type ImageSource interface {
	string | *RegistryImage
}

// WithImage launches a new session from a registry image. A *RegistryImage
// uses its immutable digest ref when present, else its untagged ref.
func WithImage[T ImageSource](image T) CreateOption {
	ref := ""
	switch value := any(image).(type) {
	case string:
		ref = strings.TrimSpace(value)
	case *RegistryImage:
		ref = value.launchRef()
	}
	return createOptionFunc(func(cfg *createConfig) {
		cfg.image = ref
	})
}

// TemplateSpecSource identifies a typed template by ID or resource object.
type TemplateSpecSource interface {
	string | *Template
}

// FromTemplateSpec executes the current typed template spec directly in the new session.
func FromTemplateSpec[T TemplateSpecSource](template T) CreateOption {
	templateID := ""
	switch value := any(template).(type) {
	case string:
		templateID = strings.TrimSpace(value)
	case *Template:
		if value != nil {
			templateID = strings.TrimSpace(value.ID)
		}
	}
	return createOptionFunc(func(cfg *createConfig) {
		cfg.templateSpecID = templateID
	})
}

// WithSetupEnvs sets non-secret environment variables used only by template setup.
func WithSetupEnvs(env map[string]string) CreateOption {
	return createOptionFunc(func(cfg *createConfig) { cfg.setupEnv = cloneStringMap(env) })
}

// WithSetupSecrets sets secret environment variables used only by template setup.
func WithSetupSecrets(secrets map[string]string) CreateOption {
	return createOptionFunc(func(cfg *createConfig) { cfg.setupSecrets = cloneStringMap(secrets) })
}

func defaultCreateConfig(client *Client) createConfig {
	_ = client
	cpuCores := defaultCPUCores
	memoryMB := defaultMemoryMB
	diskSizeGB := defaultDiskSizeGB
	return createConfig{
		allowInbound:  true,
		allowOutbound: true,
		cpuCores:      &cpuCores,
		memoryMB:      &memoryMB,
		diskSizeGB:    &diskSizeGB,
		metadata:      map[string]string{},
		env:           map[string]string{},
		sshKeys:       []string{},
		volumes:       []*VolumeMount{},
		waitReady:     true,
		waitTimeout:   DefaultSessionCreateTimeout,
	}
}

func defaultCreateVolumeConfig() createVolumeConfig {
	return createVolumeConfig{}
}

func defaultTemplateConfig() templateConfig {
	return templateConfig{}
}

func defaultListConfig() listConfig {
	return listConfig{}
}

func defaultDetachVolumeConfig() detachVolumeConfig {
	return detachVolumeConfig{
		waitTimeout: DefaultVolumeDetachTimeout,
	}
}

func defaultExposeConfig() exposeConfig {
	return exposeConfig{}
}

// ExposePortOption configures Session.ExposePort behavior.
type ExposePortOption interface {
	applyExpose(*exposeConfig)
}

type exposePortOptionFunc func(*exposeConfig)

func (f exposePortOptionFunc) applyExpose(cfg *exposeConfig) {
	f(cfg)
}

// WithExposureTTL sets a relative TTL for one exposed port.
func WithExposureTTL(ttl time.Duration) ExposePortOption {
	return exposePortOptionFunc(func(cfg *exposeConfig) {
		if ttl <= 0 {
			cfg.expiresAt = nil
			return
		}
		expiresAt := time.Now().Add(ttl)
		cfg.expiresAt = &expiresAt
	})
}

func WithSlug(slug string) ExposePortOption {
	return exposePortOptionFunc(func(cfg *exposeConfig) {
		cfg.slug = strings.TrimSpace(slug)
	})
}

// WithOpenCode toggles eager opencode startup during session provisioning.
func WithOpenCode(enabled bool) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.enableOpenCode = enabled
	})
}

// WithCloneRepo configures repo clone during provisioning and enables opencode.
func WithCloneRepo(repoURL string) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.cloneRepoURL = strings.TrimSpace(repoURL)
		if cfg.cloneRepoURL != "" {
			cfg.enableOpenCode = true
		}
	})
}

// CreateOption configures Create behavior.
type CreateOption interface {
	applyCreate(*createConfig)
}

type createOptionFunc func(*createConfig)

func (f createOptionFunc) applyCreate(cfg *createConfig) {
	f(cfg)
}

// CreateVolumeOption configures volume creation behavior.
type CreateVolumeOption interface {
	applyCreateVolume(*createVolumeConfig)
}

type createVolumeOptionFunc func(*createVolumeConfig)

func (f createVolumeOptionFunc) applyCreateVolume(cfg *createVolumeConfig) {
	f(cfg)
}

// TemplateOption configures template create/update behavior.
type TemplateOption interface {
	applyTemplate(*templateConfig)
}

type templateOptionFunc func(*templateConfig)

func (f templateOptionFunc) applyTemplate(cfg *templateConfig) {
	f(cfg)
}

type templateBuildConfig struct {
	publishRawImage   *bool
	buildEnv          map[string]string
	buildSecrets      map[string]string
	waitForCompletion bool
	eventHandler      TemplateBuildEventHandler
}

// WithWaitForCompletion makes BuildTemplate wait for a terminal build.
func WithWaitForCompletion(wait bool) TemplateBuildOption {
	return templateBuildOptionFunc(func(cfg *templateBuildConfig) { cfg.waitForCompletion = wait })
}

// WithBuildEventHandler observes ordered build events while waiting.
func WithBuildEventHandler(handler TemplateBuildEventHandler) TemplateBuildOption {
	return templateBuildOptionFunc(func(cfg *templateBuildConfig) { cfg.eventHandler = handler })
}

// TemplateBuildOption configures one template build.
type TemplateBuildOption interface {
	applyTemplateBuild(*templateBuildConfig)
}

type templateBuildOptionFunc func(*templateBuildConfig)

func (f templateBuildOptionFunc) applyTemplateBuild(cfg *templateBuildConfig) {
	f(cfg)
}

// WithBuildPublishRawImage overrides whether this build publishes the raw
// rootfs disk image alongside the build snapshot. Unset defers to server
// configuration.
func WithBuildPublishRawImage(publish bool) TemplateBuildOption {
	return templateBuildOptionFunc(func(cfg *templateBuildConfig) {
		cfg.publishRawImage = &publish
	})
}

// WithBuildSecrets provides ephemeral environment variables to a template
// build. Values are not persisted on the template or build record.
func WithBuildSecrets(secrets map[string]string) TemplateBuildOption {
	secrets = cloneStringMap(secrets)
	return templateBuildOptionFunc(func(cfg *templateBuildConfig) {
		cfg.buildSecrets = secrets
	})
}

// WithBuildEnv provides request-scoped environment overrides frozen into this
// build without mutating the template.
func WithBuildEnv(env map[string]string) TemplateBuildOption {
	env = cloneStringMap(env)
	return templateBuildOptionFunc(func(cfg *templateBuildConfig) {
		cfg.buildEnv = env
	})
}

// ListOption configures list filtering behavior.
type ListOption interface {
	applyList(*listConfig)
}

type listOptionFunc func(*listConfig)

func (f listOptionFunc) applyList(cfg *listConfig) {
	f(cfg)
}

// PreviewURLOption configures preview URL create and list behavior.
type PreviewURLOption interface {
	applyPreviewURL(*previewURLConfig)
}

// UpdateSessionOption configures Session.Update behavior.
type UpdateSessionOption interface {
	applyUpdateSession(*updateSessionConfig)
}

type updateSessionOptionFunc func(*updateSessionConfig)

func (f updateSessionOptionFunc) applyUpdateSession(cfg *updateSessionConfig) {
	f(cfg)
}

// UpdateSnapshotOption configures Client.UpdateSnapshot behavior.
type UpdateSnapshotOption interface {
	applyUpdateSnapshot(*updateSnapshotConfig)
}

type updateSnapshotOptionFunc func(*updateSnapshotConfig)

func (f updateSnapshotOptionFunc) applyUpdateSnapshot(cfg *updateSnapshotConfig) {
	f(cfg)
}

// UpdateVolumeOption configures Client.UpdateVolume behavior.
type UpdateVolumeOption interface {
	applyUpdateVolume(*updateVolumeConfig)
}

type updateVolumeOptionFunc func(*updateVolumeConfig)

func (f updateVolumeOptionFunc) applyUpdateVolume(cfg *updateVolumeConfig) {
	f(cfg)
}

type workspaceIDOption struct {
	workspaceID string
}

func (o workspaceIDOption) applyCreate(cfg *createConfig) {
	cfg.workspaceID = o.workspaceID
}

func (o workspaceIDOption) applyCreateVolume(cfg *createVolumeConfig) {
	cfg.workspaceID = o.workspaceID
}

func (o workspaceIDOption) applyTemplate(cfg *templateConfig) {
	cfg.workspaceID = o.workspaceID
}

func (o workspaceIDOption) applyList(cfg *listConfig) {
	cfg.workspaceID = o.workspaceID
}

func (o workspaceIDOption) applyPreviewURL(cfg *previewURLConfig) {
	cfg.workspaceID = o.workspaceID
}

// WithVolumeName sets the volume name for CreateVolume.
func WithVolumeName(name string) CreateVolumeOption {
	return createVolumeOptionFunc(func(cfg *createVolumeConfig) {
		cfg.name = strings.TrimSpace(name)
	})
}

// WithVolumeSize sets the volume size in bytes for CreateVolume. Prefer helpers like GB or GiB.
func WithVolumeSize(sizeBytes int64) CreateVolumeOption {
	return createVolumeOptionFunc(func(cfg *createVolumeConfig) {
		cfg.sizeBytes = sizeBytes
	})
}

// WithWorkspaceID explicitly scopes requests for trusted service credentials.
// Workspace API keys infer their scope automatically.
func WithWorkspaceID(workspaceID string) interface {
	CreateOption
	CreateVolumeOption
	TemplateOption
	ListOption
	PreviewURLOption
} {
	return workspaceIDOption{workspaceID: strings.TrimSpace(workspaceID)}
}

// VolumeOption configures per-volume attachment behavior.
type VolumeOption interface {
	applyVolume(*volumeConfig)
}

type volumeOptionFunc func(*volumeConfig)

func (f volumeOptionFunc) applyVolume(cfg *volumeConfig) {
	f(cfg)
}

// WithReadOnly mounts a volume read-only.
func WithReadOnly() VolumeOption {
	return volumeOptionFunc(func(cfg *volumeConfig) {
		cfg.readonly = true
	})
}

// WithVolume attaches a persistent volume during Create.
func WithVolume(volumeID, mountPath string, opts ...VolumeOption) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		volumeCfg := volumeConfig{}
		for _, opt := range opts {
			if opt == nil {
				continue
			}
			opt.applyVolume(&volumeCfg)
		}
		cfg.volumes = append(cfg.volumes, &VolumeMount{
			VolumeID:  strings.TrimSpace(volumeID),
			MountPath: strings.TrimSpace(mountPath),
			ReadOnly:  volumeCfg.readonly,
		})
	})
}

// OpenCodeProviderConfig configures OpenCode provider env vars for Create.
type OpenCodeProviderConfig struct {
	APIKey  string
	BaseURL string
	Npm     string
}

// WithOpenCodeProvider sets OpenCode provider env vars for Create.
func WithOpenCodeProvider(provider OpenCodeProviderConfig) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		if cfg.env == nil {
			cfg.env = map[string]string{}
		}
		if apiKey := strings.TrimSpace(provider.APIKey); apiKey != "" {
			cfg.env[openCodeAPIKeyEnvVar] = apiKey
		}
		if baseURL := strings.TrimSpace(provider.BaseURL); baseURL != "" {
			cfg.env[openCodeBaseURLEnvVar] = baseURL
		}
		if npm := strings.TrimSpace(provider.Npm); npm != "" {
			cfg.env[openCodeNpmEnvVar] = npm
		}
	})
}

// WithGitHubToken sets both GH_TOKEN and GIT_TOKEN for Create.
func WithGitHubToken(token string) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			return
		}
		if cfg.env == nil {
			cfg.env = map[string]string{}
		}
		cfg.env[githubTokenEnvVar] = trimmed
		cfg.env[gitTokenEnvVar] = trimmed
	})
}

// WithAllowInbound sets inbound network policy on Create requests.
func WithAllowInbound(allowInbound bool) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.allowInbound = allowInbound
	})
}

// WithAllowOutbound sets outbound network policy on Create requests.
func WithAllowOutbound(allowOutbound bool) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.allowOutbound = allowOutbound
	})
}

// WithMaxDuration sets max session duration on Create requests.
func WithMaxDuration(maxDuration time.Duration) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.maxDuration = &maxDuration
	})
}

// WithIdleTimeout sets the inactivity window after which a session is auto-paused.
func WithIdleTimeout(idleTimeout time.Duration) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.idleTimeout = &idleTimeout
	})
}

// WithPauseRetention sets how long paused state is retained for the session.
func WithPauseRetention(retention time.Duration) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.pauseRetention = &retention
	})
}

// WithCPUCores sets CPU cores on Create requests.
func WithCPUCores(cpuCores int32) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.cpuCores = &cpuCores
		cfg.cpuCoresSet = true
	})
}

// WithMemoryMB sets memory on Create requests.
func WithMemoryMB(memoryMB int32) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.memoryMB = &memoryMB
		cfg.memoryMBSet = true
	})
}

// WithDiskSizeGB sets ephemeral root disk size on Create requests, in GB.
// Range is 5-100. A Create with no image, snapshot or template spec defaults
// to 5; one with a source inherits that source's disk unless this is set.
func WithDiskSizeGB(gb int) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		value := int32(gb)
		cfg.diskSizeGB = &value
		cfg.diskSizeGBSet = true
	})
}

// WithMetadata sets metadata on Create requests.
func WithMetadata(metadata map[string]string) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.metadata = cloneStringMap(metadata)
	})
}

// WithTags sets session or template tags on create/update requests.
func WithTags(tags ...string) interface {
	CreateOption
	TemplateOption
	UpdateSessionOption
	UpdateSnapshotOption
	UpdateVolumeOption
} {
	normalized := normalizeTagList(tags)
	return struct {
		createOptionFunc
		templateOptionFunc
		updateSessionOptionFunc
		updateSnapshotOptionFunc
		updateVolumeOptionFunc
	}{
		createOptionFunc(func(cfg *createConfig) {
			cfg.tags = append(cfg.tags[:0], normalized...)
		}),
		templateOptionFunc(func(cfg *templateConfig) {
			cfg.tags = append(cfg.tags[:0], normalized...)
			cfg.tagsSet = true
		}),
		updateSessionOptionFunc(func(cfg *updateSessionConfig) {
			cfg.tags = append(cfg.tags[:0], normalized...)
			cfg.tagsSet = true
		}),
		updateSnapshotOptionFunc(func(cfg *updateSnapshotConfig) {
			cfg.tags = append(cfg.tags[:0], normalized...)
			cfg.tagsSet = true
		}),
		updateVolumeOptionFunc(func(cfg *updateVolumeConfig) {
			cfg.tags = append(cfg.tags[:0], normalized...)
			cfg.tagsSet = true
		}),
	}
}

// WithExpiresAt sets snapshot expiration on update requests.
func WithExpiresAt(expiresAt time.Time) UpdateSnapshotOption {
	return updateSnapshotOptionFunc(func(cfg *updateSnapshotConfig) {
		value := expiresAt
		cfg.expiresAt = &value
		cfg.expiresAtSet = true
	})
}

// WithoutExpiresAt clears snapshot expiration on update requests.
func WithoutExpiresAt() UpdateSnapshotOption {
	return updateSnapshotOptionFunc(func(cfg *updateSnapshotConfig) {
		cfg.expiresAt = nil
		cfg.expiresAtSet = true
	})
}

// WithTagFilter sets AND-tag filtering on list requests.
func WithTagFilter(tags ...string) ListOption {
	normalized := normalizeTagList(tags)
	return listOptionFunc(func(cfg *listConfig) {
		cfg.tags = append(cfg.tags[:0], normalized...)
	})
}

// WithSticky marks a session as sticky on Create.
func WithSticky() CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.sticky = true
	})
}

// WithWaitReady controls whether Create waits for the session to be RUNNING and
// exec-ready before returning (default true); pass false to return immediately in CREATING.
func WithWaitReady(wait bool) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.waitReady = wait
	})
}

// WithWaitForRuntime holds Create until an image boot runtime is READY or FAILED.
func WithWaitForRuntime(wait bool) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.waitForRuntime = wait
	})
}

// WithWaitTimeout overrides the readiness wait (default 2h for direct template specs, otherwise 3m).
func WithWaitTimeout(timeout time.Duration) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.waitTimeout = timeout
		cfg.waitTimeoutSet = true
	})
}

// WithSetSticky toggles the sticky flag on Update.
func WithSetSticky(sticky bool) UpdateSessionOption {
	return updateSessionOptionFunc(func(cfg *updateSessionConfig) {
		cfg.sticky = &sticky
	})
}

// WithSetMaxDuration sets the lifetime and requires WithSetSticky.
func WithSetMaxDuration(maxDuration time.Duration) UpdateSessionOption {
	return updateSessionOptionFunc(func(cfg *updateSessionConfig) {
		cfg.maxDuration = &maxDuration
	})
}

// WithStickyFilter filters sessions by sticky flag on List.
func WithStickyFilter(sticky bool) ListOption {
	return listOptionFunc(func(cfg *listConfig) {
		cfg.sticky = &sticky
	})
}

// WithIncludeTerminated includes terminated sessions in List results. By
// default the server omits terminated sessions; set this to surface them (for
// example when filtering by a terminated state).
func WithIncludeTerminated(include bool) ListOption {
	return listOptionFunc(func(cfg *listConfig) {
		cfg.includeTerminated = include
	})
}

// WithSSHKeys overrides session SSH authorized keys at creation time.
func WithSSHKeys(keys []string) CreateOption {
	return createOptionFunc(func(cfg *createConfig) {
		cfg.sshKeys = append([]string(nil), keys...)
	})
}

type execConfig struct {
	args     []string
	timeout  *time.Duration
	env      map[string]string
	dir      string
	onOutput func(Output)
}

func defaultExecConfig() execConfig {
	return execConfig{
		args: []string{},
		env:  map[string]string{},
	}
}

// ExecOption configures Exec behavior.
type ExecOption interface {
	applyExec(*execConfig)
}

type execOptionFunc func(*execConfig)

func (f execOptionFunc) applyExec(cfg *execConfig) {
	f(cfg)
}

// WithArgs sets command args for Exec.
func WithArgs(args ...string) ExecOption {
	return execOptionFunc(func(cfg *execConfig) {
		cfg.args = append([]string(nil), args...)
	})
}

type timeoutOption struct {
	timeout time.Duration
}

func (o timeoutOption) applyExec(cfg *execConfig) {
	cfg.timeout = &o.timeout
}

// WithTimeout sets timeout for Exec.
func WithTimeout(timeout time.Duration) ExecOption {
	return timeoutOption{timeout: timeout}
}

// WithDir sets the command working directory. Relative paths resolve under the
// sandbox guest workdir; absolute paths are used unchanged.
func WithDir(dir string) ExecOption {
	return execOptionFunc(func(cfg *execConfig) {
		cfg.dir = dir
	})
}

type envOption struct {
	env map[string]string
}

func (o envOption) applyCreate(cfg *createConfig) {
	if cfg.env == nil {
		cfg.env = map[string]string{}
	}
	for k, v := range o.env {
		cfg.env[k] = v
	}
}

func (o envOption) applyExec(cfg *execConfig) {
	if cfg.env == nil {
		cfg.env = map[string]string{}
	}
	for k, v := range o.env {
		cfg.env[k] = v
	}
}

func (o envOption) applyTemplate(cfg *templateConfig) {
	cfg.env = cloneStringMap(o.env)
	cfg.envSet = true
}

// WithEnvs sets environment variables for Create (session defaults) or Exec (command overrides).
func WithEnvs(env map[string]string) interface {
	CreateOption
	ExecOption
	TemplateOption
} {
	return envOption{env: cloneStringMap(env)}
}

// WithOnOutput sets a callback invoked for each output chunk during Exec.
// The callback fires as chunks arrive from the server stream, before Exec returns.
// The full Result is still returned with aggregated Stdout/Stderr.
func WithOnOutput(fn func(Output)) ExecOption {
	return execOptionFunc(func(cfg *execConfig) {
		cfg.onOutput = fn
	})
}

// WithEnv sets a single command environment override.
func WithEnv(key, value string) ExecOption {
	return execOptionFunc(func(cfg *execConfig) {
		if cfg.env == nil {
			cfg.env = map[string]string{}
		}
		cfg.env[key] = value
	})
}

// WithTemplateName sets the template name for CreateTemplate/UpdateTemplate.
func WithTemplateName(name string) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.name = strings.TrimSpace(name)
		cfg.nameSet = true
	})
}

// WithSetupScript sets the template setup script for CreateTemplate/UpdateTemplate.
func WithSetupScript(script string) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.setupScript = strings.TrimSpace(script)
		cfg.setupScriptSet = true
	})
}

// WithStartCmd sets the template start command for CreateTemplate/UpdateTemplate.
func WithStartCmd(startCmd string) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.startCmdSet = true
		value := strings.TrimSpace(startCmd)
		cfg.startCmd = &value
	})
}

// WithTemplateResources sets template CPU and memory defaults in MB.
func WithTemplateResources(cpuCores, memoryMB int32) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.resources = &TemplateResources{
			CPUCores: cpuCores,
			MemoryMB: memoryMB,
		}
		cfg.resourcesSet = true
	})
}

// WithTemplateDiskSizeGB sets the template default ephemeral root disk size,
// in GB. Must be 0 or 5-100, where 0 leaves the template without a disk
// default. Sessions created from the template inherit this unless they pass
// their own WithDiskSizeGB.
func WithTemplateDiskSizeGB(gb int32) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		if cfg.resources == nil {
			cfg.resources = &TemplateResources{}
		}
		cfg.resources.DiskSizeGB = gb
		cfg.resourcesSet = true
	})
}

// WithBaseImageID sets the template base image.
func WithBaseImageID(baseImageID string) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.baseImageID = strings.TrimSpace(baseImageID)
		cfg.baseImageIDSet = true
	})
}

// WithParentTemplateID builds a template on top of the current publication of another template.
func WithParentTemplateID(templateID string) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.parentTemplateID = strings.TrimSpace(templateID)
	})
}

// WithParentImage builds a template on top of a pinned registry image snapshot.
func WithParentImage(image string) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.parentImage = strings.TrimSpace(image)
	})
}

// WithTemplateSpec sets the typed build spec for CreateTemplate/UpdateTemplate.
// It cannot be combined with legacy field options; updates replace the spec atomically.
func WithTemplateSpec(spec TemplateSpec) TemplateOption {
	return templateOptionFunc(func(cfg *templateConfig) {
		cfg.spec = &spec
	})
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeTagList(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(strings.ToLower(tag))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
