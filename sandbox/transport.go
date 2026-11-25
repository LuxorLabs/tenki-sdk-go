package sandbox

import (
	"net/http"
	"time"
)

// newHTTP2Transport returns the transport used for control-plane and
// data-plane calls: HTTP/2 over TLS for https:// endpoints and
// prior-knowledge h2c for plain http:// endpoints. Protocols selects h2c only
// when HTTP/1 is left out, so HTTP/1 is not enabled here.
func newHTTP2Transport() *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Transport{
		Protocols: protocols,
		// Keep in sync with KEEPALIVE_PING_OPTIONS (TypeScript) and KEEPALIVE_CHANNEL_OPTIONS (Python).
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: 30 * time.Second,
			PingTimeout:     5 * time.Second,
		},
	}
}
