package sandbox

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

var errUnencryptedHTTP2 = errors.New("sandbox: unencrypted HTTP/2 not enabled")

// newHTTP2Transport returns the transport used for control-plane and
// data-plane calls to baseURL: HTTP/2 over TLS, plus prior-knowledge h2c only
// when baseURL starts with http://. HTTP/1 is never enabled.
func newHTTP2Transport(baseURL string) *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	transport := &http.Transport{
		Protocols: protocols,
		// Keep in sync with KEEPALIVE_PING_OPTIONS (TypeScript) and KEEPALIVE_CHANNEL_OPTIONS (Python).
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: 30 * time.Second,
			PingTimeout:     5 * time.Second,
		},
	}
	if strings.HasPrefix(baseURL, "http://") {
		protocols.SetUnencryptedHTTP2(true)
	} else {
		// Without this, net/http sends http:// requests, redirects included, as plaintext HTTP/1.1.
		transport.RegisterProtocol("http", refuseUnencryptedHTTP{})
	}
	return transport
}

type refuseUnencryptedHTTP struct{}

func (refuseUnencryptedHTTP) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return nil, errUnencryptedHTTP2
}
