package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"path"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/websocket"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

// SSHOption configures a single SSH() call.
type SSHOption func(*sshOptions)

type sshOptions struct {
	gatewayURL string // override the client's default gateway WS URL
}

// WithGatewayURL overrides the gateway WebSocket URL for this SSH call.
// Format: "ws://host:port", "wss://host:port", "http://host:port", or
// "https://host:port" (http/https are normalized to ws/wss). The path/query
// of the SDK's computed URL is preserved; only scheme+host are replaced.
//
// When unset, falls back to the SDK client's configured gateway URL.
// Use to point the SSH connection at a specific edge gateway:
//
//	client.SSH(ctx, sid, sandbox.WithGatewayURL("wss://gateway.example.com:2222"))
func WithGatewayURL(u string) SSHOption {
	return func(o *sshOptions) { o.gatewayURL = u }
}

type SSHConn struct {
	ws        *websocket.Conn
	readMu    sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	readBuf   []byte
}

// SSH opens an SSH transport to the session via the gateway WebSocket.
func (s *Session) SSH(ctx context.Context, opts ...SSHOption) (*SSHConn, error) {
	if s == nil || s.client == nil {
		return nil, ErrSSHUnavailable
	}
	return s.client.SSH(ctx, s.ID, opts...)
}

// SSH opens an SSH transport to the given session ID via the gateway WebSocket.
func (c *Client) SSH(ctx context.Context, sessionID string, opts ...SSHOption) (*SSHConn, error) {
	if c == nil {
		return nil, ErrSSHUnavailable
	}

	cfg := sshOptions{}
	for _, o := range opts {
		o(&cfg)
	}

	gatewayURL := c.gatewaySSHURL(sessionID)
	// Discover the Tenki Edge-fronted WS bridge from engine when the operator
	// did not pin a gateway and a per-call override was not supplied.
	// This lets new deployments work out-of-the-box once the engine starts
	// announcing ws_bridge_endpoint on ListActiveSSHGateways. Failure is
	// non-fatal: we fall back to the legacy derived URL below.
	if !c.gatewayAddressExplicit && strings.TrimSpace(cfg.gatewayURL) == "" {
		if discovered := c.DiscoverSSHGateway(ctx, sessionID); discovered != "" {
			gatewayURL = discovered
		}
	}
	if strings.TrimSpace(cfg.gatewayURL) != "" {
		gatewayURL = overrideGatewayHost(gatewayURL, cfg.gatewayURL, sessionID)
	}
	if gatewayURL == "" {
		return nil, fmt.Errorf("%w: gateway address not configured", ErrSSHUnavailable)
	}

	headers := http.Header{}
	c.setAuthHeaders(headers)

	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, gatewayURL, headers)
	if err != nil {
		return nil, mapSSHWebSocketError(resp, err)
	}
	conn.SetReadDeadline(time.Time{})
	return &SSHConn{ws: conn}, nil
}

// DiscoverSSHGateway calls engine.ListActiveSSHGateways and returns the
// per-session WS URL built from the first healthy gateway's
// ws_bridge_endpoint. Returns "" when the engine has nothing to announce
// (older engines, no gateways configured, or the call fails) so SSH()
// falls back to its derived default.
//
// CLI callers use the result to share one discovery response between cert
// minting and the ssh-proxy process.
func (c *Client) DiscoverSSHGateway(ctx context.Context, sessionID string) string {
	if c == nil || c.sshGateway == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	headers := http.Header{}
	c.setAuthHeaders(headers)
	req := connect.NewRequest(&sandboxv1.ListActiveSSHGatewaysRequest{SessionId: sessionID})
	for k, v := range headers {
		if len(v) > 0 {
			req.Header().Set(k, v[0])
		}
	}
	resp, err := c.sshGateway.ListActiveSSHGateways(ctx, req)
	if err != nil || resp == nil || resp.Msg == nil {
		return ""
	}
	for _, g := range resp.Msg.GetGateways() {
		bridge := strings.TrimSpace(g.GetWsBridgeEndpoint())
		if bridge == "" || !g.GetHealthy() {
			continue
		}
		u, parseErr := neturl.Parse(bridge)
		if parseErr != nil || u.Host == "" {
			continue
		}
		switch u.Scheme {
		case "http":
			u.Scheme = "ws"
		case "https", "":
			u.Scheme = "wss"
		}
		u.Path = path.Join(strings.TrimRight(u.Path, "/"), "v1", "ssh", strings.TrimSpace(sessionID))
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	}
	return ""
}

// overrideGatewayHost replaces the scheme+host of base with override's
// scheme+host. The path/query of base is preserved. If base is empty (the
// client has no configured gateway), fall back to deriving the path from
// sessionID so --gateway works without any client gateway configured.
//
// http/https schemes on the override are normalized to ws/wss to match the
// SDK's gateway URL contract.
func overrideGatewayHost(base, override, sessionID string) string {
	ou, err := neturl.Parse(strings.TrimSpace(override))
	if err != nil || ou.Host == "" {
		return base
	}
	switch ou.Scheme {
	case "http":
		ou.Scheme = "ws"
	case "https":
		ou.Scheme = "wss"
	case "":
		ou.Scheme = "wss"
	}

	if strings.TrimSpace(base) == "" {
		// No client-configured gateway. Build the canonical SSH path
		// from the session ID so --gateway is usable standalone.
		ou.Path = "/v1/ssh/" + strings.TrimSpace(sessionID)
		ou.RawQuery = ""
		ou.Fragment = ""
		return ou.String()
	}

	bu, err := neturl.Parse(base)
	if err != nil {
		return base
	}
	bu.Scheme = ou.Scheme
	bu.Host = ou.Host
	return bu.String()
}

func (c *SSHConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	for len(c.readBuf) == 0 {
		messageType, msg, err := c.ws.ReadMessage()
		if err != nil {
			return 0, mapSSHReadError(err)
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		c.readBuf = append(c.readBuf, msg...)
	}

	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *SSHConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	payload := append([]byte(nil), p...)
	if err := c.ws.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		return 0, mapSSHWriteError(err)
	}
	return len(p), nil
}

func (c *SSHConn) CloseWrite() error {
	// WebSocket has no half-close. SSH already carries EOF at the protocol layer,
	// so closing the transport here breaks commands like scp/sftp that still need reads.
	return nil
}

func (c *SSHConn) Close() error {
	c.closeOnce.Do(func() {
		// Acquire writeMu to avoid racing with Write, then set a short read
		// deadline to unblock any concurrent ReadMessage in Read.
		c.writeMu.Lock()
		_ = c.ws.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		c.writeMu.Unlock()
		_ = c.ws.SetReadDeadline(time.Now())
		_ = c.ws.Close()
	})
	return nil
}

func mapSSHWebSocketError(resp *http.Response, err error) error {
	if err == nil {
		return nil
	}
	if resp == nil {
		return fmt.Errorf("%w: %v", ErrSSHUnavailable, err)
	}

	reason := readSSHErrorBody(resp)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, reason)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrSessionNotFound, reason)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", ErrInvalidState, reason)
	default:
		return fmt.Errorf("%w: %s", ErrSSHUnavailable, reason)
	}
}

func readSSHErrorBody(resp *http.Response) string {
	if resp.Body == nil {
		return http.StatusText(resp.StatusCode)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil || len(body) == 0 {
		return http.StatusText(resp.StatusCode)
	}
	return strings.TrimSpace(string(body))
}

func mapSSHReadError(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return io.EOF
	}
	return fmt.Errorf("%w: %v", ErrSSHUnavailable, err)
}

func mapSSHWriteError(err error) error {
	return fmt.Errorf("%w: %v", ErrSSHUnavailable, err)
}
