package sandbox

import (
	"strings"
	"testing"

	_ "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestGeneratedV1ProjectRemovalContract(t *testing.T) {
	t.Parallel()

	files := 0
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if file.Package() != "tenki.sandbox.v1" {
			return true
		}
		files++
		assertNoProjectFileSymbols(t, file)
		return true
	})
	if files == 0 {
		t.Fatal("no tenki.sandbox.v1 descriptors registered")
	}

	removedFields := []struct {
		message      protoreflect.FullName
		number       protoreflect.FieldNumber
		reservedName protoreflect.Name
	}{
		{"tenki.sandbox.v1.ProvisionSessionCommand", 18, "project_id"},
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

func assertNoProjectFileSymbols(t *testing.T, file protoreflect.FileDescriptor) {
	t.Helper()
	assertNoProjectName(t, file)
	for i := 0; i < file.Enums().Len(); i++ {
		assertNoProjectEnumSymbols(t, file.Enums().Get(i))
	}
	for i := 0; i < file.Messages().Len(); i++ {
		assertNoProjectMessageSymbols(t, file.Messages().Get(i))
	}
	for i := 0; i < file.Extensions().Len(); i++ {
		assertNoProjectName(t, file.Extensions().Get(i))
	}
	for i := 0; i < file.Services().Len(); i++ {
		service := file.Services().Get(i)
		assertNoProjectName(t, service)
		for j := 0; j < service.Methods().Len(); j++ {
			assertNoProjectName(t, service.Methods().Get(j))
		}
	}
}

func assertNoProjectMessageSymbols(t *testing.T, message protoreflect.MessageDescriptor) {
	t.Helper()
	assertNoProjectName(t, message)
	for i := 0; i < message.Fields().Len(); i++ {
		assertNoProjectName(t, message.Fields().Get(i))
	}
	for i := 0; i < message.Oneofs().Len(); i++ {
		assertNoProjectName(t, message.Oneofs().Get(i))
	}
	for i := 0; i < message.Enums().Len(); i++ {
		assertNoProjectEnumSymbols(t, message.Enums().Get(i))
	}
	for i := 0; i < message.Messages().Len(); i++ {
		assertNoProjectMessageSymbols(t, message.Messages().Get(i))
	}
	for i := 0; i < message.Extensions().Len(); i++ {
		assertNoProjectName(t, message.Extensions().Get(i))
	}
}

func assertNoProjectEnumSymbols(t *testing.T, enum protoreflect.EnumDescriptor) {
	t.Helper()
	assertNoProjectName(t, enum)
	for i := 0; i < enum.Values().Len(); i++ {
		assertNoProjectName(t, enum.Values().Get(i))
	}
}

func assertNoProjectName(t *testing.T, descriptor protoreflect.Descriptor) {
	t.Helper()
	if strings.Contains(strings.ToLower(string(descriptor.FullName())), "project") {
		t.Errorf("generated v1 descriptor still exposes Project symbol %s", descriptor.FullName())
	}
}
