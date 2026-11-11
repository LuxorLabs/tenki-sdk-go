package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rpc "go.luxor.ai/sandbox/internal/proto/tenki/cloud/workspace/v1beta1/workspacev1beta1connect"
)

type capturedRequest struct{ authorization, clientFamily, userAgent string }

func envTestServer(t *testing.T, captured *[]capturedRequest) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := rpc.NewWorkspaceSecretsServiceHandler(&secretsHandler{})
	mux.Handle(path, handler)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = append(*captured, capturedRequest{r.Header.Get("Authorization"), r.Header.Get("X-Luxor-Client-Family"), r.Header.Get("User-Agent")})
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestWorkspaceClientPrefersLuxorAIEnv(t *testing.T) {
	var luxorRequests, tenkiRequests []capturedRequest
	luxor := envTestServer(t, &luxorRequests)
	tenki := envTestServer(t, &tenkiRequests)
	t.Setenv("TENKI_AUTH_TOKEN", "tk_tenki")
	t.Setenv("TENKI_API_KEY", "")
	t.Setenv("LUXORAI_AUTH_TOKEN", "")
	t.Setenv("LUXORAI_API_KEY", "lx_svc_luxor")
	t.Setenv("TENKI_CLOUD_API_URL", tenki.URL)
	t.Setenv("LUXORAI_CLOUD_API_URL", luxor.URL)

	client, err := NewWorkspaceClient(WorkspaceOptions{HTTPClient: luxor.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Secrets.Get(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	if len(tenkiRequests) != 0 || len(luxorRequests) != 1 {
		t.Fatalf("requests luxor=%d tenki=%d, want LUXORAI_CLOUD_API_URL only", len(luxorRequests), len(tenkiRequests))
	}
	got := luxorRequests[0]
	if got.authorization != "Bearer lx_svc_luxor" {
		t.Fatalf("authorization = %q, want LUXORAI_API_KEY", got.authorization)
	}
	if got.clientFamily != "go" || !strings.HasPrefix(got.userAgent, "luxor-sandbox-go/") {
		t.Fatalf("sdk identity = %q / %q", got.clientFamily, got.userAgent)
	}
}

func TestWorkspaceClientFallsBackToTenkiEnv(t *testing.T) {
	var requests []capturedRequest
	server := envTestServer(t, &requests)
	t.Setenv("LUXORAI_AUTH_TOKEN", "")
	t.Setenv("LUXORAI_API_KEY", "")
	t.Setenv("LUXORAI_CLOUD_API_URL", "")
	t.Setenv("TENKI_AUTH_TOKEN", "")
	t.Setenv("TENKI_API_KEY", "tk_tenki")
	t.Setenv("TENKI_CLOUD_API_URL", server.URL)

	client, err := NewWorkspaceClient(WorkspaceOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Secrets.Get(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].authorization != "Bearer tk_tenki" {
		t.Fatalf("requests = %+v, want TENKI_API_KEY at TENKI_CLOUD_API_URL", requests)
	}
}
