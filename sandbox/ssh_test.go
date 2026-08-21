package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

func TestOverrideGatewayHost(t *testing.T) {
	t.Parallel()

	sid := "00000000-0000-0000-0000-000000000001"

	tests := []struct {
		name     string
		base     string
		override string
		want     string
	}{
		{
			name:     "wss override preserves path",
			base:     "wss://sandbox-gateway.tenki.cloud/v1/ssh/" + sid,
			override: "wss://gateway.example.com:2222",
			want:     "wss://gateway.example.com:2222/v1/ssh/" + sid,
		},
		{
			name:     "ws override normalizes from http",
			base:     "ws://gateway.example.com/v1/ssh/" + sid,
			override: "http://localhost:8080",
			want:     "ws://localhost:8080/v1/ssh/" + sid,
		},
		{
			name:     "wss override normalizes from https",
			base:     "wss://sandbox-gateway.tenki.cloud/v1/ssh/" + sid,
			override: "https://edge.example:2222",
			want:     "wss://edge.example:2222/v1/ssh/" + sid,
		},
		{
			name:     "empty base builds path from session id",
			base:     "",
			override: "wss://edge.example:2222",
			want:     "wss://edge.example:2222/v1/ssh/" + sid,
		},
		{
			name:     "invalid override falls back to base",
			base:     "wss://sandbox-gateway.tenki.cloud/v1/ssh/" + sid,
			override: "://not-a-url",
			want:     "wss://sandbox-gateway.tenki.cloud/v1/ssh/" + sid,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := overrideGatewayHost(tt.base, tt.override, sid); got != tt.want {
				t.Fatalf("overrideGatewayHost(%q, %q, %q) = %q, want %q",
					tt.base, tt.override, sid, got, tt.want)
			}
		})
	}
}

// fakeSSHGatewayServer implements just ListActiveSSHGateways for the
// discovery-fallback tests below. Other RPCs return Unimplemented.
type fakeSSHGatewayServer struct {
	sandboxv1connect.UnimplementedSSHGatewayClientServiceHandler
	resp       *sandboxv1.ListActiveSSHGatewaysResponse
	requestIDs chan string
}

func (f *fakeSSHGatewayServer) ListActiveSSHGateways(
	_ context.Context,
	req *connect.Request[sandboxv1.ListActiveSSHGatewaysRequest],
) (*connect.Response[sandboxv1.ListActiveSSHGatewaysResponse], error) {
	if f.requestIDs != nil {
		f.requestIDs <- req.Msg.GetSessionId()
	}
	return connect.NewResponse(f.resp), nil
}

func newDiscoveryTestClient(t *testing.T, srv *fakeSSHGatewayServer) *Client {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := sandboxv1connect.NewSSHGatewayClientServiceHandler(srv)
	mux.Handle(path, handler)
	// SDK New() configures an h2c-only client for http:// base URLs.
	// httptest.NewServer defaults to HTTP/1.1, so we need to pass an
	// HTTP/1.1-capable client explicitly for the connect handshake to
	// negotiate the right protocol against the test server.
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c, err := New(
		WithAuthToken("tk_test-token"),
		WithBaseURL(ts.URL),
		WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestDiscoverSSHGateway_PreferEngineWsBridgeEndpoint(t *testing.T) {
	t.Parallel()

	requestIDs := make(chan string, 1)
	srv := &fakeSSHGatewayServer{
		requestIDs: requestIDs,
		resp: &sandboxv1.ListActiveSSHGatewaysResponse{
			Gateways: []*sandboxv1.ActiveSSHGateway{
				{
					GatewayId:        "gateway-1",
					Region:           "test-region",
					PublicEndpoint:   "192.0.2.10:2222",
					MeshEndpoint:     "198.51.100.2:8022",
					WsBridgeEndpoint: "wss://edge.example.com",
					Healthy:          true,
				},
			},
		},
	}
	c := newDiscoveryTestClient(t, srv)
	got := c.DiscoverSSHGateway(context.Background(), "00000000-0000-0000-0000-000000000001")
	want := "wss://edge.example.com/v1/ssh/00000000-0000-0000-0000-000000000001"
	if got != want {
		t.Fatalf("DiscoverSSHGateway = %q, want %q", got, want)
	}
	if requested := <-requestIDs; requested != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("ListActiveSSHGateways session_id = %q", requested)
	}
}

func TestDiscoverSSHGateway_SkipsUnhealthy(t *testing.T) {
	t.Parallel()

	srv := &fakeSSHGatewayServer{
		resp: &sandboxv1.ListActiveSSHGatewaysResponse{
			Gateways: []*sandboxv1.ActiveSSHGateway{
				{
					GatewayId:        "gw-down",
					WsBridgeEndpoint: "wss://gw-down.example",
					Healthy:          false,
				},
				{
					GatewayId:        "gw-up",
					WsBridgeEndpoint: "wss://gw-up.example",
					Healthy:          true,
				},
			},
		},
	}
	c := newDiscoveryTestClient(t, srv)
	got := c.DiscoverSSHGateway(context.Background(), "sid")
	if !strings.HasPrefix(got, "wss://gw-up.example/v1/ssh/") {
		t.Fatalf("unexpected URL %q (should pick the healthy gateway)", got)
	}
}

func TestDiscoverSSHGateway_EmptyWhenNoBridgeAnnounced(t *testing.T) {
	t.Parallel()

	srv := &fakeSSHGatewayServer{
		resp: &sandboxv1.ListActiveSSHGatewaysResponse{
			Gateways: []*sandboxv1.ActiveSSHGateway{
				{
					GatewayId:      "gw-no-bridge",
					PublicEndpoint: "ssh.example:22",
					Healthy:        true,
					// no WsBridgeEndpoint — older engine
				},
			},
		},
	}
	c := newDiscoveryTestClient(t, srv)
	got := c.DiscoverSSHGateway(context.Background(), "sid")
	if got != "" {
		t.Fatalf("expected empty discovery on missing bridge endpoint, got %q", got)
	}
}
