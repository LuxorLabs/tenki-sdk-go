package sandbox

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type RegistryImageKind string

const (
	RegistryImageKindTemplate RegistryImageKind = "template"
	RegistryImageKindSnapshot RegistryImageKind = "snapshot"
)

type RegistryVisibility string

const (
	RegistryVisibilityPrivate RegistryVisibility = "private"
	RegistryVisibilityPublic  RegistryVisibility = "public"
	RegistryVisibilityShared  RegistryVisibility = "shared"
)

type RegistrySortBy string

const (
	RegistrySortUpdatedAt RegistrySortBy = "updated_at"
	RegistrySortName      RegistrySortBy = "name"
)

type RegistryTag struct {
	ID         string
	ImageID    string
	Tag        string
	SnapshotID string
	Ref        string
	UpdatedAt  time.Time
}

type RegistryImage struct {
	ID                     string
	WorkspaceID            string
	WorkspaceSlug          string
	Name                   string
	Kind                   RegistryImageKind
	Visibility             RegistryVisibility
	Title                  string
	Description            string
	Labels                 []string
	SourceTemplateID       string
	SourceSnapshotID       string
	Tags                   []*RegistryTag
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ChangesNotYetPublished bool
	// Digest and DigestRef pin the exact built output when the image came from
	// a template build (for example "sha256:..." and "acme/api@sha256:...").
	Digest    string
	DigestRef string
}

// Ref returns the untagged image ref that resolves the latest eligible build.
func (i *RegistryImage) Ref() string {
	if i == nil {
		return ""
	}
	if i.WorkspaceSlug != "" && i.Name != "" {
		return i.WorkspaceSlug + "/" + i.Name
	}
	return i.Name
}

// launchRef prefers the immutable digest ref for sandbox creation.
func (i *RegistryImage) launchRef() string {
	if i == nil {
		return ""
	}
	if i.DigestRef != "" {
		return i.DigestRef
	}
	return i.Ref()
}

type RegistryImageSummary struct {
	ID                     string
	WorkspaceID            string
	WorkspaceSlug          string
	Name                   string
	Kind                   RegistryImageKind
	Visibility             RegistryVisibility
	Labels                 []string
	Tags                   []*RegistryTag
	LatestSnapshotID       string
	LatestRef              string
	UpdatedAt              time.Time
	ChangesNotYetPublished bool
}

type RegistryImageDetail struct {
	Image              *RegistryImage
	ResolvedSnapshotID string
	ResolvedRef        string
	WorkspaceActive    bool
	Tombstoned         bool
	MaskedEnvVarKeys   []string
	Metadata           map[string]string
	EnvVars            map[string]string
}

type RegistryShareGrant struct {
	ID                string
	ImageID           string
	OwnerWorkspaceID  string
	TargetWorkspaceID string
	CurrentSnapshotID string
	GrantedViaTokenID string
	AcceptedBy        string
	AcceptedAt        time.Time
	RevokedAt         time.Time
	FollowMode        string
	FollowTag         string
}

type ResolvedRegistryRef struct {
	ImageID             string
	OwningWorkspaceID   string
	OwningWorkspaceSlug string
	ImageName           string
	SnapshotID          string
	ResolvedRef         string
	Kind                RegistryImageKind
	Visibility          RegistryVisibility
}

type RegistryPublishResult struct {
	Image      *RegistryImage
	Tag        *RegistryTag
	SnapshotID string
	DigestRef  string
}

type RegistryListResult struct {
	Images     []*RegistryImageSummary
	NextCursor string
}

type RegistryShareResult struct {
	Image             *RegistryImage
	CurrentSnapshotID string
	DigestRef         string
	Grant             *RegistryShareGrant
}

type RegistryUnshareResult struct {
	Image             *RegistryImage
	RevokedTokenCount int32
	RevokedGrantCount int32
}

type RegistryDeleteResult struct {
	Image             *RegistryImage
	RevokedTokenCount int32
	RevokedGrantCount int32
}

type RegistryVersionDeleteResult struct {
	ImageID    string
	SnapshotID string
}

type RegistryListOption func(*sandboxv1.ListRegistryImagesRequest)

func WithRegistryWorkspace(slug string) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		if slug = strings.TrimSpace(slug); slug != "" {
			req.WorkspaceSlug = &slug
		}
	}
}

func WithRegistryWorkspaceID(workspaceID string) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			req.WorkspaceId = &workspaceID
		}
	}
}

func WithRegistryLabels(labels ...string) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		req.Labels = append(req.Labels, cleanStrings(labels)...)
	}
}

func WithRegistryKind(kind RegistryImageKind) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		req.Kind = registryKindToProto(kind)
	}
}

func WithRegistryNameSubstring(name string) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		if name = strings.TrimSpace(name); name != "" {
			req.NameSubstring = &name
		}
	}
}

func WithRegistrySort(sortBy RegistrySortBy) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		req.SortBy = registrySortToProto(sortBy)
	}
}

func WithRegistryPageSize(size int32) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		if size > 0 {
			req.PageSize = &size
		}
	}
}

func WithRegistryCursor(cursor string) RegistryListOption {
	return func(req *sandboxv1.ListRegistryImagesRequest) {
		if cursor = strings.TrimSpace(cursor); cursor != "" {
			req.Cursor = &cursor
		}
	}
}

type registryLookupOptions struct {
	workspaceID string
}

type RegistryLookupOption func(*registryLookupOptions)

func WithRegistryLookupWorkspaceID(workspaceID string) RegistryLookupOption {
	return func(options *registryLookupOptions) {
		options.workspaceID = strings.TrimSpace(workspaceID)
	}
}

type RegistryPublishOption func(*sandboxv1.PublishRegistryImageRequest)

func WithRegistryImage(image string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		if image = strings.TrimSpace(image); image != "" {
			req.Ref = &image
		}
	}
}

func WithRegistryWorkspaceName(workspaceID string, name string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			req.WorkspaceId = &workspaceID
		}
		if name = strings.TrimSpace(name); name != "" {
			req.Name = &name
		}
	}
}

func WithRegistryTemplate(templateID string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		if templateID = strings.TrimSpace(templateID); templateID != "" {
			req.Kind = sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_TEMPLATE
			req.SourceTemplateId = &templateID
		}
	}
}

func WithRegistrySnapshot(snapshotID string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		if snapshotID = strings.TrimSpace(snapshotID); snapshotID != "" {
			req.Kind = sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_SNAPSHOT
			req.SnapshotId = &snapshotID
		}
	}
}

func WithRegistryTag(tag string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		if tag = strings.TrimSpace(tag); tag != "" {
			req.Tag = &tag
		}
	}
}

func WithRegistryVisibility(visibility RegistryVisibility) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		v := registryVisibilityToProto(visibility)
		if v != sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_UNSPECIFIED {
			req.Visibility = &v
		}
	}
}

func WithRegistryPublishLabels(labels ...string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		req.Labels = append(req.Labels, cleanStrings(labels)...)
	}
}

func WithRegistryTitle(title string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		if title = strings.TrimSpace(title); title != "" {
			req.Title = &title
		}
	}
}

func WithRegistryDescription(description string) RegistryPublishOption {
	return func(req *sandboxv1.PublishRegistryImageRequest) {
		if description = strings.TrimSpace(description); description != "" {
			req.Description = &description
		}
	}
}

type RegistryShareOption func(*sandboxv1.ShareImageRequest)

func WithRegistryShareTag(tag string) RegistryShareOption {
	return func(req *sandboxv1.ShareImageRequest) {
		if tag = strings.TrimSpace(tag); tag != "" {
			req.Tag = &tag
		}
	}
}

func WithRegistryShareSnapshotID(snapshotID string) RegistryShareOption {
	return func(req *sandboxv1.ShareImageRequest) {
		if snapshotID = strings.TrimSpace(snapshotID); snapshotID != "" {
			req.SnapshotId = &snapshotID
		}
	}
}

func (c *Client) ListRegistryImages(ctx context.Context, opts ...RegistryListOption) (*RegistryListResult, error) {
	req := &sandboxv1.ListRegistryImagesRequest{}
	for _, opt := range opts {
		if opt != nil {
			opt(req)
		}
	}
	resp, err := c.sandbox.ListRegistryImages(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]*RegistryImageSummary, 0, len(resp.Msg.Images))
	for _, item := range resp.Msg.Images {
		items = append(items, registrySummaryFromProto(item))
	}
	return &RegistryListResult{Images: items, NextCursor: resp.Msg.NextCursor}, nil
}

func (c *Client) GetRegistryImage(
	ctx context.Context,
	imageOrID string,
	opts ...RegistryLookupOption,
) (*RegistryImageDetail, error) {
	value := strings.TrimSpace(imageOrID)
	req := &sandboxv1.GetRegistryImageRequest{Ref: &value}
	options := registryLookupOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.workspaceID != "" {
		req.WorkspaceId = &options.workspaceID
	}
	if looksLikeUUID(value) {
		req.Ref = nil
		req.ImageId = &value
	}
	resp, err := c.sandbox.GetRegistryImage(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return registryDetailFromProto(resp.Msg.Detail), nil
}

func (c *Client) ResolveRegistryRef(
	ctx context.Context,
	image string,
	opts ...RegistryLookupOption,
) (*ResolvedRegistryRef, error) {
	image = strings.TrimSpace(image)
	options := registryLookupOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	req := &sandboxv1.ResolveRegistryRefRequest{Ref: image}
	if options.workspaceID != "" {
		req.WorkspaceId = &options.workspaceID
	}
	resp, err := c.sandbox.ResolveRegistryRef(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	resolved := resolvedRegistryRefFromProto(resp.Msg.Resolved)
	return resolved, nil
}

func (c *Client) PublishRegistryImage(ctx context.Context, opts ...RegistryPublishOption) (*RegistryPublishResult, error) {
	req := &sandboxv1.PublishRegistryImageRequest{}
	for _, opt := range opts {
		if opt != nil {
			opt(req)
		}
	}
	resp, err := c.sandbox.PublishRegistryImage(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return &RegistryPublishResult{
		Image:      registryImageFromProto(resp.Msg.Image),
		Tag:        registryTagFromProto(resp.Msg.Tag),
		SnapshotID: resp.Msg.SnapshotId,
		DigestRef:  resp.Msg.DigestRef,
	}, nil
}

func (c *Client) UnpublishRegistryImage(ctx context.Context, imageOrID string) (*RegistryImage, error) {
	value := strings.TrimSpace(imageOrID)
	if strings.Contains(value, "/") {
		if slash, colon := strings.LastIndex(value, "/"), strings.LastIndex(value, ":"); colon > slash {
			value = value[:colon]
		}
	}
	req := &sandboxv1.SetRegistryImageVisibilityRequest{
		Ref:        &value,
		Visibility: sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_PRIVATE,
	}
	if looksLikeUUID(value) {
		req.Ref = nil
		req.ImageId = &value
	}
	resp, err := c.sandbox.SetRegistryImageVisibility(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return registryImageFromProto(resp.Msg.Image), nil
}

func (c *Client) ShareImage(ctx context.Context, imageOrID string, targetWorkspaceID string, opts ...RegistryShareOption) (*RegistryShareResult, error) {
	req := &sandboxv1.ShareImageRequest{TargetWorkspaceId: strings.TrimSpace(targetWorkspaceID)}
	if value := strings.TrimSpace(imageOrID); looksLikeUUID(value) {
		req.ImageId = &value
	} else if value != "" {
		req.ImageRef = &value
	}
	for _, opt := range opts {
		if opt != nil {
			opt(req)
		}
	}
	resp, err := c.sandbox.ShareImage(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return &RegistryShareResult{
		Image:             registryImageFromProto(resp.Msg.Image),
		CurrentSnapshotID: resp.Msg.CurrentSnapshotId,
		DigestRef:         resp.Msg.DigestRef,
		Grant:             registryShareGrantFromProto(resp.Msg.Grant),
	}, nil
}

func (c *Client) RevokeRegistryShareGrant(ctx context.Context, grantID, reason string) (*RegistryShareGrant, error) {
	req := &sandboxv1.RevokeRegistryShareGrantRequest{GrantId: strings.TrimSpace(grantID)}
	if reason = strings.TrimSpace(reason); reason != "" {
		req.Reason = &reason
	}
	resp, err := c.sandbox.RevokeRegistryShareGrant(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return registryShareGrantFromProto(resp.Msg.Grant), nil
}

func (c *Client) ListRegistryShareGrants(ctx context.Context, imageOrID string) ([]*RegistryShareGrant, error) {
	req := &sandboxv1.ListRegistryShareGrantsRequest{}
	if value := strings.TrimSpace(imageOrID); looksLikeUUID(value) {
		req.ImageId = &value
	} else {
		req.Ref = &value
	}
	resp, err := c.sandbox.ListRegistryShareGrants(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*RegistryShareGrant, 0, len(resp.Msg.Grants))
	for _, grant := range resp.Msg.Grants {
		out = append(out, registryShareGrantFromProto(grant))
	}
	return out, nil
}

func (c *Client) DeleteRegistryImage(ctx context.Context, imageOrID, reason string) (*RegistryDeleteResult, error) {
	req := &sandboxv1.DeleteRegistryImageRequest{}
	if value := strings.TrimSpace(imageOrID); looksLikeUUID(value) {
		req.ImageId = &value
	} else {
		req.Ref = &value
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		req.Reason = &reason
	}
	resp, err := c.sandbox.DeleteRegistryImage(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return &RegistryDeleteResult{
		Image:             registryImageFromProto(resp.Msg.Image),
		RevokedTokenCount: resp.Msg.RevokedTokenCount,
		RevokedGrantCount: resp.Msg.RevokedGrantCount,
	}, nil
}

func (c *Client) DeleteRegistryImageVersion(ctx context.Context, imageID, snapshotID string) (*RegistryVersionDeleteResult, error) {
	resp, err := c.sandbox.DeleteRegistryImageVersion(ctx, connect.NewRequest(&sandboxv1.DeleteRegistryImageVersionRequest{
		ImageId:    strings.TrimSpace(imageID),
		SnapshotId: strings.TrimSpace(snapshotID),
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return &RegistryVersionDeleteResult{
		ImageID:    resp.Msg.ImageId,
		SnapshotID: resp.Msg.SnapshotId,
	}, nil
}

func (c *Client) UnshareRegistryImage(ctx context.Context, imageOrID, reason string) (*RegistryUnshareResult, error) {
	req := &sandboxv1.UnshareRegistryImageRequest{}
	if value := strings.TrimSpace(imageOrID); looksLikeUUID(value) {
		req.ImageId = &value
	} else {
		req.Ref = &value
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		req.Reason = &reason
	}
	resp, err := c.sandbox.UnshareRegistryImage(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return &RegistryUnshareResult{
		Image:             registryImageFromProto(resp.Msg.Image),
		RevokedTokenCount: resp.Msg.RevokedTokenCount,
		RevokedGrantCount: resp.Msg.RevokedGrantCount,
	}, nil
}

func registryImageFromProto(in *sandboxv1.RegistryImage) *RegistryImage {
	if in == nil {
		return nil
	}
	return &RegistryImage{
		ID:                     in.Id,
		WorkspaceID:            in.WorkspaceId,
		WorkspaceSlug:          in.WorkspaceSlug,
		Name:                   in.Name,
		Kind:                   registryKindFromProto(in.Kind),
		Visibility:             registryVisibilityFromProto(in.Visibility),
		Title:                  in.GetTitle(),
		Description:            in.GetDescription(),
		Labels:                 append([]string{}, in.Labels...),
		SourceTemplateID:       in.GetSourceTemplateId(),
		SourceSnapshotID:       in.GetSourceSnapshotId(),
		Tags:                   registryTagsFromProto(in.Tags),
		CreatedAt:              timestampToTime(in.CreatedAt),
		UpdatedAt:              timestampToTime(in.UpdatedAt),
		ChangesNotYetPublished: in.ChangesNotYetPublished,
		Digest:                 in.GetDigest(),
		DigestRef:              in.GetDigestRef(),
	}
}

func registrySummaryFromProto(in *sandboxv1.RegistryImageSummary) *RegistryImageSummary {
	if in == nil {
		return nil
	}
	return &RegistryImageSummary{
		ID:                     in.Id,
		WorkspaceID:            in.WorkspaceId,
		WorkspaceSlug:          in.WorkspaceSlug,
		Name:                   in.Name,
		Kind:                   registryKindFromProto(in.Kind),
		Visibility:             registryVisibilityFromProto(in.Visibility),
		Labels:                 append([]string{}, in.Labels...),
		Tags:                   registryTagsFromProto(in.Tags),
		LatestSnapshotID:       in.GetLatestSnapshotId(),
		LatestRef:              in.LatestRef,
		UpdatedAt:              timestampToTime(in.UpdatedAt),
		ChangesNotYetPublished: in.ChangesNotYetPublished,
	}
}

func registryDetailFromProto(in *sandboxv1.RegistryImageDetail) *RegistryImageDetail {
	if in == nil {
		return nil
	}
	return &RegistryImageDetail{
		Image:              registryImageFromProto(in.Image),
		ResolvedSnapshotID: in.GetResolvedSnapshotId(),
		ResolvedRef:        in.GetResolvedRef(),
		WorkspaceActive:    in.WorkspaceActive,
		Tombstoned:         in.Tombstoned,
		MaskedEnvVarKeys:   append([]string{}, in.MaskedEnvVarKeys...),
		Metadata:           cloneMap(in.Metadata),
		EnvVars:            cloneMap(in.EnvVars),
	}
}

func registryTagsFromProto(in []*sandboxv1.RegistryTag) []*RegistryTag {
	out := make([]*RegistryTag, 0, len(in))
	for _, tag := range in {
		out = append(out, registryTagFromProto(tag))
	}
	return out
}

func registryTagFromProto(in *sandboxv1.RegistryTag) *RegistryTag {
	if in == nil {
		return nil
	}
	return &RegistryTag{
		ID:         in.Id,
		ImageID:    in.ImageId,
		Tag:        in.Tag,
		SnapshotID: in.SnapshotId,
		Ref:        in.Ref,
		UpdatedAt:  timestampToTime(in.UpdatedAt),
	}
}

func registryShareGrantFromProto(in *sandboxv1.RegistryShareGrant) *RegistryShareGrant {
	if in == nil {
		return nil
	}
	return &RegistryShareGrant{
		ID:                in.Id,
		ImageID:           in.ImageId,
		OwnerWorkspaceID:  in.OwnerWorkspaceId,
		TargetWorkspaceID: in.TargetWorkspaceId,
		CurrentSnapshotID: in.CurrentSnapshotId,
		GrantedViaTokenID: in.GetGrantedViaTokenId(),
		AcceptedBy:        in.GetAcceptedBy(),
		AcceptedAt:        timestampToTime(in.AcceptedAt),
		RevokedAt:         timestampToTime(in.RevokedAt),
		FollowMode:        in.FollowMode,
		FollowTag:         in.GetFollowTag(),
	}
}

func resolvedRegistryRefFromProto(in *sandboxv1.ResolvedRegistryRef) *ResolvedRegistryRef {
	if in == nil {
		return nil
	}
	return &ResolvedRegistryRef{
		ImageID:             in.ImageId,
		OwningWorkspaceID:   in.OwningWorkspaceId,
		OwningWorkspaceSlug: in.OwningWorkspaceSlug,
		ImageName:           in.ImageName,
		SnapshotID:          in.GetSnapshotId(),
		ResolvedRef:         registryResolvedImage(in),
		Kind:                registryKindFromProto(in.Kind),
		Visibility:          registryVisibilityFromProto(in.Visibility),
	}
}

func registryResolvedImage(in *sandboxv1.ResolvedRegistryRef) string {
	base := in.OwningWorkspaceSlug + "/" + in.ImageName
	if tag := in.GetTag(); tag != "" {
		return base + ":" + tag
	}
	if snapshotID := in.GetSnapshotId(); snapshotID != "" {
		return base + "@" + snapshotID
	}
	return base
}

func registryKindToProto(kind RegistryImageKind) *sandboxv1.RegistryImageKind {
	var out sandboxv1.RegistryImageKind
	switch kind {
	case RegistryImageKindTemplate:
		out = sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_TEMPLATE
	case RegistryImageKindSnapshot:
		out = sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_SNAPSHOT
	default:
		return nil
	}
	return &out
}

func registryKindFromProto(kind sandboxv1.RegistryImageKind) RegistryImageKind {
	switch kind {
	case sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_TEMPLATE:
		return RegistryImageKindTemplate
	case sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_SNAPSHOT:
		return RegistryImageKindSnapshot
	default:
		return ""
	}
}

func registryVisibilityToProto(visibility RegistryVisibility) sandboxv1.RegistryVisibility {
	switch visibility {
	case RegistryVisibilityPrivate:
		return sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_PRIVATE
	case RegistryVisibilityPublic:
		return sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_PUBLIC
	case RegistryVisibilityShared:
		return sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_SHARED
	default:
		return sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_UNSPECIFIED
	}
}

func registryVisibilityFromProto(visibility sandboxv1.RegistryVisibility) RegistryVisibility {
	switch visibility {
	case sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_PRIVATE:
		return RegistryVisibilityPrivate
	case sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_PUBLIC:
		return RegistryVisibilityPublic
	case sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_SHARED:
		return RegistryVisibilityShared
	default:
		return ""
	}
}

func registrySortToProto(sortBy RegistrySortBy) sandboxv1.RegistrySortBy {
	if sortBy == RegistrySortName {
		return sandboxv1.RegistrySortBy_REGISTRY_SORT_BY_NAME
	}
	return sandboxv1.RegistrySortBy_REGISTRY_SORT_BY_UPDATED_AT
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func looksLikeUUID(value string) bool {
	return len(value) == 36 && strings.Count(value, "-") == 4
}

func timestampToTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}
