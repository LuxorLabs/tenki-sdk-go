package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/proto"
)

type tailnetCaptureHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	request *sandboxv1.TailnetAttachment
}

func (h *tailnetCaptureHandler) CreateSession(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateSessionRequest],
) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	h.request = req.Msg.GetTailnet()
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{Session: &sandboxv1.SandboxSession{
		Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_RUNNING,
		TailnetStatus: &sandboxv1.TailnetStatus{
			Provider: "tailscale", State: "online", NodeId: "node-1", Fqdn: "sandbox.example.ts.net",
			Ips: []string{"100.64.0.1", "fd7a:115c:a1e0::1"}, Tags: []string{"tag:sandbox"},
			Owner: "tag:sandbox", Error: "approval pending", DeviceRetained: true,
		},
	}}), nil
}

func TestTailnetCreateRequestAndStatus(t *testing.T) {
	t.Parallel()
	handler := &tailnetCaptureHandler{}
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

	session, err := client.Create(context.Background(), WithTailnet(TailnetAttachment{
		AuthKey: NewTailnetAuthKey("tskey-auth-secret"), Hostname: "sandbox-1",
		Tags: []string{"tag:sandbox"}, Ephemeral: true, ControlURL: "https://control.example.com",
		ExitNode: "auto", AcceptRoutes: true, WakeOnConnect: true, ExposePorts: []uint32{3000, 8080},
		WaitForOnline: true, ExitPolicy: TailnetExitPolicyExitNodeManaged,
		EphemeralPausePolicy: TailnetEphemeralPausePolicyRecreateOnResume,
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := &sandboxv1.TailnetAttachment{
		Provider: "tailscale", Hostname: "sandbox-1", Tags: []string{"tag:sandbox"}, Ephemeral: true,
		ControlUrl: "https://control.example.com", ExitNode: "auto", AcceptRoutes: true,
		WakeOnConnect: true, ExposePorts: []uint32{3000, 8080}, WaitForOnline: true,
		Credential:           &sandboxv1.TailnetAttachment_AuthKey{AuthKey: "tskey-auth-secret"},
		ExitPolicy:           sandboxv1.TailnetExitPolicy_TAILNET_EXIT_POLICY_EXIT_NODE_MANAGED,
		EphemeralPausePolicy: sandboxv1.TailnetEphemeralPausePolicy_TAILNET_EPHEMERAL_PAUSE_POLICY_RECREATE_ON_RESUME,
	}
	if !proto.Equal(handler.request, want) {
		t.Fatal("tailnet request mismatch")
	}
	if session.TailnetStatus == nil || session.TailnetStatus.State != TailnetStateOnline || session.TailnetStatus.NodeID != "node-1" {
		t.Fatalf("tailnet status = %+v", session.TailnetStatus)
	}
	if session.TailnetStatus.Provider != string(TailnetProviderTailscale) {
		t.Fatalf("tailnet provider = %q", session.TailnetStatus.Provider)
	}
	if session.TailnetStatus.FQDN != "sandbox.example.ts.net" || session.TailnetStatus.Owner != "tag:sandbox" {
		t.Fatalf("tailnet status = %+v", session.TailnetStatus)
	}
	if len(session.TailnetStatus.IPs) != 2 || len(session.TailnetStatus.Tags) != 1 {
		t.Fatalf("tailnet status = %+v", session.TailnetStatus)
	}
	if session.TailnetStatus.Error != "approval pending" || !session.TailnetStatus.DeviceRetained {
		t.Fatalf("tailnet status = %+v", session.TailnetStatus)
	}
}

func TestTailnetAuthKeyFormattingIsRedacted(t *testing.T) {
	t.Parallel()
	key := NewTailnetAuthKey("tskey-auth-secret")
	attachment := TailnetAttachment{AuthKey: key, Hostname: "sandbox-1"}
	encoded, err := json.Marshal(attachment)
	if err != nil {
		t.Fatalf("marshal attachment: %v", err)
	}
	for _, rendered := range []string{
		fmt.Sprint(key), fmt.Sprintf("%+v", key), fmt.Sprintf("%#v", key),
		fmt.Sprintf("%+v", attachment), fmt.Sprintf("%#v", attachment), string(encoded),
	} {
		if strings.Contains(rendered, "tskey-auth-secret") {
			t.Fatalf("formatted key contains credential: %q", rendered)
		}
	}
	if !strings.Contains(fmt.Sprint(key), "REDACTED") {
		t.Fatal("formatted key is not visibly redacted")
	}
}

func TestTailnetDefaultsInheritWorkspacePolicies(t *testing.T) {
	t.Parallel()
	got, err := tailnetAttachmentProto(TailnetAttachment{AuthKey: NewTailnetAuthKey("tskey-auth-secret")})
	if err != nil {
		t.Fatalf("tailnet proto: %v", err)
	}
	if got.GetProvider() != "tailscale" {
		t.Fatalf("provider = %q", got.GetProvider())
	}
	if got.GetExitPolicy() != sandboxv1.TailnetExitPolicy_TAILNET_EXIT_POLICY_UNSPECIFIED {
		t.Fatalf("exit policy = %s", got.GetExitPolicy())
	}
	if got.GetEphemeralPausePolicy() != sandboxv1.TailnetEphemeralPausePolicy_TAILNET_EPHEMERAL_PAUSE_POLICY_UNSPECIFIED {
		t.Fatalf("ephemeral pause policy = %s", got.GetEphemeralPausePolicy())
	}
}

func TestTailnetRejectsUnsupportedProviderBeforeCredential(t *testing.T) {
	_, err := tailnetAttachmentProto(TailnetAttachment{Provider: "other"})
	if err == nil || !strings.Contains(err.Error(), "unsupported tailnet provider") {
		t.Fatalf("provider error = %v", err)
	}
}

func TestTailnetLegacyStatusDefaultsProvider(t *testing.T) {
	status := tailnetStatusFromProto(&sandboxv1.TailnetStatus{State: "joining"})
	if status.Provider != string(TailnetProviderTailscale) {
		t.Fatalf("legacy provider = %q", status.Provider)
	}
}

func TestTailnetStatusKeepsFutureProvider(t *testing.T) {
	status := tailnetStatusFromProto(&sandboxv1.TailnetStatus{Provider: "future", State: "online"})
	if status.Provider != "future" {
		t.Fatalf("provider = %q", status.Provider)
	}
}
