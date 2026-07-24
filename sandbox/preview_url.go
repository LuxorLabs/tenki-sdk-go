package sandbox

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type PreviewURL struct {
	ID             string
	WorkspaceID    string
	OwnerID        string
	Slug           string
	Token          string
	PreviewURL     string
	SessionID      string
	Port           *int32
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastAccessedAt *time.Time
}

func previewURLFromProto(protoPreviewURL *sandboxv1.PreviewUrl) *PreviewURL {
	if protoPreviewURL == nil {
		return nil
	}
	result := &PreviewURL{
		ID:          protoPreviewURL.Id,
		WorkspaceID: protoPreviewURL.WorkspaceId,
		OwnerID:     protoPreviewURL.OwnerId,
		Slug:        protoPreviewURL.Slug,
		Token:       protoPreviewURL.Token,
		PreviewURL:  protoPreviewURL.PreviewUrl,
		SessionID:   protoPreviewURL.GetSessionId(),
	}
	if protoPreviewURL.CreatedAt != nil {
		result.CreatedAt = protoPreviewURL.CreatedAt.AsTime()
	}
	if protoPreviewURL.UpdatedAt != nil {
		result.UpdatedAt = protoPreviewURL.UpdatedAt.AsTime()
	}
	if protoPreviewURL.Port != nil {
		port := protoPreviewURL.GetPort()
		result.Port = &port
	}
	result.LastAccessedAt = protoTimestampPtr(protoPreviewURL.LastAccessedAt)
	return result
}

// CreatePreviewURL creates a sticky preview URL owned by the Workspace API key.
func (c *Client) CreatePreviewURL(ctx context.Context, slug string, sessionID *string, port *int32, opts ...PreviewURLOption) (*PreviewURL, error) {
	cfg := previewURLConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt.applyPreviewURL(&cfg)
		}
	}
	req := &sandboxv1.CreatePreviewUrlRequest{
		Slug: strings.TrimSpace(slug),
	}
	if cfg.workspaceID != "" {
		req.WorkspaceId = &cfg.workspaceID
	}
	if sessionID != nil {
		req.SessionId = sessionID
	}
	if port != nil {
		req.Port = port
	}
	resp, err := c.sandbox.CreatePreviewUrl(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return previewURLFromProto(resp.Msg.PreviewUrl), nil
}

func (c *Client) DeletePreviewURL(ctx context.Context, previewURLID string) error {
	_, err := c.sandbox.DeletePreviewUrl(ctx, connect.NewRequest(&sandboxv1.DeletePreviewUrlRequest{
		PreviewUrlId: strings.TrimSpace(previewURLID),
	}))
	return mapError(err)
}

func (c *Client) BindPreviewURL(ctx context.Context, previewURLID string, sessionID string, port int32) (*PreviewURL, error) {
	resp, err := c.sandbox.BindPreviewUrl(ctx, connect.NewRequest(&sandboxv1.BindPreviewUrlRequest{
		PreviewUrlId: strings.TrimSpace(previewURLID),
		SessionId:    strings.TrimSpace(sessionID),
		Port:         port,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return previewURLFromProto(resp.Msg.PreviewUrl), nil
}

func (c *Client) UnbindPreviewURL(ctx context.Context, previewURLID string) (*PreviewURL, error) {
	resp, err := c.sandbox.UnbindPreviewUrl(ctx, connect.NewRequest(&sandboxv1.UnbindPreviewUrlRequest{
		PreviewUrlId: strings.TrimSpace(previewURLID),
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return previewURLFromProto(resp.Msg.PreviewUrl), nil
}

// ListPreviewURLs lists sticky preview URLs owned by the Workspace API key.
func (c *Client) ListPreviewURLs(ctx context.Context, opts ...PreviewURLOption) ([]*PreviewURL, error) {
	cfg := previewURLConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt.applyPreviewURL(&cfg)
		}
	}
	var items []*PreviewURL
	var pageToken string
	for {
		req := &sandboxv1.ListPreviewUrlsRequest{
			PageSize:  automaticPageSize,
			PageToken: pageToken,
		}
		if cfg.workspaceID != "" {
			req.WorkspaceId = &cfg.workspaceID
		}
		resp, err := c.sandbox.ListPreviewUrls(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, mapError(err)
		}
		for _, item := range resp.Msg.PreviewUrls {
			items = append(items, previewURLFromProto(item))
		}
		pageToken, err = advancePageToken(pageToken, resp.Msg.NextPageToken)
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			return items, nil
		}
	}
}

func (c *Client) GetPreviewURL(ctx context.Context, previewURLID string) (*PreviewURL, error) {
	resp, err := c.sandbox.GetPreviewUrl(ctx, connect.NewRequest(&sandboxv1.GetPreviewUrlRequest{
		Lookup: &sandboxv1.GetPreviewUrlRequest_PreviewUrlId{PreviewUrlId: strings.TrimSpace(previewURLID)},
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return previewURLFromProto(resp.Msg.PreviewUrl), nil
}
