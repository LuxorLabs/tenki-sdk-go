package workspace

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1"
	"google.golang.org/protobuf/proto"
)

func policyFixture() *pb.SecretAccessPolicy {
	return &pb.SecretAccessPolicy{Id: "policy-id", WorkspaceId: "workspace", Name: "claude", Revision: 3, AttachedSessionCount: 2,
		Rules: []*pb.SecretRequestRule{{Origin: "https://api.example.com", Methods: []string{"POST"}, PathPrefix: "/v1/", Header: "Authorization", Secrets: []string{"TOKEN", "SECOND"}}}}
}

func (h *secretsHandler) CreateSecretPolicy(_ context.Context, request *connect.Request[pb.CreateSecretPolicyRequest]) (*connect.Response[pb.CreateSecretPolicyResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.CreateSecretPolicyResponse{Policy: policyFixture()}), nil
}

func (h *secretsHandler) UpdateSecretPolicy(_ context.Context, request *connect.Request[pb.UpdateSecretPolicyRequest]) (*connect.Response[pb.UpdateSecretPolicyResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.UpdateSecretPolicyResponse{Policy: policyFixture()}), nil
}

func (h *secretsHandler) GetSecretPolicy(_ context.Context, request *connect.Request[pb.GetSecretPolicyRequest]) (*connect.Response[pb.GetSecretPolicyResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.GetSecretPolicyResponse{Policy: policyFixture()}), nil
}

func (h *secretsHandler) ListSecretPolicies(_ context.Context, request *connect.Request[pb.ListSecretPoliciesRequest]) (*connect.Response[pb.ListSecretPoliciesResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.ListSecretPoliciesResponse{Policies: []*pb.SecretAccessPolicy{policyFixture()}, NextCursor: "next"}), nil
}

func (h *secretsHandler) DeleteSecretPolicy(_ context.Context, request *connect.Request[pb.DeleteSecretPolicyRequest]) (*connect.Response[pb.DeleteSecretPolicyResponse], error) {
	h.requests = append(h.requests, proto.Clone(request.Msg))
	h.headers = append(h.headers, request.Header().Get("Authorization"))
	if h.errorCode != 0 {
		return nil, connect.NewError(h.errorCode, errors.New("request failed"))
	}
	return connect.NewResponse(&pb.DeleteSecretPolicyResponse{Policy: policyFixture()}), nil
}

func TestSecretPoliciesWireContract(t *testing.T) {
	for _, workspaceID := range []string{"workspace", ""} {
		t.Run(workspaceID, func(t *testing.T) {
			h := &secretsHandler{}
			c := testClient(t, h, workspaceID)
			defer c.Close()
			policies := c.Secrets.Policies()
			ctx := context.Background()
			requestID := "00000000-0000-4000-8000-000000000001"
			rules := policyFixture().Rules
			created, err := policies.Create(ctx, "claude", rules, requestID)
			if err != nil {
				t.Fatal(err)
			}
			if created.Name != "claude" || created.AttachedSessionCount != 2 || len(created.Rules[0].Secrets) != 2 {
				t.Fatal("metadata lost")
			}
			options := SecretMutationOptions{ExpectedRevision: 3, RequestID: requestID}
			if _, err = policies.Update(ctx, created.ID, rules, options); err != nil {
				t.Fatal(err)
			}
			if _, err = policies.Get(ctx, "claude"); err != nil {
				t.Fatal(err)
			}
			page, err := policies.List(ctx, SecretPageOptions{PageSize: 2, Cursor: "current"})
			if err != nil || len(page.Policies) != 1 || page.NextCursor != "next" {
				t.Fatalf("list: %+v %v", page, err)
			}
			if _, err = policies.Delete(ctx, created.ID, options); err != nil {
				t.Fatal(err)
			}
			for _, request := range h.requests {
				if request.ProtoReflect().Get(request.ProtoReflect().Descriptor().Fields().ByName("workspace_id")).String() != workspaceID {
					t.Fatal("scope lost")
				}
			}
			for _, header := range h.headers {
				if header != "Bearer tk_test" {
					t.Fatal("auth lost")
				}
			}
			create := h.requests[0].(*pb.CreateSecretPolicyRequest)
			if create.RequestId != requestID || !proto.Equal(create.Rules[0], rules[0]) {
				t.Fatal("create contract")
			}
			update := h.requests[1].(*pb.UpdateSecretPolicyRequest)
			if update.PolicyId != created.ID || update.ExpectedRevision != 3 || update.RequestId != requestID {
				t.Fatal("update CAS")
			}
			list := h.requests[3].(*pb.ListSecretPoliciesRequest)
			if list.Cursor != "current" || list.PageSize != 2 {
				t.Fatal("pagination")
			}
			deletion := h.requests[4].(*pb.DeleteSecretPolicyRequest)
			if deletion.ExpectedRevision != 3 || deletion.RequestId != requestID {
				t.Fatal("delete CAS")
			}
			h.errorCode = connect.CodeAborted
			for range 2 {
				_, err = policies.Update(ctx, created.ID, rules, options)
				var failure *WorkspaceSecretError
				if !errors.As(err, &failure) || failure.Code != connect.CodeAborted {
					t.Fatalf("status lost: %v", err)
				}
			}
			if len(h.requests) != 7 {
				t.Fatal("unexpected retry")
			}
			for _, request := range h.requests[5:] {
				if request.(*pb.UpdateSecretPolicyRequest).RequestId != requestID {
					t.Fatal("replay identity lost")
				}
			}
		})
	}
}
