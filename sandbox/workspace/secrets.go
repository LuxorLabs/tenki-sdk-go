package workspace

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	pb "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1"
	rpc "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1/workspacev1beta1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrMissingAuthToken = errors.New("workspace: missing auth token; set TENKI_AUTH_TOKEN or TENKI_API_KEY")
	ErrInvalidAuthToken = errors.New("workspace: API keys and service tokens must start with tk_")
)

// WorkspaceSecretError preserves the RPC status for conflict and replay handling.
type WorkspaceSecretError struct {
	Code  connect.Code
	cause error
}

func (e *WorkspaceSecretError) Error() string { return e.cause.Error() }
func (e *WorkspaceSecretError) Unwrap() error { return e.cause }
func workspaceSecretError(err error) error {
	var rpcError *connect.Error
	if !errors.As(err, &rpcError) {
		return err
	}
	return &WorkspaceSecretError{Code: rpcError.Code(), cause: err}
}

type SecretDeliveryMode string

const (
	SecretGuestAndInjection SecretDeliveryMode = "guest_and_injection"
	SecretInjectionOnly     SecretDeliveryMode = "injection_only"
)

type SecretDestinationMode string

const (
	SecretDestinationUnset     SecretDestinationMode = "unset"
	SecretDestinationAllowlist SecretDestinationMode = "allowlist"
	SecretDestinationAllowAny  SecretDestinationMode = "allow_any"
)

type SecretPolicy struct {
	DeliveryMode    SecretDeliveryMode
	DestinationMode SecretDestinationMode
	AllowedHosts    []string
}
type WorkspaceSecret struct {
	ID, WorkspaceID, Name                      string
	ActiveVersion, Revision                    uint32
	Policy                                     SecretPolicy
	CreatedAt, UpdatedAt, RevokedAt, DeletedAt *time.Time
}
type WorkspaceSecretVersion struct {
	Version              uint32
	CreatedAt, RevokedAt *time.Time
}
type SecretMutationOptions struct {
	ExpectedRevision uint32
	RequestID        string
}
type UpdateSecretOptions struct {
	SecretMutationOptions
	// Nil retains the value; a non-nil empty slice creates an empty value.
	Value         []byte
	Policy        *SecretPolicy
	ActiveVersion *uint32
}
type SecretPageOptions struct {
	PageSize uint32
	Cursor   string
}
type SecretPage struct {
	Secrets    []WorkspaceSecret
	NextCursor string
}
type SecretVersionPage struct {
	Versions   []WorkspaceSecretVersion
	NextCursor string
}

// Secrets provides metadata-only secret management through WorkspaceClient.Secrets.
type Secrets struct {
	workspaceID string
	rpc         rpc.WorkspaceSecretsServiceClient
}

func secretRequestID(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func secretPolicyProto(policy *SecretPolicy) (*pb.SecretPolicy, error) {
	if policy == nil {
		return nil, nil
	}
	delivery, ok := map[SecretDeliveryMode]pb.SecretDeliveryMode{SecretGuestAndInjection: pb.SecretDeliveryMode_SECRET_DELIVERY_MODE_GUEST_AND_INJECTION, SecretInjectionOnly: pb.SecretDeliveryMode_SECRET_DELIVERY_MODE_INJECTION_ONLY}[policy.DeliveryMode]
	if !ok {
		return nil, fmt.Errorf("invalid secret delivery mode")
	}
	destination, ok := map[SecretDestinationMode]pb.SecretDestinationMode{SecretDestinationUnset: pb.SecretDestinationMode_SECRET_DESTINATION_MODE_UNSET, SecretDestinationAllowlist: pb.SecretDestinationMode_SECRET_DESTINATION_MODE_ALLOWLIST, SecretDestinationAllowAny: pb.SecretDestinationMode_SECRET_DESTINATION_MODE_ALLOW_ANY}[policy.DestinationMode]
	if !ok {
		return nil, fmt.Errorf("invalid secret destination mode")
	}
	return &pb.SecretPolicy{DeliveryMode: delivery, DestinationMode: destination, AllowedHosts: append([]string(nil), policy.AllowedHosts...)}, nil
}
func secretTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	value := ts.AsTime()
	return &value
}
func secretMetadata(secret *pb.Secret) (WorkspaceSecret, error) {
	if secret == nil || secret.Policy == nil {
		return WorkspaceSecret{}, fmt.Errorf("missing secret metadata")
	}
	delivery := map[pb.SecretDeliveryMode]SecretDeliveryMode{pb.SecretDeliveryMode_SECRET_DELIVERY_MODE_GUEST_AND_INJECTION: SecretGuestAndInjection, pb.SecretDeliveryMode_SECRET_DELIVERY_MODE_INJECTION_ONLY: SecretInjectionOnly}[secret.Policy.DeliveryMode]
	destination := map[pb.SecretDestinationMode]SecretDestinationMode{pb.SecretDestinationMode_SECRET_DESTINATION_MODE_UNSET: SecretDestinationUnset, pb.SecretDestinationMode_SECRET_DESTINATION_MODE_ALLOWLIST: SecretDestinationAllowlist, pb.SecretDestinationMode_SECRET_DESTINATION_MODE_ALLOW_ANY: SecretDestinationAllowAny}[secret.Policy.DestinationMode]
	if delivery == "" || destination == "" {
		return WorkspaceSecret{}, fmt.Errorf("unknown secret policy mode")
	}
	return WorkspaceSecret{ID: secret.Id, WorkspaceID: secret.WorkspaceId, Name: secret.Name, ActiveVersion: secret.ActiveVersion, Revision: secret.Revision, Policy: SecretPolicy{DeliveryMode: delivery, DestinationMode: destination, AllowedHosts: append([]string(nil), secret.Policy.AllowedHosts...)}, CreatedAt: secretTime(secret.CreatedAt), UpdatedAt: secretTime(secret.UpdatedAt), RevokedAt: secretTime(secret.RevokedAt), DeletedAt: secretTime(secret.DeletedAt)}, nil
}
func (c *Secrets) Create(ctx context.Context, name string, value []byte, policy SecretPolicy, requestID string) (WorkspaceSecret, error) {
	if value == nil {
		return WorkspaceSecret{}, fmt.Errorf("secret value required; use a non-nil empty slice for an empty value")
	}
	policyPB, err := secretPolicyProto(&policy)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	requestID, err = secretRequestID(requestID)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	response, err := c.rpc.CreateSecret(ctx, connect.NewRequest(&pb.CreateSecretRequest{WorkspaceId: c.workspaceID, Name: name, Value: value, Policy: policyPB, RequestId: requestID}))
	if err != nil {
		return WorkspaceSecret{}, workspaceSecretError(err)
	}
	return secretMetadata(response.Msg.Secret)
}
func (c *Secrets) Update(ctx context.Context, secretID string, options UpdateSecretOptions) (WorkspaceSecret, error) {
	policy, err := secretPolicyProto(options.Policy)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	requestID, err := secretRequestID(options.RequestID)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	response, err := c.rpc.UpdateSecret(ctx, connect.NewRequest(&pb.UpdateSecretRequest{WorkspaceId: c.workspaceID, SecretId: secretID, Value: options.Value, Policy: policy, ExpectedRevision: options.ExpectedRevision, RequestId: requestID, ActiveVersion: options.ActiveVersion}))
	if err != nil {
		return WorkspaceSecret{}, workspaceSecretError(err)
	}
	return secretMetadata(response.Msg.Secret)
}
func (c *Secrets) Get(ctx context.Context, secretID string) (WorkspaceSecret, error) {
	response, err := c.rpc.GetSecret(ctx, connect.NewRequest(&pb.GetSecretRequest{WorkspaceId: c.workspaceID, SecretId: secretID}))
	if err != nil {
		return WorkspaceSecret{}, workspaceSecretError(err)
	}
	return secretMetadata(response.Msg.Secret)
}
func (c *Secrets) List(ctx context.Context, options SecretPageOptions) (SecretPage, error) {
	response, err := c.rpc.ListSecrets(ctx, connect.NewRequest(&pb.ListSecretsRequest{WorkspaceId: c.workspaceID, PageSize: options.PageSize, Cursor: options.Cursor}))
	if err != nil {
		return SecretPage{}, workspaceSecretError(err)
	}
	result := SecretPage{NextCursor: response.Msg.NextCursor, Secrets: make([]WorkspaceSecret, 0, len(response.Msg.Secrets))}
	for _, secret := range response.Msg.Secrets {
		metadata, err := secretMetadata(secret)
		if err != nil {
			return SecretPage{}, err
		}
		result.Secrets = append(result.Secrets, metadata)
	}
	return result, nil
}
func (c *Secrets) ListVersions(ctx context.Context, secretID string, options SecretPageOptions) (SecretVersionPage, error) {
	response, err := c.rpc.ListSecretVersions(ctx, connect.NewRequest(&pb.ListSecretVersionsRequest{WorkspaceId: c.workspaceID, SecretId: secretID, PageSize: options.PageSize, Cursor: options.Cursor}))
	if err != nil {
		return SecretVersionPage{}, workspaceSecretError(err)
	}
	result := SecretVersionPage{NextCursor: response.Msg.NextCursor, Versions: make([]WorkspaceSecretVersion, 0, len(response.Msg.Versions))}
	for _, version := range response.Msg.Versions {
		result.Versions = append(result.Versions, WorkspaceSecretVersion{Version: version.Version, CreatedAt: secretTime(version.CreatedAt), RevokedAt: secretTime(version.RevokedAt)})
	}
	return result, nil
}
func (c *Secrets) Revoke(ctx context.Context, secretID string, version *uint32, options SecretMutationOptions) (WorkspaceSecret, error) {
	requestID, err := secretRequestID(options.RequestID)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	response, err := c.rpc.RevokeSecret(ctx, connect.NewRequest(&pb.RevokeSecretRequest{WorkspaceId: c.workspaceID, SecretId: secretID, Version: version, ExpectedRevision: options.ExpectedRevision, RequestId: requestID}))
	if err != nil {
		return WorkspaceSecret{}, workspaceSecretError(err)
	}
	return secretMetadata(response.Msg.Secret)
}
func (c *Secrets) Delete(ctx context.Context, secretID string, options SecretMutationOptions) (WorkspaceSecret, error) {
	requestID, err := secretRequestID(options.RequestID)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	response, err := c.rpc.DeleteSecret(ctx, connect.NewRequest(&pb.DeleteSecretRequest{WorkspaceId: c.workspaceID, SecretId: secretID, ExpectedRevision: options.ExpectedRevision, RequestId: requestID}))
	if err != nil {
		return WorkspaceSecret{}, workspaceSecretError(err)
	}
	return secretMetadata(response.Msg.Secret)
}
