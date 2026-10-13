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
	"google.golang.org/protobuf/proto"
)

type egressCaptureHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	lastEgress         *sandboxv1.SessionEgressPolicy
	lastEgressPolicies []string
	// expanded stands in for the server's merge of the named policies.
	expanded *sandboxv1.SessionEgressPolicy
}

func (h *egressCaptureHandler) CreateSession(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateSessionRequest],
) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	h.lastEgress = req.Msg.GetEgress()
	h.lastEgressPolicies = req.Msg.GetEgressPolicies()
	egress := h.lastEgress
	if h.expanded != nil {
		egress = h.expanded
	}
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{
		Session: &sandboxv1.SandboxSession{
			Id:             "sess_egress",
			State:          sandboxv1.SessionState_SESSION_STATE_RUNNING,
			OwnerType:      "SERVICE",
			OwnerId:        "self",
			Egress:         egress,
			EgressPolicies: h.lastEgressPolicies,
		},
	}), nil
}

func newEgressHarness(t *testing.T, handler *egressCaptureHandler) *Client {
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

func TestCreateSetsEgressWireFieldWhenPolicySpecified(t *testing.T) {
	t.Parallel()

	handler := &egressCaptureHandler{}
	client := newEgressHarness(t, handler)

	sess, err := client.Create(
		context.Background(),
		WithAllowDomains("*.pypi.org", "files.pythonhosted.org"),
		WithAllowCIDRs("10.0.0.0/8"),
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	want := &sandboxv1.SessionEgressPolicy{
		AllowDomains: []string{"*.pypi.org", "files.pythonhosted.org"},
		AllowCidrs:   []string{"10.0.0.0/8"},
	}
	if !proto.Equal(handler.lastEgress, want) {
		t.Fatalf("wire egress = %+v, want %+v", handler.lastEgress, want)
	}

	gotEgress := sess.Egress()
	wantEgress := SessionEgressPolicy{
		AllowDomains: []string{"*.pypi.org", "files.pythonhosted.org"},
		AllowCIDRs:   []string{"10.0.0.0/8"},
	}
	if !reflect.DeepEqual(gotEgress, wantEgress) {
		t.Fatalf("session.Egress() = %+v, want %+v", gotEgress, wantEgress)
	}
}

func TestCreateOmitsEgressWireFieldWhenPolicyUnset(t *testing.T) {
	t.Parallel()

	handler := &egressCaptureHandler{}
	client := newEgressHarness(t, handler)

	sess, err := client.Create(context.Background())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if handler.lastEgress != nil {
		t.Fatalf("wire egress = %+v, want nil", handler.lastEgress)
	}
	if got := sess.Egress(); !reflect.DeepEqual(got, SessionEgressPolicy{}) {
		t.Fatalf("session.Egress() = %+v, want zero value", got)
	}
}

func TestCreateSendsHTTPSInterceptionAndRules(t *testing.T) {
	t.Parallel()

	handler := &egressCaptureHandler{}
	client := newEgressHarness(t, handler)
	rules := []EgressHTTPRule{
		{Domains: []string{"github.com", "pypi.org"}, Methods: []string{"GET", "HEAD"}},
		{Domains: []string{"github.com"}, Methods: []string{"POST"}, PathRegex: `^/[^/]+/[^/]+(\.git)?/git-upload-pack$`},
		{Domains: []string{"*"}, PathPrefix: "/simple/"},
		{Domains: []string{"*.example.com"}, PathExact: "/exact"},
	}

	sess, err := client.Create(
		context.Background(),
		WithAllowDomains("github.com", "pypi.org"),
		WithEgressInterceptHTTPS(),
		WithEgressHTTPRules(rules...),
		WithAllowedPorts(22, 8443),
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	want := &sandboxv1.SessionEgressPolicy{
		AllowDomains:   []string{"github.com", "pypi.org"},
		InterceptHttps: true,
		AllowedPorts:   []uint32{22, 8443},
		HttpRules: []*sandboxv1.SessionEgressHttpRule{
			{Domains: []string{"github.com", "pypi.org"}, Methods: []string{"GET", "HEAD"}},
			{
				Domains: []string{"github.com"},
				Methods: []string{"POST"},
				Path:    &sandboxv1.SessionEgressHttpRule_Regex{Regex: `^/[^/]+/[^/]+(\.git)?/git-upload-pack$`},
			},
			{Domains: []string{"*"}, Path: &sandboxv1.SessionEgressHttpRule_Prefix{Prefix: "/simple/"}},
			{Domains: []string{"*.example.com"}, Path: &sandboxv1.SessionEgressHttpRule_Exact{Exact: "/exact"}},
		},
	}
	if !proto.Equal(handler.lastEgress, want) {
		t.Fatalf("wire egress = %+v, want %+v", handler.lastEgress, want)
	}
	got := sess.Egress()
	if !got.InterceptHTTPS || !reflect.DeepEqual(got.HTTPRules, rules) || !reflect.DeepEqual(got.AllowedPorts, []uint32{22, 8443}) {
		t.Fatalf("session.Egress() = %+v, want interception and %+v", got, rules)
	}
}

func TestCreateRejectsHTTPRuleWithTwoPaths(t *testing.T) {
	t.Parallel()

	handler := &egressCaptureHandler{}
	client := newEgressHarness(t, handler)

	_, err := client.Create(
		context.Background(),
		WithAllowDomains("github.com"),
		WithEgressInterceptHTTPS(),
		WithEgressHTTPRules(EgressHTTPRule{Domains: []string{"github.com"}, PathExact: "/a", PathPrefix: "/b"}),
	)
	if err == nil {
		t.Fatal("expected a rule with two path constraints to be rejected")
	}
	if handler.lastEgress != nil {
		t.Fatalf("request reached the server: %+v", handler.lastEgress)
	}
}

func TestCreateSendsEgressPolicyNamesAndReadsThemBack(t *testing.T) {
	t.Parallel()

	handler := &egressCaptureHandler{expanded: &sandboxv1.SessionEgressPolicy{
		AllowDomains: []string{"github.com", "pypi.org"},
	}}
	client := newEgressHarness(t, handler)

	sess, err := client.Create(
		context.Background(),
		WithEgressPolicies("registries", "github-read"),
		WithAllowDomains("pypi.org"),
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if want := []string{"registries", "github-read"}; !reflect.DeepEqual(handler.lastEgressPolicies, want) {
		t.Fatalf("wire egress_policies = %v, want %v", handler.lastEgressPolicies, want)
	}
	if want := (&sandboxv1.SessionEgressPolicy{AllowDomains: []string{"pypi.org"}}); !proto.Equal(handler.lastEgress, want) {
		t.Fatalf("wire egress = %+v, want %+v", handler.lastEgress, want)
	}
	want := SessionEgressPolicy{
		AllowDomains: []string{"github.com", "pypi.org"},
		PolicyNames:  []string{"registries", "github-read"},
	}
	if got := sess.Egress(); !reflect.DeepEqual(got, want) {
		t.Fatalf("session.Egress() = %+v, want %+v", got, want)
	}
}

func TestCreateWithOnlyEgressPolicyNamesOmitsInlineEgress(t *testing.T) {
	t.Parallel()

	handler := &egressCaptureHandler{}
	client := newEgressHarness(t, handler)

	if _, err := client.Create(context.Background(), WithEgressPolicies("registries")); err != nil {
		t.Fatalf("create: %v", err)
	}

	if handler.lastEgress != nil {
		t.Fatalf("wire egress = %+v, want nil", handler.lastEgress)
	}
	if want := []string{"registries"}; !reflect.DeepEqual(handler.lastEgressPolicies, want) {
		t.Fatalf("wire egress_policies = %v, want %v", handler.lastEgressPolicies, want)
	}
}
