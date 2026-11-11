package sandbox

import (
	"errors"
	"testing"

	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/proto"
)

func TestStorageMountSpecToProtoKeepsFieldPresence(t *testing.T) {
	unset := storageMountSpecToProto(StorageMountSpec{DefaultMountID: "d1", MountPath: "/workspace/data"})
	if unset.Prefix != nil || unset.Access != nil {
		t.Fatalf("nil prefix and access must stay unset so the default keeps its values, got %v", unset)
	}

	empty, connectionDefault := "", StorageAccessDefault
	cleared := storageMountSpecToProto(StorageMountSpec{DefaultMountID: "d1", Prefix: &empty, Access: &connectionDefault})
	if cleared.Prefix == nil || cleared.GetPrefix() != "" {
		t.Fatalf("an empty prefix must be sent to clear the default's prefix, got %v", cleared.Prefix)
	}
	if cleared.Access == nil || cleared.GetAccess() != sandboxv1.StorageAccess_STORAGE_ACCESS_UNSPECIFIED {
		t.Fatalf("StorageAccessDefault must be sent as UNSPECIFIED, got %v", cleared.Access)
	}

	prefix, readOnly := " team-a ", StorageAccessReadOnly
	set := storageMountSpecToProto(StorageMountSpec{ConnectionName: "datasets", Prefix: &prefix, MountPath: "/mnt/a", Access: &readOnly})
	if set.GetPrefix() != "team-a" || set.GetAccess() != sandboxv1.StorageAccess_STORAGE_ACCESS_READ_ONLY || set.GetConnectionName() != "datasets" {
		t.Fatalf("unexpected spec %v", set)
	}
}

func TestStorageMountSpecFromProtoReportsResolvedValues(t *testing.T) {
	spec := storageMountSpecFromProto(&sandboxv1.StorageMountSpec{
		Connection: &sandboxv1.StorageMountSpec_ConnectionId{ConnectionId: "c1"},
		Prefix:     proto.String(""), MountPath: "/mnt/a", Access: sandboxv1.StorageAccess_STORAGE_ACCESS_READ_WRITE.Enum(),
	})
	if spec.Prefix == nil || *spec.Prefix != "" {
		t.Fatalf("a whole-bucket mount must report an empty prefix, got %v", spec.Prefix)
	}
	if spec.Access == nil || *spec.Access != StorageAccessReadWrite {
		t.Fatalf("expected READ_WRITE, got %v", spec.Access)
	}
	if spec.ConnectionID != "c1" || spec.MountPath != "/mnt/a" {
		t.Fatalf("unexpected spec %+v", spec)
	}
	if got := storageMountSpecFromProto(nil); got.Prefix != nil || got.Access != nil {
		t.Fatalf("a missing spec must convert to an empty one, got %+v", got)
	}
}

// Python refuses an ambiguous secret; Go must not silently send the name and drop the value.
func TestStorageCredentialsRejectAmbiguousSecrets(t *testing.T) {
	for _, credentials := range []StorageConnectionCredentials{
		{AccessKeyID: &StorageSecret{Name: "KEY_ID", Value: "AKIA"}},
		{SecretAccessKey: &StorageSecret{}},
		{SessionToken: &StorageSecret{Name: "  "}},
	} {
		if _, err := storageCredentialsToProto(credentials); !errors.Is(err, ErrStorageSecretAmbiguous) {
			t.Fatalf("storageCredentialsToProto(%+v) error = %v, want ErrStorageSecretAmbiguous", credentials, err)
		}
	}
	credentials, err := storageCredentialsToProto(StorageConnectionCredentials{
		AccessKeyID: StorageSecretName(" KEY_ID "), SecretAccessKey: StorageSecretValue("s"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if credentials.GetAccessKeyId().GetSecretName() != "KEY_ID" || credentials.GetSecretAccessKey().GetValue() != "s" {
		t.Fatalf("credentials = %v", credentials)
	}
}
