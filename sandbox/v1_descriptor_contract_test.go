package sandbox

import (
	"testing"

	_ "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestGeneratedV1DescriptorReservations(t *testing.T) {
	t.Parallel()

	removedFields := []struct {
		message      protoreflect.FullName
		number       protoreflect.FieldNumber
		reservedName protoreflect.Name
	}{
		{"tenki.sandbox.v1.Volume", 8, "project_id"},
		{"tenki.sandbox.v1.SandboxSession", 19, "project_id"},
		{"tenki.sandbox.v1.Snapshot", 12, "project_id"},
		{"tenki.sandbox.v1.PreviewUrl", 2, "project_id"},
		{"tenki.sandbox.v1.CreateSessionRequest", 20, "project_id"},
		{"tenki.sandbox.v1.CreateVolumeRequest", 4, "project_id"},
		{"tenki.sandbox.v1.CreatePreviewUrlRequest", 1, "project_id"},
		{"tenki.sandbox.v1.ListPreviewUrlsRequest", 1, "project_id"},
		{"tenki.sandbox.v1.GetWorkspaceSandboxUsageRequest", 2, "project_id"},
		{"tenki.sandbox.v1.WhoAmIWorkspace", 3, "projects"},
		{"tenki.sandbox.v1.Template", 14, "project_id"},
		{"tenki.sandbox.v1.CreateTemplateRequest", 8, "project_id"},
	}
	for _, removed := range removedFields {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(removed.message)
		if err != nil {
			t.Fatalf("find %s: %v", removed.message, err)
		}
		message, ok := descriptor.(protoreflect.MessageDescriptor)
		if !ok {
			t.Fatalf("%s is %T, want message descriptor", removed.message, descriptor)
		}
		if field := message.Fields().ByNumber(removed.number); field != nil {
			t.Errorf("%s field %d reused by %s", removed.message, removed.number, field.FullName())
		}
		if !message.ReservedRanges().Has(removed.number) {
			t.Errorf("%s field %d is not reserved", removed.message, removed.number)
		}
		if !message.ReservedNames().Has(removed.reservedName) {
			t.Errorf("%s name %q is not reserved", removed.message, removed.reservedName)
		}
	}
}
