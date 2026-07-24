package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var errTemplateSpecLegacyConflict = errors.New("sandbox: WithTemplateSpec cannot be combined with legacy template field options")

var templateBuildPollInterval = 2 * time.Second

// TemplateBuildState mirrors template build lifecycle states from the service contract.
type TemplateBuildState string

const (
	TemplateBuildStateUnspecified TemplateBuildState = "UNSPECIFIED"
	TemplateBuildStatePending     TemplateBuildState = "PENDING"
	TemplateBuildStateBuilding    TemplateBuildState = "BUILDING"
	TemplateBuildStateReady       TemplateBuildState = "READY"
	TemplateBuildStateFailed      TemplateBuildState = "FAILED"
)

// TemplateVisibility mirrors template visibility states from the service contract.
type TemplateVisibility string

const (
	TemplateVisibilityUnspecified TemplateVisibility = "UNSPECIFIED"
	TemplateVisibilityPrivate     TemplateVisibility = "PRIVATE"
	TemplateVisibilityPublic      TemplateVisibility = "PUBLIC"
)

// TemplateDefinitionMode identifies how a template definition is stored.
type TemplateDefinitionMode string

const (
	TemplateDefinitionModeUnspecified TemplateDefinitionMode = "UNSPECIFIED"
	TemplateDefinitionModeLegacy      TemplateDefinitionMode = "LEGACY"
	TemplateDefinitionModeTyped       TemplateDefinitionMode = "TYPED"
)

// TemplateResources is the SDK view of template CPU/memory defaults.
type TemplateResources struct {
	CPUCores   int32
	MemoryMB   int32
	DiskSizeGB int32
}

// TemplateBuild is the SDK view of one template build.
type TemplateBuild struct {
	ID                 string
	TemplateID         string
	State              TemplateBuildState
	Version            int32
	Error              string
	BuildLogTail       string
	BuildLogTruncated  bool
	BuildLogArtifactID string
	StartedAt          time.Time
	CompletedAt        time.Time
	SnapshotID         string
	SessionID          string
	// Spec is the immutable normalized spec frozen at submission; SpecHash is
	// its server-computed canonical hash.
	Spec     *TemplateSpec
	SpecHash string
	// Image is the private registry image registered by a successful build;
	// ImageDigest/ImageDigestRef pin the exact built output.
	Image          *RegistryImage
	ImageDigest    string
	ImageDigestRef string
	Events         []TemplateBuildEvent
	Provenance     *TemplateBuildProvenance
	Failure        *TemplateBuildFailure
}

type TemplateBuildStepReference struct {
	Index int32
	Kind  string
	Name  string
	Label string
}

type TemplateBuildLogEvent struct {
	Timestamp time.Time
	Phase     string
	Step      *TemplateBuildStepReference
	Stream    string
	Data      string
}

type TemplateBuildProgressEvent struct {
	Timestamp time.Time
	Phase     string
	Step      *TemplateBuildStepReference
	State     string
	Message   string
}

type TemplateBuildEvent struct {
	Log      *TemplateBuildLogEvent
	Progress *TemplateBuildProgressEvent
}

type TemplateBuildProvenance struct {
	RequestedGitRef      string
	ResolvedGitCommitSHA string
	BuildSecretKeys      []string
}

type TemplateBuildFailure struct {
	Code    string
	Message string
	Step    *TemplateBuildStepReference
}

type TemplateBuildEventHandler func(TemplateBuildEvent) error

type TemplateBuildFailedError struct {
	Build *TemplateBuild
}

func (e *TemplateBuildFailedError) Error() string {
	if e.Build != nil {
		if e.Build.Failure != nil && e.Build.Failure.Message != "" {
			return "sandbox: template build failed: " + e.Build.Failure.Message
		}
		if e.Build.Error != "" {
			return "sandbox: template build failed: " + e.Build.Error
		}
	}
	return ErrTemplateBuildFailed.Error()
}

func (e *TemplateBuildFailedError) Unwrap() error { return ErrTemplateBuildFailed }

// Template is the SDK view of one sandbox template.
type Template struct {
	ID                string
	WorkspaceID       string
	OwnerType         string
	OwnerID           string
	Name              string
	BaseImageID       string
	SetupScript       string
	StartCmd          string
	EnvVars           map[string]string
	Tags              []string
	Resources         *TemplateResources
	LatestBuild       *TemplateBuild
	Visibility        TemplateVisibility
	DefinitionMode    TemplateDefinitionMode
	ParentWorkspaceID string
	// Spec is the normalized build spec for both legacy and typed templates;
	// SpecHash is its server-computed canonical hash.
	Spec      *TemplateSpec
	SpecHash  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateTemplate creates one sandbox template.
func (c *Client) CreateTemplate(ctx context.Context, opts ...TemplateOption) (*Template, error) {
	cfg := defaultTemplateConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyTemplate(&cfg)
	}
	if cfg.resourcesSet {
		if err := validateTemplateResources(cfg.resources); err != nil {
			return nil, err
		}
	}
	if cfg.spec != nil {
		if cfg.baseImageIDSet || cfg.setupScriptSet || cfg.startCmdSet || cfg.envSet || cfg.resourcesSet ||
			strings.TrimSpace(cfg.parentTemplateID) != "" || strings.TrimSpace(cfg.parentImage) != "" {
			return nil, errTemplateSpecLegacyConflict
		}
		if err := cfg.spec.Validate(); err != nil {
			return nil, err
		}
	}
	req := &sandboxv1.CreateTemplateRequest{
		Name:        cfg.name,
		BaseImageId: cfg.baseImageID,
		SetupScript: cfg.setupScript,
		EnvVars:     cloneStringMap(cfg.env),
		Tags:        append([]string(nil), cfg.tags...),
	}
	if workspaceID := strings.TrimSpace(cfg.workspaceID); workspaceID != "" {
		req.WorkspaceId = workspaceID
	}
	if parentTemplateID := strings.TrimSpace(cfg.parentTemplateID); parentTemplateID != "" {
		req.ParentTemplateId = &parentTemplateID
	}
	if parentImage := strings.TrimSpace(cfg.parentImage); parentImage != "" {
		req.ParentImage = &parentImage
	}
	if cfg.startCmdSet {
		req.StartCmd = cfg.startCmd
	}
	if cfg.resourcesSet {
		req.Resources = templateResourcesToProto(cfg.resources)
	}
	if cfg.spec != nil {
		req.BuilderSpec = cfg.spec.toProto()
	}

	resp, err := c.sandbox.CreateTemplate(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapTemplateSpecError(err)
	}
	return templateFromProto(resp.Msg.Template), nil
}

// templateRefID resolves a template resource object or raw ID string.
func templateRefID(template any) (string, error) {
	switch value := template.(type) {
	case *Template:
		if value == nil || value.ID == "" {
			return "", errors.New("sandbox: template reference has no ID")
		}
		return value.ID, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return "", errors.New("sandbox: template ID is required")
		}
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("sandbox: template reference must be *Template or string ID, got %T", template)
	}
}

// templateBuildRefID resolves a build resource object or raw ID string.
func templateBuildRefID(build any) (string, error) {
	switch value := build.(type) {
	case *TemplateBuild:
		if value == nil || value.ID == "" {
			return "", errors.New("sandbox: template build reference has no ID")
		}
		return value.ID, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return "", errors.New("sandbox: template build ID is required")
		}
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("sandbox: template build reference must be *TemplateBuild or string ID, got %T", build)
	}
}

// BuildTemplate starts one template build. It accepts a *Template resource
// object or a raw template ID string.
func (c *Client) BuildTemplate(ctx context.Context, template any, opts ...TemplateBuildOption) (*TemplateBuild, error) {
	templateID, err := templateRefID(template)
	if err != nil {
		return nil, err
	}
	var cfg templateBuildConfig
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyTemplateBuild(&cfg)
	}

	req := &sandboxv1.BuildTemplateRequest{
		TemplateId:      templateID,
		PublishRawImage: cfg.publishRawImage,
		BuildEnv:        cloneStringMap(cfg.buildEnv),
		BuildSecrets:    cloneStringMap(cfg.buildSecrets),
	}

	resp, err := c.sandbox.BuildTemplate(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapTemplateSpecError(err)
	}
	build := templateBuildFromProto(resp.Msg.Build)
	if cfg.waitForCompletion {
		return c.waitForTemplateBuild(ctx, build.ID, cfg.eventHandler)
	}
	return build, nil
}

// WaitForTemplateBuild polls a build (*TemplateBuild or ID string) until READY
// or FAILED; context cancellation stops local observation only, never the remote build.
func (c *Client) WaitForTemplateBuild(ctx context.Context, build any) (*TemplateBuild, error) {
	buildID, err := templateBuildRefID(build)
	if err != nil {
		return nil, err
	}
	return c.waitForTemplateBuild(ctx, buildID, nil)
}

// WaitForTemplateBuildWithEvents observes ordered events while waiting. It
// reconnects to an existing build, replaying events in order exactly once.
func (c *Client) WaitForTemplateBuildWithEvents(ctx context.Context, build any, handler TemplateBuildEventHandler) (*TemplateBuild, error) {
	buildID, err := templateBuildRefID(build)
	if err != nil {
		return nil, err
	}
	return c.waitForTemplateBuild(ctx, buildID, handler)
}

// CancelTemplateBuild explicitly cancels one remote build (*TemplateBuild or
// ID string) and returns the terminal build.
func (c *Client) CancelTemplateBuild(ctx context.Context, build any) (*TemplateBuild, error) {
	buildID, err := templateBuildRefID(build)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.CancelTemplateBuild(ctx, connect.NewRequest(&sandboxv1.CancelTemplateBuildRequest{
		BuildId: buildID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return templateBuildFromProto(resp.Msg.Build), nil
}

func (c *Client) waitForTemplateBuild(ctx context.Context, buildID string, handler TemplateBuildEventHandler) (*TemplateBuild, error) {
	seenEvents := 0
	for {
		build, err := c.GetTemplateBuild(ctx, buildID)
		if err != nil {
			return nil, err
		}
		if handler != nil {
			for seenEvents < len(build.Events) {
				if err := handler(build.Events[seenEvents]); err != nil {
					return nil, err
				}
				seenEvents++
			}
		}
		switch build.State {
		case TemplateBuildStateReady:
			return build, nil
		case TemplateBuildStateFailed:
			return build, &TemplateBuildFailedError{Build: build}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(templateBuildPollInterval):
		}
	}
}

// GetTemplate fetches one template (*Template or ID string).
func (c *Client) GetTemplate(ctx context.Context, template any) (*Template, error) {
	templateID, err := templateRefID(template)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.GetTemplate(ctx, connect.NewRequest(&sandboxv1.GetTemplateRequest{
		TemplateId: templateID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return templateFromProto(resp.Msg.Template), nil
}

// ListTemplates lists templates owned by the Workspace API key.
func (c *Client) ListTemplates(ctx context.Context, opts ...ListOption) ([]*Template, error) {
	cfg := defaultListConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyList(&cfg)
	}
	var templates []*Template
	var pageToken string
	for {
		resp, err := c.sandbox.ListTemplates(ctx, connect.NewRequest(&sandboxv1.ListTemplatesRequest{
			WorkspaceId: cfg.workspaceID,
			PageSize:    automaticPageSize,
			PageToken:   pageToken,
			Tags:        append([]string(nil), cfg.tags...),
		}))
		if err != nil {
			return nil, mapError(err)
		}
		templates = append(templates, templatesFromProto(resp.Msg.Templates)...)
		pageToken, err = advancePageToken(pageToken, resp.Msg.NextPageToken)
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			return templates, nil
		}
	}
}

// UpdateTemplate updates one template (*Template or ID string). Metadata is
// patchable; a typed spec passed via WithTemplateSpec replaces the whole spec atomically.
func (c *Client) UpdateTemplate(ctx context.Context, template any, opts ...TemplateOption) (*Template, error) {
	templateID, err := templateRefID(template)
	if err != nil {
		return nil, err
	}
	cfg := defaultTemplateConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.applyTemplate(&cfg)
	}
	if cfg.resourcesSet {
		if err := validateTemplateResources(cfg.resources); err != nil {
			return nil, err
		}
	}
	if cfg.spec != nil {
		if cfg.baseImageIDSet || cfg.setupScriptSet || cfg.startCmdSet || cfg.envSet || cfg.resourcesSet {
			return nil, errTemplateSpecLegacyConflict
		}
		if err := cfg.spec.Validate(); err != nil {
			return nil, err
		}
	}

	req := &sandboxv1.UpdateTemplateRequest{
		TemplateId: templateID,
	}
	if cfg.nameSet {
		req.Name = &cfg.name
	}
	if cfg.baseImageIDSet {
		req.BaseImageId = &cfg.baseImageID
	}
	if cfg.setupScriptSet {
		req.SetupScript = &cfg.setupScript
	}
	if cfg.startCmdSet {
		req.StartCmd = cfg.startCmd
	}
	if cfg.envSet {
		req.EnvVars = cloneStringMap(cfg.env)
	}
	if cfg.tagsSet {
		if len(cfg.tags) == 0 {
			req.ClearTags = true
		} else {
			req.Tags = append([]string(nil), cfg.tags...)
		}
	}
	if cfg.resourcesSet {
		req.Resources = templateResourcesToProto(cfg.resources)
	}
	if cfg.spec != nil {
		req.BuilderSpec = cfg.spec.toProto()
	}

	resp, err := c.sandbox.UpdateTemplate(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapTemplateSpecError(err)
	}
	return templateFromProto(resp.Msg.Template), nil
}

// DeleteTemplate deletes one template (*Template or ID string). Built images
// and tags stay launchable until deleted from the registry.
func (c *Client) DeleteTemplate(ctx context.Context, template any) (*Template, error) {
	templateID, err := templateRefID(template)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.DeleteTemplate(ctx, connect.NewRequest(&sandboxv1.DeleteTemplateRequest{
		TemplateId: templateID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return templateFromProto(resp.Msg.Template), nil
}

// GetTemplateBuild fetches one template build (*TemplateBuild or ID string).
func (c *Client) GetTemplateBuild(ctx context.Context, build any) (*TemplateBuild, error) {
	buildID, err := templateBuildRefID(build)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.GetTemplateBuild(ctx, connect.NewRequest(&sandboxv1.GetTemplateBuildRequest{
		BuildId: buildID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return templateBuildFromProto(resp.Msg.Build), nil
}

// ListActiveTemplateBuilds lists pending/building executions for a template
// (*Template or ID string), newest template-local build number first.
func (c *Client) ListActiveTemplateBuilds(ctx context.Context, template any) ([]*TemplateBuild, error) {
	templateID, err := templateRefID(template)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.ListActiveTemplateBuilds(ctx, connect.NewRequest(&sandboxv1.ListActiveTemplateBuildsRequest{
		TemplateId: templateID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	builds := make([]*TemplateBuild, 0, len(resp.Msg.Builds))
	for _, build := range resp.Msg.Builds {
		builds = append(builds, templateBuildFromProto(build))
	}
	return builds, nil
}

// IsReady returns true if the build snapshot is ready for restore.
func (s TemplateBuildState) IsReady() bool {
	return s == TemplateBuildStateReady
}

// IsTerminal returns true if the build has reached a final state.
func (s TemplateBuildState) IsTerminal() bool {
	return s == TemplateBuildStateReady || s == TemplateBuildStateFailed
}

func templateResourcesToProto(resources *TemplateResources) *sandboxv1.TemplateResources {
	if resources == nil {
		return nil
	}
	return &sandboxv1.TemplateResources{
		CpuCores:   resources.CPUCores,
		MemoryMb:   resources.MemoryMB,
		DiskSizeGb: resources.DiskSizeGB,
	}
}

func templatesFromProto(protoTemplates []*sandboxv1.Template) []*Template {
	templates := make([]*Template, 0, len(protoTemplates))
	for _, protoTemplate := range protoTemplates {
		templates = append(templates, templateFromProto(protoTemplate))
	}
	return templates
}

func templateFromProto(protoTemplate *sandboxv1.Template) *Template {
	if protoTemplate == nil {
		return nil
	}

	template := &Template{
		ID:          protoTemplate.Id,
		WorkspaceID: protoTemplate.WorkspaceId,
		OwnerType:   protoTemplate.OwnerType,
		OwnerID:     protoTemplate.OwnerId,
		Name:        protoTemplate.Name,
		BaseImageID: protoTemplate.BaseImageId,
		SetupScript: protoTemplate.SetupScript,
		Tags:        append([]string(nil), protoTemplate.Tags...),
		EnvVars:     cloneStringMap(protoTemplate.EnvVars),
		LatestBuild: templateBuildFromProto(protoTemplate.LatestBuild),
		Visibility:  templateVisibilityFromProto(protoTemplate.Visibility),
		DefinitionMode: templateDefinitionModeFromProto(
			protoTemplate.DefinitionMode,
		),
	}
	if protoTemplate.ParentWorkspaceId != nil {
		template.ParentWorkspaceID = protoTemplate.GetParentWorkspaceId()
	}
	template.Spec = templateSpecFromProto(protoTemplate.BuilderSpec)
	template.SpecHash = protoTemplate.GetSpecHash()
	if protoTemplate.StartCmd != nil {
		template.StartCmd = protoTemplate.GetStartCmd()
	}
	if protoTemplate.Resources != nil {
		template.Resources = &TemplateResources{
			CPUCores:   protoTemplate.Resources.CpuCores,
			MemoryMB:   protoTemplate.Resources.MemoryMb,
			DiskSizeGB: protoTemplate.Resources.DiskSizeGb,
		}
	}
	if protoTemplate.CreatedAt != nil {
		template.CreatedAt = protoTemplate.CreatedAt.AsTime()
	}
	if protoTemplate.UpdatedAt != nil {
		template.UpdatedAt = protoTemplate.UpdatedAt.AsTime()
	}
	return template
}

func templateBuildFromProto(protoBuild *sandboxv1.TemplateBuild) *TemplateBuild {
	if protoBuild == nil {
		return nil
	}

	build := &TemplateBuild{
		ID:                protoBuild.Id,
		TemplateID:        protoBuild.TemplateId,
		State:             templateBuildStateFromProto(protoBuild.State),
		Version:           protoBuild.Version,
		BuildLogTruncated: protoBuild.BuildLogTruncated,
	}
	if protoBuild.Error != nil {
		build.Error = protoBuild.GetError()
	}
	if protoBuild.BuildLogTail != nil {
		build.BuildLogTail = protoBuild.GetBuildLogTail()
	}
	if protoBuild.BuildLogArtifactId != nil {
		build.BuildLogArtifactID = protoBuild.GetBuildLogArtifactId()
	}
	if protoBuild.StartedAt != nil {
		build.StartedAt = protoBuild.StartedAt.AsTime()
	}
	if protoBuild.CompletedAt != nil {
		build.CompletedAt = protoBuild.CompletedAt.AsTime()
	}
	if protoBuild.SnapshotId != nil {
		build.SnapshotID = protoBuild.GetSnapshotId()
	}
	if protoBuild.SessionId != nil {
		build.SessionID = protoBuild.GetSessionId()
	}
	build.Spec = templateSpecFromProto(protoBuild.BuilderSpec)
	build.SpecHash = protoBuild.GetSpecHash()
	build.Image = registryImageFromProto(protoBuild.Image)
	build.ImageDigest = protoBuild.GetImageDigest()
	build.ImageDigestRef = protoBuild.GetImageDigestRef()
	build.Events = make([]TemplateBuildEvent, 0, len(protoBuild.Events))
	for _, event := range protoBuild.Events {
		if logEvent := event.GetLog(); logEvent != nil {
			build.Events = append(build.Events, TemplateBuildEvent{Log: &TemplateBuildLogEvent{
				Timestamp: protoTimestamp(logEvent.Timestamp), Phase: logEvent.Phase, Step: templateBuildStepFromProto(logEvent.Step),
				Stream: templateBuildLogStreamFromProto(logEvent.Stream), Data: logEvent.Data,
			}})
		} else if progress := event.GetProgress(); progress != nil {
			build.Events = append(build.Events, TemplateBuildEvent{Progress: &TemplateBuildProgressEvent{
				Timestamp: protoTimestamp(progress.Timestamp), Phase: progress.Phase, Step: templateBuildStepFromProto(progress.Step),
				State: templateBuildProgressStateFromProto(progress.State), Message: progress.GetMessage(),
			}})
		}
	}
	if provenance := protoBuild.Provenance; provenance != nil {
		build.Provenance = &TemplateBuildProvenance{
			RequestedGitRef:      provenance.GetRequestedGitRef(),
			ResolvedGitCommitSHA: provenance.GetResolvedGitCommitSha(),
			BuildSecretKeys:      append([]string(nil), provenance.GetBuildSecretKeys()...),
		}
	}
	if failure := protoBuild.Failure; failure != nil {
		build.Failure = &TemplateBuildFailure{Code: failure.Code, Message: failure.Message, Step: templateBuildStepFromProto(failure.Step)}
	}
	return build
}

func protoTimestamp(value *timestamppb.Timestamp) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.AsTime()
}

func templateBuildStepFromProto(step *sandboxv1.TemplateBuildStepReference) *TemplateBuildStepReference {
	if step == nil {
		return nil
	}
	return &TemplateBuildStepReference{Index: step.Index, Kind: step.Kind, Name: step.GetName(), Label: step.Label}
}

func templateBuildLogStreamFromProto(stream sandboxv1.TemplateBuildLogStream) string {
	switch stream {
	case sandboxv1.TemplateBuildLogStream_TEMPLATE_BUILD_LOG_STREAM_STDOUT:
		return "stdout"
	case sandboxv1.TemplateBuildLogStream_TEMPLATE_BUILD_LOG_STREAM_STDERR:
		return "stderr"
	default:
		return "system"
	}
}

func templateBuildProgressStateFromProto(state sandboxv1.TemplateBuildProgressState) string {
	switch state {
	case sandboxv1.TemplateBuildProgressState_TEMPLATE_BUILD_PROGRESS_STATE_STARTED:
		return "started"
	case sandboxv1.TemplateBuildProgressState_TEMPLATE_BUILD_PROGRESS_STATE_COMPLETED:
		return "completed"
	case sandboxv1.TemplateBuildProgressState_TEMPLATE_BUILD_PROGRESS_STATE_FAILED:
		return "failed"
	default:
		return "unspecified"
	}
}

func templateBuildStateFromProto(state sandboxv1.TemplateBuildState) TemplateBuildState {
	switch state {
	case sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_PENDING:
		return TemplateBuildStatePending
	case sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_BUILDING:
		return TemplateBuildStateBuilding
	case sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_READY:
		return TemplateBuildStateReady
	case sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_FAILED:
		return TemplateBuildStateFailed
	default:
		return TemplateBuildStateUnspecified
	}
}

func templateVisibilityFromProto(visibility sandboxv1.TemplateVisibility) TemplateVisibility {
	switch visibility {
	case sandboxv1.TemplateVisibility_TEMPLATE_VISIBILITY_PRIVATE:
		return TemplateVisibilityPrivate
	case sandboxv1.TemplateVisibility_TEMPLATE_VISIBILITY_PUBLIC:
		return TemplateVisibilityPublic
	default:
		return TemplateVisibilityUnspecified
	}
}

func templateDefinitionModeFromProto(mode sandboxv1.TemplateDefinitionMode) TemplateDefinitionMode {
	switch mode {
	case sandboxv1.TemplateDefinitionMode_TEMPLATE_DEFINITION_MODE_LEGACY:
		return TemplateDefinitionModeLegacy
	case sandboxv1.TemplateDefinitionMode_TEMPLATE_DEFINITION_MODE_TYPED:
		return TemplateDefinitionModeTyped
	default:
		return TemplateDefinitionModeUnspecified
	}
}
