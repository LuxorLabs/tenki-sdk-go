package workspace

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	pb "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1"
	"google.golang.org/protobuf/proto"
)

type SecretRequestRule = pb.SecretRequestRule

type WorkspaceSecretPolicy struct {
	ID, WorkspaceID, Name           string
	Revision, AttachedSessionCount  uint32
	Rules                           []*SecretRequestRule
	CreatedAt, UpdatedAt, DeletedAt *time.Time
}

type SecretPolicyPage struct {
	Policies   []WorkspaceSecretPolicy
	NextCursor string
}

type SecretPolicies struct{ secrets *Secrets }

// Policies manages the saved request policies in this workspace.
func (c *Secrets) Policies() *SecretPolicies { return &SecretPolicies{secrets: c} }

func policyMetadata(p *pb.SecretAccessPolicy) (WorkspaceSecretPolicy, error) {
	if p == nil {
		return WorkspaceSecretPolicy{}, errors.New("missing secret policy metadata")
	}
	rules := make([]*SecretRequestRule, len(p.Rules))
	for i, r := range p.Rules {
		rules[i] = proto.CloneOf(r)
	}
	return WorkspaceSecretPolicy{ID: p.Id,
			WorkspaceID:          p.WorkspaceId,
			Name:                 p.Name,
			Revision:             p.Revision,
			AttachedSessionCount: p.AttachedSessionCount,
			Rules:                rules,
			CreatedAt:            secretTime(p.CreatedAt),
			UpdatedAt:            secretTime(p.UpdatedAt),
			DeletedAt:            secretTime(p.DeletedAt)},
		nil
}

func (c *SecretPolicies) Create(ctx context.Context, name string, rules []*SecretRequestRule, requestID string) (WorkspaceSecretPolicy, error) {
	id, err := secretRequestID(requestID)
	if err != nil {
		return WorkspaceSecretPolicy{}, err
	}
	response,
		err := c.secrets.rpc.CreateSecretPolicy(ctx,
		connect.NewRequest(&pb.CreateSecretPolicyRequest{WorkspaceId: c.secrets.workspaceID,
			Name:      name,
			Rules:     rules,
			RequestId: id}))
	if err != nil {
		return WorkspaceSecretPolicy{}, workspaceSecretError(err)
	}
	return policyMetadata(response.Msg.Policy)
}
func (c *SecretPolicies) Update(ctx context.Context,
	policyID string,
	rules []*SecretRequestRule,
	options SecretMutationOptions) (WorkspaceSecretPolicy,
	error) {
	id, err := secretRequestID(options.RequestID)
	if err != nil {
		return WorkspaceSecretPolicy{}, err
	}
	response,
		err := c.secrets.rpc.UpdateSecretPolicy(ctx,
		connect.NewRequest(&pb.UpdateSecretPolicyRequest{WorkspaceId: c.secrets.workspaceID,
			PolicyId:         policyID,
			Rules:            rules,
			ExpectedRevision: options.ExpectedRevision,
			RequestId:        id}))
	if err != nil {
		return WorkspaceSecretPolicy{}, workspaceSecretError(err)
	}
	return policyMetadata(response.Msg.Policy)
}
func (c *SecretPolicies) Delete(ctx context.Context, policyID string, options SecretMutationOptions) (WorkspaceSecretPolicy, error) {
	id, err := secretRequestID(options.RequestID)
	if err != nil {
		return WorkspaceSecretPolicy{}, err
	}
	response,
		err := c.secrets.rpc.DeleteSecretPolicy(ctx,
		connect.NewRequest(&pb.DeleteSecretPolicyRequest{WorkspaceId: c.secrets.workspaceID,
			PolicyId:         policyID,
			ExpectedRevision: options.ExpectedRevision,
			RequestId:        id}))
	if err != nil {
		return WorkspaceSecretPolicy{}, workspaceSecretError(err)
	}
	return policyMetadata(response.Msg.Policy)
}
func (c *SecretPolicies) Get(ctx context.Context, nameOrID string) (WorkspaceSecretPolicy, error) {
	response, err := c.secrets.rpc.GetSecretPolicy(ctx, connect.NewRequest(&pb.GetSecretPolicyRequest{WorkspaceId: c.secrets.workspaceID, PolicyId: nameOrID}))
	if err != nil {
		return WorkspaceSecretPolicy{}, workspaceSecretError(err)
	}
	return policyMetadata(response.Msg.Policy)
}
func (c *SecretPolicies) List(ctx context.Context, options SecretPageOptions) (SecretPolicyPage, error) {
	response,
		err := c.secrets.rpc.ListSecretPolicies(ctx,
		connect.NewRequest(&pb.ListSecretPoliciesRequest{WorkspaceId: c.secrets.workspaceID,
			PageSize: options.PageSize,
			Cursor:   options.Cursor}))
	if err != nil {
		return SecretPolicyPage{}, workspaceSecretError(err)
	}
	page := SecretPolicyPage{NextCursor: response.Msg.NextCursor, Policies: make([]WorkspaceSecretPolicy, 0, len(response.Msg.Policies))}
	for _, p := range response.Msg.Policies {
		metadata, err := policyMetadata(p)
		if err != nil {
			return SecretPolicyPage{}, err
		}
		page.Policies = append(page.Policies, metadata)
	}
	return page, nil
}
