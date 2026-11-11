package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// go test builds the SDK as the main module at (devel), which reports unknown.
var wantClientMetadata = map[string]string{
	"X-Luxor-Client-Family":     "go",
	"X-Luxor-Client-Version":    "unknown",
	"X-Luxor-Client-Generation": "1",
	"X-Luxor-Client-Runtime":    "go/" + strings.TrimPrefix(runtime.Version(), "go"),
}

func assertClientMetadata(t *testing.T, header http.Header) {
	t.Helper()
	for name, want := range wantClientMetadata {
		if got := header.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want [%q]", name, got, want)
		}
	}
}

type headerRecorder struct {
	mu     sync.Mutex
	header http.Header
}

func (r *headerRecorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.header = req.Header.Clone()
		r.mu.Unlock()
		next.ServeHTTP(w, req)
	})
}

func (r *headerRecorder) last() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.header
}

type metricsEngine struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
}

func (metricsEngine) GetSessionMetrics(
	context.Context,
	*connect.Request[sandboxv1.GetSessionMetricsRequest],
) (*connect.Response[sandboxv1.GetSessionMetricsResponse], error) {
	return connect.NewResponse(&sandboxv1.GetSessionMetricsResponse{}), nil
}

func TestClientMetadataReachesBothPlanes(t *testing.T) {
	t.Parallel()

	control := &headerRecorder{}
	controlMux := http.NewServeMux()
	controlMux.Handle(sandboxv1connect.NewSandboxServiceHandler(metricsEngine{}))
	controlServer := httptest.NewUnstartedServer(control.wrap(controlMux))
	controlServer.EnableHTTP2 = true
	controlServer.StartTLS()
	t.Cleanup(controlServer.Close)

	data := &headerRecorder{}
	dataMux := http.NewServeMux()
	dataMux.Handle(sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(&reproNode{}))
	dataServer := httptest.NewServer(h2c.NewHandler(data.wrap(dataMux), &http2.Server{}))
	t.Cleanup(dataServer.Close)

	client, err := New(WithAuthToken("tk_test"), WithBaseURL(controlServer.URL), WithHTTPClient(controlServer.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.GetSessionMetrics(context.Background(), "session-repro", 0); err != nil {
		t.Fatalf("control-plane call: %v", err)
	}
	assertClientMetadata(t, control.last())

	session := newReproSession(client, dataServer.URL, "data-plane-credential", time.Hour)
	if _, err := session.Stat(context.Background(), "/home/tenki"); err != nil {
		t.Fatalf("data-plane call: %v", err)
	}
	if got := data.last().Get(dataPlaneCredentialHeader); got != "data-plane-credential" {
		t.Fatalf("data-plane credential = %q", got)
	}
	assertClientMetadata(t, data.last())
}
