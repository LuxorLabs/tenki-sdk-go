package workspace

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"errors"
	pb "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1"
	rpc "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1/workspacev1beta1connect"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type secretsHandler struct {
	rpc.UnimplementedWorkspaceSecretsServiceHandler
	requests  []proto.Message
	headers   []string
	errorCode connect.Code
}

func metadataFixture() *pb.Secret {
	return &pb.Secret{Id: "secret", WorkspaceId: "workspace", Name: "TOKEN", ActiveVersion: 1, Revision: 2, Policy: &pb.SecretPolicy{DeliveryMode: pb.SecretDeliveryMode_SECRET_DELIVERY_MODE_GUEST_AND_INJECTION, DestinationMode: pb.SecretDestinationMode_SECRET_DESTINATION_MODE_UNSET}}
}
func (h *secretsHandler) CreateSecret(_ context.Context, request *connect.Request[pb.CreateSecretRequest]) (*connect.Response[pb.CreateSecretResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.CreateSecretResponse{Secret: metadataFixture()}), nil
}
func (h *secretsHandler) UpdateSecret(_ context.Context, request *connect.Request[pb.UpdateSecretRequest]) (*connect.Response[pb.UpdateSecretResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.UpdateSecretResponse{Secret: metadataFixture()}), nil
}
func (h *secretsHandler) GetSecret(_ context.Context, request *connect.Request[pb.GetSecretRequest]) (*connect.Response[pb.GetSecretResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.GetSecretResponse{Secret: metadataFixture()}), nil
}
func (h *secretsHandler) ListSecrets(_ context.Context, request *connect.Request[pb.ListSecretsRequest]) (*connect.Response[pb.ListSecretsResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.ListSecretsResponse{Secrets: []*pb.Secret{metadataFixture()}, NextCursor: "next"}), nil
}
func (h *secretsHandler) ListSecretVersions(_ context.Context, request *connect.Request[pb.ListSecretVersionsRequest]) (*connect.Response[pb.ListSecretVersionsResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.ListSecretVersionsResponse{Versions: []*pb.SecretVersion{{Version: 1}}, NextCursor: "versions-next"}), nil
}
func (h *secretsHandler) RevokeSecret(_ context.Context, request *connect.Request[pb.RevokeSecretRequest]) (*connect.Response[pb.RevokeSecretResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.RevokeSecretResponse{Secret: metadataFixture()}), nil
}
func (h *secretsHandler) DeleteSecret(_ context.Context, request *connect.Request[pb.DeleteSecretRequest]) (*connect.Response[pb.DeleteSecretResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.DeleteSecretResponse{Secret: metadataFixture()}), nil
}
func testClient(t *testing.T, h *secretsHandler, workspaceID string) *WorkspaceClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := rpc.NewWorkspaceSecretsServiceHandler(h)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := NewWorkspaceClient(WorkspaceOptions{WorkspaceID: workspaceID, AuthToken: "tk_test", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func TestWorkspaceSecretsWireContract(t *testing.T) {
	h := &secretsHandler{}
	c := testClient(t, h, "workspace")
	ctx := context.Background()
	policy := SecretPolicy{DeliveryMode: SecretGuestAndInjection, DestinationMode: SecretDestinationUnset}
	value := []byte{0, 255, 10, 128}
	secret, err := c.Secrets.Create(ctx, "TOKEN", value, policy, "00000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Secrets.Create(ctx, "EMPTY", []byte{}, policy, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Secrets.Update(ctx, "secret", UpdateSecretOptions{SecretMutationOptions: SecretMutationOptions{ExpectedRevision: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Secrets.Update(ctx, "secret", UpdateSecretOptions{Value: []byte{}, SecretMutationOptions: SecretMutationOptions{ExpectedRevision: 2, RequestID: "00000000-0000-4000-8000-000000000002"}}); err != nil {
		t.Fatal(err)
	}
	version := uint32(1)
	if _, err = c.Secrets.Update(ctx, "secret", UpdateSecretOptions{ActiveVersion: &version, SecretMutationOptions: SecretMutationOptions{ExpectedRevision: 2, RequestID: "00000000-0000-4000-8000-000000000003"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Secrets.Get(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	page, err := c.Secrets.List(ctx, SecretPageOptions{PageSize: 2, Cursor: "current"})
	if err != nil || page.NextCursor != "next" || len(page.Secrets) != 1 {
		t.Fatalf("list: %+v %v", page, err)
	}
	versions, err := c.Secrets.ListVersions(ctx, "secret", SecretPageOptions{Cursor: "current"})
	if err != nil || versions.NextCursor != "versions-next" || versions.Versions[0].Version != 1 {
		t.Fatalf("versions: %+v %v", versions, err)
	}
	if _, err = c.Secrets.Revoke(ctx, "secret", nil, SecretMutationOptions{ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Secrets.Revoke(ctx, "secret", &version, SecretMutationOptions{ExpectedRevision: 2, RequestID: "00000000-0000-4000-8000-000000000004"}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Secrets.Delete(ctx, "secret", SecretMutationOptions{ExpectedRevision: 2, RequestID: "00000000-0000-4000-8000-000000000005"}); err != nil {
		t.Fatal(err)
	}
	for _, request := range h.requests {
		if request.ProtoReflect().Get(request.ProtoReflect().Descriptor().Fields().ByName("workspace_id")).String() != "workspace" {
			t.Fatal("workspace scope lost")
		}
	}
	for _, header := range h.headers {
		if header != "Bearer tk_test" {
			t.Fatalf("auth header %q", header)
		}
	}
	create := h.requests[0].(*pb.CreateSecretRequest)
	if !bytes.Equal(create.Value, value) || create.RequestId != "00000000-0000-4000-8000-000000000001" || create.WorkspaceId != "workspace" {
		t.Fatal("create contract")
	}
	empty := h.requests[1].(*pb.CreateSecretRequest)
	if empty.Value == nil || len(empty.Value) != 0 || len(empty.RequestId) != 36 || empty.WorkspaceId != "workspace" {
		t.Fatal("empty/create identity contract")
	}
	omitted := h.requests[2].(*pb.UpdateSecretRequest)
	if omitted.Value != nil {
		t.Fatal("omitted update lost presence")
	}
	update := h.requests[3].(*pb.UpdateSecretRequest)
	if update.Value == nil || len(update.Value) != 0 || update.ExpectedRevision != 2 || update.RequestId != "00000000-0000-4000-8000-000000000002" {
		t.Fatal("empty/update CAS contract")
	}
	selected := h.requests[4].(*pb.UpdateSecretRequest)
	if selected.ActiveVersion == nil || *selected.ActiveVersion != 1 || selected.RequestId != "00000000-0000-4000-8000-000000000003" {
		t.Fatal("active version contract")
	}
	if h.requests[8].(*pb.RevokeSecretRequest).Version != nil || h.requests[9].(*pb.RevokeSecretRequest).GetVersion() != 1 {
		t.Fatal("revoke presence")
	}
	if h.requests[10].(*pb.DeleteSecretRequest).RequestId != "00000000-0000-4000-8000-000000000005" {
		t.Fatal("delete replay")
	}
	if _, ok := reflect.TypeOf(secret).FieldByName("Value"); ok {
		t.Fatal("metadata exposes value")
	}
	if secret.Policy.DeliveryMode != SecretGuestAndInjection || secret.Policy.DestinationMode != SecretDestinationUnset {
		t.Fatal("policy metadata")
	}
}
func TestWorkspaceSecretsConflictReplay(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeAborted, connect.CodeNotFound, connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeAlreadyExists} {
		t.Run(code.String(), func(t *testing.T) {
			h := &secretsHandler{errorCode: code}
			c := testClient(t, h, "workspace")
			for i := 0; i < 2; i++ {
				_, err := c.Secrets.Update(context.Background(), "secret", UpdateSecretOptions{SecretMutationOptions: SecretMutationOptions{ExpectedRevision: 1, RequestID: "00000000-0000-4000-8000-000000000002"}})
				var status *WorkspaceSecretError
				if !errors.As(err, &status) || status.Code != code {
					t.Fatalf("error status: %v", err)
				}
			}
			if len(h.requests) != 2 {
				t.Fatal("unexpected automatic retries")
			}
			for _, request := range h.requests {
				if request.(*pb.UpdateSecretRequest).RequestId != "00000000-0000-4000-8000-000000000002" {
					t.Fatal("request ID changed")
				}
			}
		})
	}
}
func TestWorkspaceSecretsValidation(t *testing.T) {
	t.Setenv("TENKI_AUTH_TOKEN", "")
	t.Setenv("TENKI_API_KEY", "")
	if _, err := NewWorkspaceClient(WorkspaceOptions{}); !errors.Is(err, ErrMissingAuthToken) {
		t.Fatal(err)
	}
	if _, err := NewWorkspaceClient(WorkspaceOptions{AuthToken: "invalid"}); !errors.Is(err, ErrInvalidAuthToken) {
		t.Fatal(err)
	}
	if _, err := secretPolicyProto(&SecretPolicy{DeliveryMode: "invalid", DestinationMode: SecretDestinationUnset}); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestWorkspaceClientInferredScope(t *testing.T) {
	h := &secretsHandler{}
	c := testClient(t, h, "")
	defer c.Close()
	if _, err := c.Secrets.Get(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	if h.requests[0].(*pb.GetSecretRequest).WorkspaceId != "" {
		t.Fatal("inferred scope was replaced")
	}
}

type closeTrackingTransport struct{ closed bool }

func (*closeTrackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected request")
}
func (t *closeTrackingTransport) CloseIdleConnections() { t.closed = true }

func TestWorkspaceClientConnectionOwnership(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		transport := &closeTrackingTransport{}
		options := WorkspaceOptions{AuthToken: "tk_test"}
		if supplied {
			options.HTTPClient = &http.Client{Transport: transport}
		}
		client, err := NewWorkspaceClient(options)
		if err != nil {
			t.Fatal(err)
		}
		if !supplied {
			client.httpClient.Transport = transport
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		if transport.closed == supplied {
			t.Fatalf("supplied=%v: incorrect connection ownership", supplied)
		}
	}
}
