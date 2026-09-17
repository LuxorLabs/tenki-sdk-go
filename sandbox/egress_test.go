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
	lastEgress *sandboxv1.SessionEgressPolicy
}

func (h *egressCaptureHandler) CreateSession(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateSessionRequest],
) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	h.lastEgress = req.Msg.GetEgress()
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{
		Session: &sandboxv1.SandboxSession{
			Id:        "sess_egress",
			State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
			OwnerType: "SERVICE",
			OwnerId:   "self",
			Egress:    h.lastEgress,
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
