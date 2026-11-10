package sandbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type StorageProvider string

const (
	StorageProviderUnspecified  StorageProvider = "UNSPECIFIED"
	StorageProviderAWSS3        StorageProvider = "AWS_S3"
	StorageProviderGCS          StorageProvider = "GCS"
	StorageProviderR2           StorageProvider = "R2"
	StorageProviderS3Compatible StorageProvider = "S3_COMPATIBLE"
)

type StorageAuthMethod string

const (
	StorageAuthMethodUnspecified StorageAuthMethod = "UNSPECIFIED"
	// StorageAuthMethodAccessKey reads an access key pair (GCS: HMAC keys) from Tenki Secrets.
	StorageAuthMethodAccessKey StorageAuthMethod = "ACCESS_KEY"
	// StorageAuthMethodAWSOIDCRole assumes an AWS IAM role that trusts Tenki's OIDC issuer; no keys are stored.
	StorageAuthMethodAWSOIDCRole StorageAuthMethod = "AWS_OIDC_ROLE"
	// StorageAuthMethodR2Temporary mints scoped short-lived R2 keys from a Cloudflare API token.
	StorageAuthMethodR2Temporary StorageAuthMethod = "R2_TEMPORARY"
)

// StorageAccess is empty on a mount to use the connection's default access.
type StorageAccess string

const (
	StorageAccessDefault   StorageAccess = ""
	StorageAccessReadOnly  StorageAccess = "READ_ONLY"
	StorageAccessReadWrite StorageAccess = "READ_WRITE"
)

// StorageSecret is a credential field: an existing s3_storage Tenki Secret, or a value Tenki stores as a new one.
type StorageSecret struct {
	Name  string
	Value string
}

func StorageSecretName(name string) *StorageSecret { return &StorageSecret{Name: name} }

func StorageSecretValue(value string) *StorageSecret { return &StorageSecret{Value: value} }

// StorageConnectionCredentials leaves a field nil to keep its current secret on update.
type StorageConnectionCredentials struct {
	AccessKeyID     *StorageSecret
	SecretAccessKey *StorageSecret
	SessionToken    *StorageSecret
	R2ParentToken   *StorageSecret
	RoleARN         string
}

// StorageConnectionSecrets names the Tenki Secrets a connection reads.
type StorageConnectionSecrets struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	R2ParentToken   string
}

type StorageConnection struct {
	ID            string
	WorkspaceID   string
	Name          string
	Provider      StorageProvider
	Endpoint      string
	Bucket        string
	Region        string
	AuthMethod    StorageAuthMethod
	RoleARN       string
	Secrets       StorageConnectionSecrets
	DefaultAccess StorageAccess
	Revision      int64
	// OIDCSubjectPattern is the subject an AWS role trust policy must match for keyless access.
	OIDCSubjectPattern string
	ActiveMountCount   int32
	DefaultMountCount  int32
	LastTestedAt       *time.Time
	LastTestReason     string
	// Disabled means Tenki disabled the connection: it accepts no new mounts and issues no credentials.
	Disabled   bool
	DisabledAt *time.Time
}

type StorageConnectionList struct {
	Connections []*StorageConnection
	// OIDCIssuer is the URL to register as an AWS IAM OIDC identity provider; empty when keyless AWS is unavailable.
	OIDCIssuer   string
	OIDCAudience string
}

type StorageConnectionTest struct {
	Reachable bool
	Reason    string
	Message   string
}

// StorageMountSpec names its connection by ConnectionID or ConnectionName; set DefaultMountID to change a workspace default.
// A nil Prefix or Access keeps the default mount's value. An empty Prefix mounts the whole bucket, and
// StorageAccessDefault uses the connection's default access.
type StorageMountSpec struct {
	ConnectionID   string
	ConnectionName string
	Prefix         *string
	MountPath      string
	Access         *StorageAccess
	DefaultMountID string
}

type StorageMount struct {
	ID             string
	SessionID      string
	ConnectionName string
	Spec           StorageMountSpec
	State          string
	LastError      string
}

type StorageDefaultMount struct {
	ID           string
	ConnectionID string
	Prefix       string
	MountPath    string
	Access       StorageAccess
}

type StorageDefaults struct {
	WorkspaceID string
	Mounts      []StorageDefaultMount
	Revision    uint32
}

type CreateStorageConnectionOptions struct {
	WorkspaceID string
	Name        string
	Provider    StorageProvider
	// Endpoint may be empty for AWS S3 (derived from Region) and GCS.
	Endpoint      string
	Bucket        string
	Region        string
	AuthMethod    StorageAuthMethod
	DefaultAccess StorageAccess
	Credentials   StorageConnectionCredentials
}

type UpdateStorageConnectionOptions struct {
	ExpectedRevision int64
	Name             *string
	Endpoint         *string
	Region           *string
	DefaultAccess    *StorageAccess
	Credentials      *StorageConnectionCredentials
}

func (c *Client) CreateStorageConnection(ctx context.Context, options CreateStorageConnectionOptions) (*StorageConnection, error) {
	credentials, err := storageCredentialsToProto(options.Credentials)
	if err != nil {
		return nil, err
	}
	resp, err := c.sandbox.CreateStorageConnection(ctx, connect.NewRequest(&sandboxv1.CreateStorageConnectionRequest{
		WorkspaceId:   strings.TrimSpace(options.WorkspaceID),
		Name:          strings.TrimSpace(options.Name),
		Provider:      storageProviderToProto(options.Provider),
		Endpoint:      strings.TrimSpace(options.Endpoint),
		Bucket:        strings.TrimSpace(options.Bucket),
		Region:        strings.TrimSpace(options.Region),
		AuthMethod:    storageAuthMethodToProto(options.AuthMethod),
		DefaultAccess: storageAccessToProto(options.DefaultAccess),
		Credentials:   credentials,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return storageConnectionFromProto(resp.Msg.GetConnection()), nil
}

func (c *Client) ListStorageConnections(ctx context.Context, workspaceID string) (*StorageConnectionList, error) {
	resp, err := c.sandbox.ListStorageConnections(ctx, connect.NewRequest(&sandboxv1.ListStorageConnectionsRequest{
		WorkspaceId: strings.TrimSpace(workspaceID),
	}))
	if err != nil {
		return nil, mapError(err)
	}
	list := &StorageConnectionList{OIDCIssuer: resp.Msg.GetOidcIssuer(), OIDCAudience: resp.Msg.GetOidcAudience()}
	for _, connection := range resp.Msg.GetConnections() {
		list.Connections = append(list.Connections, storageConnectionFromProto(connection))
	}
	return list, nil
}

func (c *Client) UpdateStorageConnection(ctx context.Context, connectionID string, options UpdateStorageConnectionOptions) (*StorageConnection, error) {
	req := &sandboxv1.UpdateStorageConnectionRequest{ConnectionId: strings.TrimSpace(connectionID), ExpectedRevision: options.ExpectedRevision}
	if options.Name != nil {
		name := strings.TrimSpace(*options.Name)
		req.Name = &name
	}
	if options.Endpoint != nil {
		endpoint := strings.TrimSpace(*options.Endpoint)
		req.Endpoint = &endpoint
	}
	if options.Region != nil {
		region := strings.TrimSpace(*options.Region)
		req.Region = &region
	}
	if options.DefaultAccess != nil {
		access := storageAccessToProto(*options.DefaultAccess)
		req.DefaultAccess = &access
	}
	if options.Credentials != nil {
		credentials, err := storageCredentialsToProto(*options.Credentials)
		if err != nil {
			return nil, err
		}
		req.Credentials = credentials
	}
	resp, err := c.sandbox.UpdateStorageConnection(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return storageConnectionFromProto(resp.Msg.GetConnection()), nil
}

func (c *Client) DeleteStorageConnection(ctx context.Context, connectionID string, expectedRevision int64) error {
	_, err := c.sandbox.DeleteStorageConnection(ctx, connect.NewRequest(&sandboxv1.DeleteStorageConnectionRequest{
		ConnectionId:     strings.TrimSpace(connectionID),
		ExpectedRevision: expectedRevision,
	}))
	return mapError(err)
}

func (c *Client) TestStorageConnection(ctx context.Context, connectionID string) (*StorageConnectionTest, error) {
	resp, err := c.sandbox.TestStorageConnection(ctx, connect.NewRequest(&sandboxv1.TestStorageConnectionRequest{
		ConnectionId: strings.TrimSpace(connectionID),
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return &StorageConnectionTest{
		Reachable: resp.Msg.GetReachable(), Reason: storageTestReasonFromProto(resp.Msg.GetReason()), Message: resp.Msg.GetErrorMessage(),
	}, nil
}

func (c *Client) GetStorageDefaults(ctx context.Context, workspaceID string) (*StorageDefaults, error) {
	resp, err := c.sandbox.GetStorageDefaults(ctx, connect.NewRequest(&sandboxv1.GetStorageDefaultsRequest{
		WorkspaceId: strings.TrimSpace(workspaceID),
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return storageDefaultsFromProto(resp.Msg.GetDefaults()), nil
}

// SaveStorageDefaults replaces the workspace's default mounts; pass the revision you read (0 before the first save).
func (c *Client) SaveStorageDefaults(ctx context.Context, defaults StorageDefaults, expectedRevision uint32) (*StorageDefaults, error) {
	msg := &sandboxv1.StorageDefaults{WorkspaceId: strings.TrimSpace(defaults.WorkspaceID)}
	for _, mount := range defaults.Mounts {
		msg.Mounts = append(msg.Mounts, &sandboxv1.StorageDefaultMount{
			Id: mount.ID, ConnectionId: strings.TrimSpace(mount.ConnectionID), Prefix: strings.TrimSpace(mount.Prefix),
			MountPath: strings.TrimSpace(mount.MountPath), Access: storageAccessToProto(mount.Access),
		})
	}
	resp, err := c.sandbox.SaveStorageDefaults(ctx, connect.NewRequest(&sandboxv1.SaveStorageDefaultsRequest{
		Defaults: msg, ExpectedRevision: expectedRevision,
	}))
	if err != nil {
		return nil, mapError(err)
	}
	return storageDefaultsFromProto(resp.Msg.GetDefaults()), nil
}

func storageConnectionFromProto(connection *sandboxv1.StorageConnection) *StorageConnection {
	if connection == nil {
		return nil
	}
	secrets := connection.GetSecrets()
	out := &StorageConnection{
		ID: connection.GetId(), WorkspaceID: connection.GetWorkspaceId(), Name: connection.GetName(),
		Provider: storageProviderFromProto(connection.GetProvider()), Endpoint: connection.GetEndpoint(),
		Bucket: connection.GetBucket(), Region: connection.GetRegion(), AuthMethod: storageAuthMethodFromProto(connection.GetAuthMethod()),
		RoleARN: connection.GetRoleArn(), DefaultAccess: storageAccessFromProto(connection.GetDefaultAccess()),
		Revision: connection.GetRevision(), OIDCSubjectPattern: connection.GetOidcSubjectPattern(),
		ActiveMountCount: connection.GetActiveMountCount(), DefaultMountCount: connection.GetDefaultMountCount(),
		LastTestReason: storageTestReasonFromProto(connection.GetLastTestReason()),
		Secrets: StorageConnectionSecrets{
			AccessKeyID: secrets.GetAccessKeyId(), SecretAccessKey: secrets.GetSecretAccessKey(),
			SessionToken: secrets.GetSessionToken(), R2ParentToken: secrets.GetR2ParentToken(),
		},
	}
	if connection.GetLastTestedAt() != nil {
		tested := connection.GetLastTestedAt().AsTime()
		out.LastTestedAt = &tested
	}
	out.Disabled = connection.GetDisabled()
	if connection.GetDisabledAt() != nil {
		disabled := connection.GetDisabledAt().AsTime()
		out.DisabledAt = &disabled
	}
	return out
}

func storageDefaultsFromProto(defaults *sandboxv1.StorageDefaults) *StorageDefaults {
	out := &StorageDefaults{WorkspaceID: defaults.GetWorkspaceId(), Revision: defaults.GetRevision()}
	for _, mount := range defaults.GetMounts() {
		out.Mounts = append(out.Mounts, StorageDefaultMount{
			ID: mount.GetId(), ConnectionID: mount.GetConnectionId(), Prefix: mount.GetPrefix(),
			MountPath: mount.GetMountPath(), Access: storageAccessFromProto(mount.GetAccess()),
		})
	}
	return out
}

func storageMountFromProto(mount *sandboxv1.StorageMount) StorageMount {
	if mount == nil {
		return StorageMount{}
	}
	return StorageMount{
		ID: mount.GetId(), SessionID: mount.GetSessionId(), ConnectionName: mount.GetConnectionName(),
		Spec: storageMountSpecFromProto(mount.GetSpec()), State: mount.GetState(), LastError: mount.GetLastError(),
	}
}

func storageMountSpecFromProto(spec *sandboxv1.StorageMountSpec) StorageMountSpec {
	if spec == nil {
		return StorageMountSpec{}
	}
	out := StorageMountSpec{
		ConnectionID: spec.GetConnectionId(), ConnectionName: spec.GetConnectionName(),
		MountPath: spec.GetMountPath(), DefaultMountID: spec.GetDefaultMountId(),
	}
	if spec.Prefix != nil {
		prefix := spec.GetPrefix()
		out.Prefix = &prefix
	}
	if spec.Access != nil {
		access := storageAccessFromProto(spec.GetAccess())
		out.Access = &access
	}
	return out
}

func storageMountSpecToProto(spec StorageMountSpec) *sandboxv1.StorageMountSpec {
	out := &sandboxv1.StorageMountSpec{
		MountPath: strings.TrimSpace(spec.MountPath), DefaultMountId: strings.TrimSpace(spec.DefaultMountID),
	}
	if spec.Prefix != nil {
		prefix := strings.TrimSpace(*spec.Prefix)
		out.Prefix = &prefix
	}
	if spec.Access != nil {
		out.Access = storageAccessToProto(*spec.Access).Enum()
	}
	switch {
	case strings.TrimSpace(spec.ConnectionID) != "":
		out.Connection = &sandboxv1.StorageMountSpec_ConnectionId{ConnectionId: strings.TrimSpace(spec.ConnectionID)}
	case strings.TrimSpace(spec.ConnectionName) != "":
		out.Connection = &sandboxv1.StorageMountSpec_ConnectionName{ConnectionName: strings.TrimSpace(spec.ConnectionName)}
	}
	return out
}

// ErrStorageSecretAmbiguous means a StorageSecret set both or neither of Name and Value.
var ErrStorageSecretAmbiguous = errors.New("a storage secret needs exactly one of Name or Value")

func storageCredentialsToProto(credentials StorageConnectionCredentials) (*sandboxv1.StorageConnectionCredentials, error) {
	for _, secret := range []*StorageSecret{credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, credentials.R2ParentToken} {
		if secret != nil && (strings.TrimSpace(secret.Name) == "") == (secret.Value == "") {
			return nil, ErrStorageSecretAmbiguous
		}
	}
	return &sandboxv1.StorageConnectionCredentials{
		AccessKeyId: storageSecretToProto(credentials.AccessKeyID), SecretAccessKey: storageSecretToProto(credentials.SecretAccessKey),
		SessionToken: storageSecretToProto(credentials.SessionToken), R2ParentToken: storageSecretToProto(credentials.R2ParentToken),
		RoleArn: strings.TrimSpace(credentials.RoleARN),
	}, nil
}

func storageSecretToProto(secret *StorageSecret) *sandboxv1.StorageSecretValue {
	switch {
	case secret == nil:
		return nil
	case strings.TrimSpace(secret.Name) != "":
		return &sandboxv1.StorageSecretValue{Source: &sandboxv1.StorageSecretValue_SecretName{SecretName: strings.TrimSpace(secret.Name)}}
	default:
		return &sandboxv1.StorageSecretValue{Source: &sandboxv1.StorageSecretValue_Value{Value: secret.Value}}
	}
}

func storageProviderToProto(provider StorageProvider) sandboxv1.StorageProvider {
	return sandboxv1.StorageProvider(sandboxv1.StorageProvider_value["STORAGE_PROVIDER_"+string(provider)])
}

func storageProviderFromProto(provider sandboxv1.StorageProvider) StorageProvider {
	return StorageProvider(strings.TrimPrefix(provider.String(), "STORAGE_PROVIDER_"))
}

func storageAuthMethodToProto(method StorageAuthMethod) sandboxv1.StorageAuthMethod {
	return sandboxv1.StorageAuthMethod(sandboxv1.StorageAuthMethod_value["STORAGE_AUTH_METHOD_"+string(method)])
}

func storageAuthMethodFromProto(method sandboxv1.StorageAuthMethod) StorageAuthMethod {
	return StorageAuthMethod(strings.TrimPrefix(method.String(), "STORAGE_AUTH_METHOD_"))
}

func storageAccessToProto(access StorageAccess) sandboxv1.StorageAccess {
	return sandboxv1.StorageAccess(sandboxv1.StorageAccess_value["STORAGE_ACCESS_"+string(access)])
}

func storageAccessFromProto(access sandboxv1.StorageAccess) StorageAccess {
	if access == sandboxv1.StorageAccess_STORAGE_ACCESS_UNSPECIFIED {
		return StorageAccessDefault
	}
	return StorageAccess(strings.TrimPrefix(access.String(), "STORAGE_ACCESS_"))
}

func storageTestReasonFromProto(reason sandboxv1.StorageTestReason) string {
	if reason == sandboxv1.StorageTestReason_STORAGE_TEST_REASON_UNSPECIFIED {
		return ""
	}
	return strings.TrimPrefix(reason.String(), "STORAGE_TEST_REASON_")
}
