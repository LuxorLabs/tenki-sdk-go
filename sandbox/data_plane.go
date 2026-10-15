package sandbox

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const dataPlaneCredentialHeader = "x-tenki-session-cert"

var errDataPlaneEndpointUnavailable = errors.New("sandbox: data-plane endpoint unavailable")

// errDataPlaneCredentialMisrouted is a refusal, not a transport failure: the
// request never leaves the process.
var errDataPlaneCredentialMisrouted = errors.New("sandbox: refusing to send a data-plane credential to the wrong destination")

type sharedDataPlaneHTTPClient struct {
	client     *http.Client
	references int
}

type dataPlaneEndpointHints struct {
	mu        sync.Mutex
	endpoint  string
	resolving chan struct{}
}

// dataPlaneReadyBackoff returns the wait before the next readiness probe.
func dataPlaneReadyBackoff(attempt int) time.Duration {
	return min(time.Duration(50*(attempt+1))*time.Millisecond, 750*time.Millisecond)
}

// isEdgeNotReady reports whether err is the transient "edge route published but
// not yet serving" condition. The per-session data-plane route lives on the
// edge (Edge); the engine publishes it synchronously but Edge applies the
// config asynchronously after the admin API returns 200, so a request can
// arrive before the route serves and gets the edge's plain HTTP 404, which
// connect maps to CodeUnimplemented. A genuine node-agent Unimplemented
// (capability unavailable) is delivered with gRPC framing and lacks the
// HTTP-404 signature, so it is not retried.
func isEdgeNotReady(err error) bool {
	if err == nil {
		return false
	}
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		return false
	}
	return strings.Contains(err.Error(), "HTTP status 404")
}

func dataPlaneNotReadyError(err error) error {
	if err == nil {
		return &DataPlaneNotReadyError{}
	}
	return &DataPlaneNotReadyError{Message: "sandbox: data-plane edge route not ready", Err: err}
}

func terminalDataPlaneNotReadyError(err error) error {
	return &DataPlaneNotReadyError{Message: "sandbox: data-plane route verification failed", Err: err, Terminal: true}
}

func dataPlaneReadyContext(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if budget <= 0 {
		budget = defaultDataPlaneReadyTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), budget)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-parent.Done():
			if errors.Is(parent.Err(), context.Canceled) {
				cancel()
			}
		case <-done:
		}
	}()
	return ctx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

func newDataPlaneHTTPClient(endpoint string) *http.Client {
	transport := &http2.Transport{
		// Keep in sync with KEEPALIVE_PING_OPTIONS (TypeScript) and KEEPALIVE_CHANNEL_OPTIONS (Python).
		ReadIdleTimeout: 30 * time.Second,
		PingTimeout:     5 * time.Second,
	}
	if strings.HasPrefix(endpoint, "http://") {
		transport.AllowHTTP = true
		transport.DialTLS = func(network, addr string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		}
	}
	return &http.Client{Transport: transport}
}

func dataPlaneOrigin(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("sandbox: invalid data-plane endpoint %q", endpoint)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func (c *Client) acquireDataPlaneHTTPClient(endpoint string) (*http.Client, func(), error) {
	origin, err := dataPlaneOrigin(endpoint)
	if err != nil {
		return nil, nil, err
	}
	c.dataPlanePoolMu.Lock()
	shared := c.dataPlanePool[origin]
	if shared == nil {
		if c.dataPlanePool == nil {
			c.dataPlanePool = make(map[string]*sharedDataPlaneHTTPClient)
		}
		shared = &sharedDataPlaneHTTPClient{client: newDataPlaneHTTPClient(origin)}
		c.dataPlanePool[origin] = shared
	}
	shared.references++
	c.dataPlanePoolMu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			c.dataPlanePoolMu.Lock()
			defer c.dataPlanePoolMu.Unlock()
			if current := c.dataPlanePool[origin]; current != shared {
				return
			}
			shared.references--
			if shared.references > 0 {
				return
			}
			delete(c.dataPlanePool, origin)
			if transport, ok := shared.client.Transport.(interface{ CloseIdleConnections() }); ok {
				transport.CloseIdleConnections()
			}
		})
	}
	return shared.client, release, nil
}

func (c *Client) closeDataPlaneHTTPClients() {
	c.dataPlanePoolMu.Lock()
	defer c.dataPlanePoolMu.Unlock()
	for origin, shared := range c.dataPlanePool {
		if transport, ok := shared.client.Transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
		delete(c.dataPlanePool, origin)
	}
}

func (h *dataPlaneEndpointHints) remember(endpoint string) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	h.mu.Lock()
	h.endpoint = endpoint
	h.mu.Unlock()
}

func (h *dataPlaneEndpointHints) current() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.endpoint
}

func (h *dataPlaneEndpointHints) clear() {
	h.mu.Lock()
	h.endpoint = ""
	h.mu.Unlock()
}

func (h *dataPlaneEndpointHints) resolve(ctx context.Context, resolve func() error) (string, error) {
	for {
		h.mu.Lock()
		if h.endpoint != "" {
			endpoint := h.endpoint
			h.mu.Unlock()
			return endpoint, nil
		}
		if h.resolving == nil {
			wait := make(chan struct{})
			h.resolving = wait
			h.mu.Unlock()
			err := resolve()
			h.mu.Lock()
			endpoint := h.endpoint
			close(wait)
			h.resolving = nil
			h.mu.Unlock()
			if err != nil {
				return "", err
			}
			if endpoint == "" {
				return "", errDataPlaneEndpointUnavailable
			}
			return endpoint, nil
		}
		wait := h.resolving
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-wait:
		}
	}
}

func (c *Client) dataPlaneClientOptions(session *Session, endpoint string) []connect.ClientOption {
	opts := []connect.ClientOption{
		connect.WithInterceptors(&dataPlaneCredentialInterceptor{
			session:   session,
			endpoint:  endpoint,
			sessionID: session.ID,
		}),
		connect.WithGRPC(),
	}
	opts = append(opts, c.connectOpts...)
	return opts
}

// dataPlaneCredentialInterceptor attaches the session's data-plane credential.
// It is bound to the session and endpoint its client targets and refuses to
// attach one anywhere else: a credential authorizes one session on one node.
type dataPlaneCredentialInterceptor struct {
	session   *Session
	endpoint  string
	sessionID string
}

func (i *dataPlaneCredentialInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if !req.Spec().IsClient {
			return next(ctx, req)
		}
		if err := i.setHeaders(req.Header(), req.Any()); err != nil {
			return nil, err
		}
		resp, err := next(ctx, req)
		if err == nil || !i.session.reauthOnUnauthenticated(ctx, err) {
			return resp, err
		}
		// Replay only when the re-mint kept the session on this node; a reauth
		// that relocates it would otherwise hand the new node's credential to
		// the old one. The caller re-resolves on its next op.
		if !i.session.dataPlaneClientTargets(i.endpoint) {
			return resp, err
		}
		if setErr := i.setHeaders(req.Header(), req.Any()); setErr != nil {
			return nil, setErr
		}
		return next(ctx, req)
	}
}

func (i *dataPlaneCredentialInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func (i *dataPlaneCredentialInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		// No message is available at open time, so the endpoint binding is
		// checked here and each message is checked as it is sent.
		return &dataPlaneStreamConn{
			StreamingClientConn: conn,
			sessionID:           i.sessionID,
			openErr:             i.setHeaders(conn.RequestHeader(), nil),
		}
	}
}

// dataPlaneStreamConn carries the credential binding onto a streaming client:
// a refusal at open time fails every Send and Receive, and a message naming
// another session is refused before it is written.
type dataPlaneStreamConn struct {
	connect.StreamingClientConn
	sessionID string
	openErr   error
}

func (c *dataPlaneStreamConn) Send(msg any) error {
	if c.openErr != nil {
		return c.openErr
	}
	if err := misroutedSessionError(msg, c.sessionID); err != nil {
		return err
	}
	return c.StreamingClientConn.Send(msg)
}

func (c *dataPlaneStreamConn) Receive(msg any) error {
	if c.openErr != nil {
		return c.openErr
	}
	return c.StreamingClientConn.Receive(msg)
}

// misroutedSessionError refuses a request that names a session other than the
// one this client is credentialed for.
func misroutedSessionError(msg any, sessionID string) error {
	id, ok := requestSessionID(msg)
	if !ok || id == "" || id == sessionID {
		return nil
	}
	return connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
		"%w: request targets session %q on a client credentialed for session %q",
		errDataPlaneCredentialMisrouted, id, sessionID))
}

func (i *dataPlaneCredentialInterceptor) setHeaders(header http.Header, msg any) error {
	if err := misroutedSessionError(msg, i.sessionID); err != nil {
		return err
	}
	credential, ok := i.session.dataPlaneCredentialForEndpoint(i.endpoint)
	if !ok {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
			"%w: credential was minted for another endpoint, not %s",
			errDataPlaneCredentialMisrouted, i.endpoint))
	}
	if credential = strings.TrimSpace(credential); credential != "" {
		header.Set(dataPlaneCredentialHeader, credential)
	}
	return nil
}

// sessionIDSearchDepth bounds the walk below. Unary requests carry session_id
// one level down (`request.session_id`); streaming frames carry it one deeper,
// inside their payload oneof (`frame.start.session_id`).
const sessionIDSearchDepth = 3

// requestSessionID reads the session id a data-plane request names, walking the
// populated singular message fields rather than assuming one envelope shape.
func requestSessionID(msg any) (string, bool) {
	message, ok := msg.(proto.Message)
	if !ok || message == nil {
		return "", false
	}
	reflected := message.ProtoReflect()
	if !reflected.IsValid() {
		return "", false
	}
	return findSessionID(reflected, sessionIDSearchDepth)
}

func findSessionID(message protoreflect.Message, depth int) (string, bool) {
	fields := message.Descriptor().Fields()
	if field := fields.ByName("session_id"); field != nil &&
		field.Kind() == protoreflect.StringKind && !field.IsList() {
		return message.Get(field).String(), true
	}
	if depth == 0 {
		return "", false
	}
	for i := range fields.Len() {
		field := fields.Get(i)
		if field.Kind() != protoreflect.MessageKind || field.IsList() || field.IsMap() {
			continue
		}
		if !message.Has(field) {
			continue
		}
		if id, ok := findSessionID(message.Get(field).Message(), depth-1); ok {
			return id, true
		}
	}
	return "", false
}
