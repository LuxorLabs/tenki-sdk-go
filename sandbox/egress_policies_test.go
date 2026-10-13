package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type egressPolicyHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	requests []proto.Message
}

var egressPolicyCreatedAt = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

func egressPolicyMessage(name string) *sandboxv1.EgressPolicy {
	return &sandboxv1.EgressPolicy{
		Id: "policy-" + name, WorkspaceId: "ws-1", Name: name, Revision: 3, ActiveSessionCount: 2,
		Egress:    &sandboxv1.SessionEgressPolicy{AllowDomains: []string{"github.com"}, InterceptHttps: true, AllowedPorts: []uint32{22, 8443}},
		CreatedAt: timestamppb.New(egressPolicyCreatedAt), UpdatedAt: timestamppb.New(egressPolicyCreatedAt),
	}
}

func (h *egressPolicyHandler) CreateEgressPolicy(
	_ context.Context, req *connect.Request[sandboxv1.CreateEgressPolicyRequest],
) (*connect.Response[sandboxv1.CreateEgressPolicyResponse], error) {
	h.requests = append(h.requests, req.Msg)
	return connect.NewResponse(&sandboxv1.CreateEgressPolicyResponse{Policy: egressPolicyMessage(req.Msg.GetName())}), nil
}

func (h *egressPolicyHandler) UpdateEgressPolicy(
	_ context.Context, req *connect.Request[sandboxv1.UpdateEgressPolicyRequest],
) (*connect.Response[sandboxv1.UpdateEgressPolicyResponse], error) {
	h.requests = append(h.requests, req.Msg)
	return connect.NewResponse(&sandboxv1.UpdateEgressPolicyResponse{Policy: egressPolicyMessage("registries")}), nil
}

func (h *egressPolicyHandler) GetEgressPolicy(
	_ context.Context, req *connect.Request[sandboxv1.GetEgressPolicyRequest],
) (*connect.Response[sandboxv1.GetEgressPolicyResponse], error) {
	h.requests = append(h.requests, req.Msg)
	if req.Msg.GetPolicyId() == "missing" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("egress policy not found"))
	}
	return connect.NewResponse(&sandboxv1.GetEgressPolicyResponse{Policy: egressPolicyMessage(req.Msg.GetPolicyId())}), nil
}

func (h *egressPolicyHandler) ListEgressPolicies(
	_ context.Context, req *connect.Request[sandboxv1.ListEgressPoliciesRequest],
) (*connect.Response[sandboxv1.ListEgressPoliciesResponse], error) {
	h.requests = append(h.requests, req.Msg)
	if req.Msg.GetCursor() == "" {
		return connect.NewResponse(&sandboxv1.ListEgressPoliciesResponse{
			Policies: []*sandboxv1.EgressPolicy{egressPolicyMessage("a")}, NextCursor: "a",
		}), nil
	}
	return connect.NewResponse(&sandboxv1.ListEgressPoliciesResponse{Policies: []*sandboxv1.EgressPolicy{egressPolicyMessage("b")}}), nil
}

func (h *egressPolicyHandler) DeleteEgressPolicy(
	_ context.Context, req *connect.Request[sandboxv1.DeleteEgressPolicyRequest],
) (*connect.Response[sandboxv1.DeleteEgressPolicyResponse], error) {
	h.requests = append(h.requests, req.Msg)
	deleted := egressPolicyMessage("registries")
	deleted.DeletedAt = timestamppb.New(egressPolicyCreatedAt.Add(time.Hour))
	return connect.NewResponse(&sandboxv1.DeleteEgressPolicyResponse{Policy: deleted}), nil
}

func newEgressPolicyHarness(t *testing.T, handler *egressPolicyHandler) *Client {
	t.Helper()
	mux := http.NewServeMux()
	path, connectHandler := sandboxv1connect.NewSandboxServiceHandler(handler)
	mux.Handle(path, connectHandler)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestEgressPolicyCRUDWireContract(t *testing.T) {
	t.Parallel()

	handler := &egressPolicyHandler{}
	client := newEgressPolicyHarness(t, handler)
	ctx := context.Background()
	body := SessionEgressPolicy{
		AllowDomains:   []string{"github.com"},
		AllowCIDRs:     []string{"10.0.0.0/8"},
		InterceptHTTPS: true,
		AllowedPorts:   []uint32{22, 8443},
		HTTPRules:      []EgressHTTPRule{{Domains: []string{"github.com"}, Methods: []string{"GET"}, PathPrefix: "/repos/"}},
		PolicyNames:    []string{"ignored"},
	}
	wantBody := &sandboxv1.SessionEgressPolicy{
		AllowDomains:   []string{"github.com"},
		AllowCidrs:     []string{"10.0.0.0/8"},
		InterceptHttps: true,
		AllowedPorts:   []uint32{22, 8443},
		HttpRules: []*sandboxv1.SessionEgressHttpRule{{
			Domains: []string{"github.com"}, Methods: []string{"GET"},
			Path: &sandboxv1.SessionEgressHttpRule_Prefix{Prefix: "/repos/"},
		}},
	}

	created, err := client.CreateEgressPolicy(ctx, "registries", body, WithWorkspaceID("ws-1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := &EgressPolicy{
		ID: "policy-registries", WorkspaceID: "ws-1", Name: "registries", Revision: 3, ActiveSessionCount: 2,
		Egress:    SessionEgressPolicy{AllowDomains: []string{"github.com"}, InterceptHTTPS: true, AllowedPorts: []uint32{22, 8443}},
		CreatedAt: egressPolicyCreatedAt, UpdatedAt: egressPolicyCreatedAt,
	}
	if !reflect.DeepEqual(created, want) {
		t.Fatalf("created = %+v, want %+v", created, want)
	}
	if _, err := client.UpdateEgressPolicy(ctx, "registries", 3, body); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := client.GetEgressPolicy(ctx, "registries"); err != nil {
		t.Fatalf("get: %v", err)
	}
	listed, err := client.ListEgressPolicies(ctx, WithWorkspaceID("ws-1"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 2 || listed[0].Name != "a" || listed[1].Name != "b" {
		t.Fatalf("list followed the cursor to %+v", listed)
	}
	deleted, err := client.DeleteEgressPolicy(ctx, "registries", 4)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted.DeletedAt == nil || !deleted.DeletedAt.Equal(egressPolicyCreatedAt.Add(time.Hour)) {
		t.Fatalf("deleted_at = %v", deleted.DeletedAt)
	}

	workspace := proto.String("ws-1")
	wantRequests := []proto.Message{
		&sandboxv1.CreateEgressPolicyRequest{WorkspaceId: workspace, Name: "registries", Egress: wantBody},
		&sandboxv1.UpdateEgressPolicyRequest{PolicyId: "registries", ExpectedRevision: 3, Egress: wantBody},
		&sandboxv1.GetEgressPolicyRequest{PolicyId: "registries"},
		&sandboxv1.ListEgressPoliciesRequest{WorkspaceId: workspace, PageSize: 100},
		&sandboxv1.ListEgressPoliciesRequest{WorkspaceId: workspace, PageSize: 100, Cursor: "a"},
		&sandboxv1.DeleteEgressPolicyRequest{PolicyId: "registries", ExpectedRevision: 4},
	}
	if len(handler.requests) != len(wantRequests) {
		t.Fatalf("got %d requests, want %d", len(handler.requests), len(wantRequests))
	}
	for i, want := range wantRequests {
		if !proto.Equal(handler.requests[i], want) {
			t.Fatalf("request %d = %v, want %v", i, handler.requests[i], want)
		}
	}
}

func TestEgressPolicyNotFoundMapsToItsOwnError(t *testing.T) {
	t.Parallel()

	client := newEgressPolicyHarness(t, &egressPolicyHandler{})

	_, err := client.GetEgressPolicy(context.Background(), "missing")

	if !errors.Is(err, ErrEgressPolicyNotFound) || errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrEgressPolicyNotFound", err)
	}
}

func TestEgressPolicyRuleWithTwoPathsIsRefusedBeforeTheRequest(t *testing.T) {
	t.Parallel()

	handler := &egressPolicyHandler{}
	client := newEgressPolicyHarness(t, handler)

	_, err := client.CreateEgressPolicy(context.Background(), "registries", SessionEgressPolicy{
		AllowDomains: []string{"github.com"}, InterceptHTTPS: true,
		AllowedPorts: []uint32{22, 8443},
		HTTPRules:    []EgressHTTPRule{{Domains: []string{"github.com"}, PathExact: "/a", PathRegex: "b"}},
	})

	if err == nil || len(handler.requests) != 0 {
		t.Fatalf("err = %v, requests = %d", err, len(handler.requests))
	}
}

func TestMissingPolicyNamesNeverMapToAnotherNotFoundError(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		"egress policies not found: ci-template",
		"egress policies not found: volume-sync, artifact-store",
		"egress policy not found: workspace does not exist",
	} {
		err := mapError(connect.NewError(connect.CodeNotFound, errors.New(message)))
		if !errors.Is(err, ErrEgressPolicyNotFound) {
			t.Errorf("%q mapped to %v, want ErrEgressPolicyNotFound", message, err)
		}
		for _, other := range []error{ErrTemplateNotFound, ErrVolumeNotFound, ErrSessionExpired, ErrSessionNotFound} {
			if errors.Is(err, other) {
				t.Errorf("%q also matched %v", message, other)
			}
		}
	}
}
