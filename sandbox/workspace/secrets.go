package workspace

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1"
	rpc "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1/workspacev1beta1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrMissingAuthToken = errors.New("workspace: missing auth token; set LUXORAI_AUTH_TOKEN or LUXORAI_API_KEY (or TENKI_AUTH_TOKEN or TENKI_API_KEY)")
	ErrInvalidAuthToken = errors.New("workspace: API keys and service tokens must start with lx_ or tk_")
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

type WorkspaceSecret struct {
	ID, WorkspaceID, Name string
	// Repository (owner/name) whose CI jobs get the secret; empty for a workspace secret.
	Repository string
	// One-line note for the people who manage the secret.
	Description                                string
	ActiveVersion, Revision                    uint32
	CreatedAt, UpdatedAt, RevokedAt, DeletedAt *time.Time
}

// SecretAvailability is where a secret applies.
type SecretAvailability string

const (
	// SecretAvailabilityWorkspace secrets reach every Tenki CI job in the workspace, and sandboxes.
	SecretAvailabilityWorkspace SecretAvailability = "workspace"
	// SecretAvailabilityRepository secrets reach only Tenki CI jobs in one repository.
	SecretAvailabilityRepository SecretAvailability = "repository"
)

type WorkspaceSecretVersion struct {
	Version              uint32
	CreatedAt, RevokedAt *time.Time
}
type SecretMutationOptions struct {
	ExpectedRevision uint32
	RequestID        string
}
type CreateSecretOptions struct {
	// Generated when empty; reuse it to retry after an uncertain result.
	RequestID string
	// Gives the secret only to CI jobs in this connected repository (owner/name); empty creates a workspace secret.
	Repository  string
	Description string
}

// UpdateSecretOptions changes only what is set. Value or ActiveVersion makes that version active and
// restores a revoked secret; Description and Availability changes don't.
type UpdateSecretOptions struct {
	SecretMutationOptions
	// Nil retains the value; a non-nil empty slice creates an empty value.
	Value         []byte
	ActiveVersion *uint32
	// Nil keeps the description; a pointer to "" clears it.
	Description *string
	// Empty keeps the availability unless Repository is set; SecretAvailabilityRepository, or Repository alone, moves the secret there.
	Availability SecretAvailability
	Repository   string
}
type SecretPageOptions struct {
	PageSize uint32
	Cursor   string
	// Availability and Repository filter List only; Repository, with or without SecretAvailabilityRepository, narrows to one repository.
	Availability SecretAvailability
	Repository   string
}
type SecretPage struct {
	Secrets    []WorkspaceSecret
	NextCursor string
}
type SecretVersionPage struct {
	Versions   []WorkspaceSecretVersion
	NextCursor string
}
type SecretRepositoryPageOptions struct {
	SecretPageOptions
	// Case-insensitive substring match on the full name.
	Search string
}
type SecretRepositoryPage struct {
	// Full names (owner/name), ordered by name.
	Repositories []string
	NextCursor   string
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
func secretTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	value := ts.AsTime()
	return &value
}
func secretMetadata(secret *pb.Secret) (WorkspaceSecret, error) {
	if secret == nil {
		return WorkspaceSecret{}, errors.New("missing secret metadata")
	}
	return WorkspaceSecret{ID: secret.Id, WorkspaceID: secret.WorkspaceId, Name: secret.Name, Repository: secret.Repository,
		Description: secret.Description, ActiveVersion: secret.ActiveVersion, Revision: secret.Revision,
		CreatedAt: secretTime(secret.CreatedAt), UpdatedAt: secretTime(secret.UpdatedAt), RevokedAt: secretTime(secret.RevokedAt), DeletedAt: secretTime(secret.DeletedAt)}, nil
}

func checkRepository(repository string) error {
	if owner, repo, ok := strings.Cut(repository, "/"); !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return fmt.Errorf("repository must be owner/name, got %q", repository)
	}
	return nil
}

// secretAvailability treats a repository without an availability as repository availability and refuses
// workspace with a repository, as the CLI does.
func secretAvailability(availability SecretAvailability, repository string) (pb.SecretAvailability, error) {
	if repository != "" {
		if availability == SecretAvailabilityWorkspace {
			return 0, errors.New("availability workspace cannot be combined with a repository")
		}
		if err := checkRepository(repository); err != nil {
			return 0, err
		}
	}
	switch availability {
	case "":
		if repository != "" {
			return pb.SecretAvailability_SECRET_AVAILABILITY_REPOSITORY, nil
		}
		return pb.SecretAvailability_SECRET_AVAILABILITY_UNSPECIFIED, nil
	case SecretAvailabilityWorkspace:
		return pb.SecretAvailability_SECRET_AVAILABILITY_WORKSPACE, nil
	case SecretAvailabilityRepository:
		return pb.SecretAvailability_SECRET_AVAILABILITY_REPOSITORY, nil
	}
	return 0, fmt.Errorf("unknown secret availability %q", availability)
}

// Create creates a secret. With options.Repository, only CI jobs in that connected repository get it,
// in place of a workspace secret with the same name.
func (c *Secrets) Create(ctx context.Context, name string, value []byte, options CreateSecretOptions) (WorkspaceSecret, error) {
	if value == nil {
		return WorkspaceSecret{}, fmt.Errorf("secret value required; use a non-nil empty slice for an empty value")
	}
	if options.Repository != "" {
		if err := checkRepository(options.Repository); err != nil {
			return WorkspaceSecret{}, err
		}
	}
	requestID, err := secretRequestID(options.RequestID)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	response, err := c.rpc.CreateSecret(ctx, connect.NewRequest(&pb.CreateSecretRequest{WorkspaceId: c.workspaceID, Name: name, Value: value, RequestId: requestID, Repository: options.Repository, Description: options.Description}))
	if err != nil {
		return WorkspaceSecret{}, workspaceSecretError(err)
	}
	return secretMetadata(response.Msg.Secret)
}
func (c *Secrets) Update(ctx context.Context, secretID string, options UpdateSecretOptions) (WorkspaceSecret, error) {
	availability, err := secretAvailability(options.Availability, options.Repository)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	if availability == pb.SecretAvailability_SECRET_AVAILABILITY_REPOSITORY {
		if err := checkRepository(options.Repository); err != nil {
			return WorkspaceSecret{}, err
		}
	}
	requestID, err := secretRequestID(options.RequestID)
	if err != nil {
		return WorkspaceSecret{}, err
	}
	response, err := c.rpc.UpdateSecret(ctx, connect.NewRequest(&pb.UpdateSecretRequest{WorkspaceId: c.workspaceID, SecretId: secretID, Value: options.Value, ExpectedRevision: options.ExpectedRevision, RequestId: requestID, ActiveVersion: options.ActiveVersion,
		Description: options.Description, Availability: availability, Repository: options.Repository}))
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
	availability, err := secretAvailability(options.Availability, options.Repository)
	if err != nil {
		return SecretPage{}, err
	}
	response, err := c.rpc.ListSecrets(ctx, connect.NewRequest(&pb.ListSecretsRequest{WorkspaceId: c.workspaceID, PageSize: options.PageSize, Cursor: options.Cursor,
		Availability: availability, Repository: options.Repository}))
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

// ListRepositories lists the connected repositories a secret can be limited to.
func (c *Secrets) ListRepositories(ctx context.Context, options SecretRepositoryPageOptions) (SecretRepositoryPage, error) {
	response, err := c.rpc.ListSecretRepositories(ctx, connect.NewRequest(&pb.ListSecretRepositoriesRequest{WorkspaceId: c.workspaceID, PageSize: options.PageSize, Cursor: options.Cursor, Search: options.Search}))
	if err != nil {
		return SecretRepositoryPage{}, workspaceSecretError(err)
	}
	result := SecretRepositoryPage{NextCursor: response.Msg.NextCursor, Repositories: make([]string, 0, len(response.Msg.Repositories))}
	for _, repository := range response.Msg.Repositories {
		result.Repositories = append(result.Repositories, repository.FullName)
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
