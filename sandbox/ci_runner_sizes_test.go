package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ciRunnerSizesHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	workspaceID *string
}

func (h *ciRunnerSizesHandler) ListCIRunnerSizes(
	_ context.Context, req *connect.Request[sandboxv1.ListCIRunnerSizesRequest],
) (*connect.Response[sandboxv1.ListCIRunnerSizesResponse], error) {
	h.workspaceID = req.Msg.WorkspaceId
	return connect.NewResponse(&sandboxv1.ListCIRunnerSizesResponse{
		Sizes: []*sandboxv1.CIRunnerSize{
			{RunsOnLabel: "tenki-small-2c-4g", Cpu: 2, Memory: 4},
			{RunsOnLabel: "tenki-medium-4c-8g", Cpu: 4, Memory: 8},
		},
		DefaultRunsOnLabel: "tenki-small-2c-4g",
	}), nil
}

func TestListCIRunnerSizesUsesTheKeysWorkspace(t *testing.T) {
	t.Parallel()

	handler := &ciRunnerSizesHandler{}
	mux := http.NewServeMux()
	mux.Handle(sandboxv1connect.NewSandboxServiceHandler(handler))
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	sizes, err := client.ListCIRunnerSizes(context.Background())

	if err != nil {
		t.Fatalf("ListCIRunnerSizes: %v", err)
	}
	if handler.workspaceID != nil {
		t.Fatalf("workspace_id = %q, want unset so the key's workspace is used", *handler.workspaceID)
	}
	want := &CIRunnerSizes{
		Sizes: []CIRunnerSize{
			{RunsOnLabel: "tenki-small-2c-4g", CPU: 2, MemoryGiB: 4},
			{RunsOnLabel: "tenki-medium-4c-8g", CPU: 4, MemoryGiB: 8},
		},
		DefaultRunsOnLabel: "tenki-small-2c-4g",
	}
	if !reflect.DeepEqual(sizes, want) {
		t.Fatalf("sizes = %+v, want %+v", sizes, want)
	}
}
