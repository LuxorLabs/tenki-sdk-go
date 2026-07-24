package sandbox

import (
	"testing"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestLegacyProjectRequestWireDecodesAfterWorkspaceFieldExpansion(t *testing.T) {
	t.Parallel()

	const projectID = "019f69c7-96c9-7971-83d8-896b9d035b36"
	wire := protowire.AppendTag(nil, 1, protowire.BytesType)
	wire = protowire.AppendString(wire, projectID)
	request := &sandboxv1.ListProjectSandboxesRequest{}

	if err := proto.Unmarshal(wire, request); err != nil {
		t.Fatal(err)
	}
	if request.GetProjectId() != projectID {
		t.Fatalf("project id = %q, want %q", request.GetProjectId(), projectID)
	}
	if request.WorkspaceId != nil {
		t.Fatalf("workspace id = %q, want omitted", request.GetWorkspaceId())
	}
}
