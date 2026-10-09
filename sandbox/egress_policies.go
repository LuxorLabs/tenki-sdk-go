package sandbox

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

// EgressPolicy is a named workspace egress policy that sessions pin with WithEgressPolicies.
type EgressPolicy struct {
	ID          string
	WorkspaceID string
	Name        string
	Revision    uint32
	// Egress is the stored policy; its PolicyNames is always empty.
	Egress SessionEgressPolicy
	// ActiveSessionCount counts sessions not yet terminated that pinned this policy.
	ActiveSessionCount uint32
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
}

// EgressPolicyOption scopes egress policy requests.
type EgressPolicyOption interface {
	applyEgressPolicy(*egressPolicyConfig)
}

type egressPolicyConfig struct {
	workspaceID string
}

func (o workspaceIDOption) applyEgressPolicy(cfg *egressPolicyConfig) {
	cfg.workspaceID = o.workspaceID
}

func egressPolicyWorkspace(opts []EgressPolicyOption) *string {
	var cfg egressPolicyConfig
	for _, opt := range opts {
		if opt != nil {
			opt.applyEgressPolicy(&cfg)
		}
	}
	if workspaceID := strings.TrimSpace(cfg.workspaceID); workspaceID != "" {
		return &workspaceID
	}
	return nil
}

func egressPolicyBodyToProto(egress SessionEgressPolicy) (*sandboxv1.SessionEgressPolicy, error) {
	rules, err := egressHTTPRulesToProto(egress.HTTPRules)
	if err != nil {
		return nil, err
	}
	return &sandboxv1.SessionEgressPolicy{
		AllowDomains:   append([]string(nil), egress.AllowDomains...),
		AllowCidrs:     append([]string(nil), egress.AllowCIDRs...),
		InterceptHttps: egress.InterceptHTTPS,
		HttpRules:      rules,
	}, nil
}

func egressPolicyFromMessage(p *sandboxv1.EgressPolicy) *EgressPolicy {
	if p == nil {
		return nil
	}
	policy := &EgressPolicy{
		ID:                 p.GetId(),
		WorkspaceID:        p.GetWorkspaceId(),
		Name:               p.GetName(),
		Revision:           p.GetRevision(),
		Egress:             egressPolicyFromProto(p.GetEgress()),
		ActiveSessionCount: p.GetActiveSessionCount(),
		CreatedAt:          p.GetCreatedAt().AsTime(),
		UpdatedAt:          p.GetUpdatedAt().AsTime(),
	}
	if p.GetDeletedAt() != nil {
		deletedAt := p.GetDeletedAt().AsTime()
		policy.DeletedAt = &deletedAt
	}
	return policy
}

// CreateEgressPolicy saves egress under name in the workspace. The server applies the same checks and limits as an
// inline session policy. Egress.PolicyNames is ignored.
func (c *Client) CreateEgressPolicy(ctx context.Context, name string, egress SessionEgressPolicy, opts ...EgressPolicyOption) (*EgressPolicy, error) {
	body, err := egressPolicyBodyToProto(egress)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.CreateEgressPolicy(ctx, connect.NewRequest(&sandboxv1.CreateEgressPolicyRequest{
		WorkspaceId: egressPolicyWorkspace(opts), Name: name, Egress: body,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return egressPolicyFromMessage(resp.Msg.GetPolicy()), nil
}

// UpdateEgressPolicy replaces the policy body when expectedRevision is current. Sessions created earlier keep the
// policy they were created with.
func (c *Client) UpdateEgressPolicy(
	ctx context.Context, nameOrID string, expectedRevision uint32, egress SessionEgressPolicy, opts ...EgressPolicyOption,
) (*EgressPolicy, error) {
	body, err := egressPolicyBodyToProto(egress)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.UpdateEgressPolicy(ctx, connect.NewRequest(&sandboxv1.UpdateEgressPolicyRequest{
		WorkspaceId: egressPolicyWorkspace(opts), PolicyId: nameOrID, ExpectedRevision: expectedRevision, Egress: body,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return egressPolicyFromMessage(resp.Msg.GetPolicy()), nil
}

func (c *Client) GetEgressPolicy(ctx context.Context, nameOrID string, opts ...EgressPolicyOption) (*EgressPolicy, error) {
	resp, err := c.sandbox.GetEgressPolicy(ctx, connect.NewRequest(&sandboxv1.GetEgressPolicyRequest{
		WorkspaceId: egressPolicyWorkspace(opts), PolicyId: nameOrID,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return egressPolicyFromMessage(resp.Msg.GetPolicy()), nil
}

// ListEgressPolicies returns every live policy in the workspace, ordered by name.
func (c *Client) ListEgressPolicies(ctx context.Context, opts ...EgressPolicyOption) ([]*EgressPolicy, error) {
	workspaceID := egressPolicyWorkspace(opts)
	var policies []*EgressPolicy
	cursor := ""
	for {
		resp, err := c.sandbox.ListEgressPolicies(ctx, connect.NewRequest(&sandboxv1.ListEgressPoliciesRequest{
			WorkspaceId: workspaceID, PageSize: uint32(automaticPageSize), Cursor: cursor,
		}))
		if err != nil {
			return nil, mapError(err)
		}
		for _, policy := range resp.Msg.GetPolicies() {
			policies = append(policies, egressPolicyFromMessage(policy))
		}
		cursor, err = advancePageToken(cursor, resp.Msg.GetNextCursor())
		if err != nil {
			return nil, err
		}
		if cursor == "" {
			return policies, nil
		}
	}
}

// DeleteEgressPolicy soft-deletes the policy when expectedRevision is current. Sessions that pinned it keep their
// merged policy, and new creates naming it fail with ErrEgressPolicyNotFound.
func (c *Client) DeleteEgressPolicy(ctx context.Context, nameOrID string, expectedRevision uint32, opts ...EgressPolicyOption) (*EgressPolicy, error) {
	resp, err := c.sandbox.DeleteEgressPolicy(ctx, connect.NewRequest(&sandboxv1.DeleteEgressPolicyRequest{
		WorkspaceId: egressPolicyWorkspace(opts), PolicyId: nameOrID, ExpectedRevision: expectedRevision,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return egressPolicyFromMessage(resp.Msg.GetPolicy()), nil
}
