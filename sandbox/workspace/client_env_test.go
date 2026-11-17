package workspace

import (
	"context"
	"errors"
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

func TestWorkspaceClientPrefersLuxorEnv(t *testing.T) {
	var luxorRequests, tenkiRequests []capturedRequest
	luxor := envTestServer(t, &luxorRequests)
	tenki := envTestServer(t, &tenkiRequests)
	t.Setenv("TENKI_AUTH_TOKEN", "tk_tenki")
	t.Setenv("TENKI_API_KEY", "")
	t.Setenv("LUXOR_AUTH_TOKEN", "")
	t.Setenv("LUXOR_API_KEY", "lx_svc_luxor")
	t.Setenv("TENKI_CLOUD_API_URL", tenki.URL)
	t.Setenv("LUXOR_CLOUD_API_URL", luxor.URL)

	client, err := NewWorkspaceClient(WorkspaceOptions{HTTPClient: luxor.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Secrets.Get(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	if len(tenkiRequests) != 0 || len(luxorRequests) != 1 {
		t.Fatalf("requests luxor=%d tenki=%d, want LUXOR_CLOUD_API_URL only", len(luxorRequests), len(tenkiRequests))
	}
	got := luxorRequests[0]
	if got.authorization != "Bearer lx_svc_luxor" {
		t.Fatalf("authorization = %q, want LUXOR_API_KEY", got.authorization)
	}
	if got.clientFamily != "go" || !strings.HasPrefix(got.userAgent, "luxor-sandbox-go/") {
		t.Fatalf("sdk identity = %q / %q", got.clientFamily, got.userAgent)
	}
}

func TestWorkspaceClientFallsBackToTenkiEnv(t *testing.T) {
	var requests []capturedRequest
	server := envTestServer(t, &requests)
	t.Setenv("LUXOR_AUTH_TOKEN", "")
	t.Setenv("LUXOR_API_KEY", "")
	t.Setenv("LUXOR_CLOUD_API_URL", "")
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

func TestWorkspaceClientLuxorAPIKeyPrefixGuard(t *testing.T) {
	tests := []struct {
		name        string
		luxorAPIKey string
		tenkiAPIKey string
		want        string
		wantErr     error
	}{
		{name: "lx prefix is used", luxorAPIKey: "lx_luxor", tenkiAPIKey: "tk_tenki", want: "Bearer lx_luxor"},
		{name: "tk prefix is used", luxorAPIKey: "tk_luxor", tenkiAPIKey: "tk_tenki", want: "Bearer tk_luxor"},
		{name: "other value falls back to TENKI_API_KEY", luxorAPIKey: "pool-key-123", tenkiAPIKey: "tk_tenki", want: "Bearer tk_tenki"},
		{name: "other value alone is missing", luxorAPIKey: "pool-key-123", wantErr: ErrMissingAuthToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests []capturedRequest
			server := envTestServer(t, &requests)
			t.Setenv("LUXOR_AUTH_TOKEN", "")
			t.Setenv("TENKI_AUTH_TOKEN", "")
			t.Setenv("LUXOR_API_KEY", tt.luxorAPIKey)
			t.Setenv("TENKI_API_KEY", tt.tenkiAPIKey)
			t.Setenv("LUXOR_CLOUD_API_URL", server.URL)

			client, err := NewWorkspaceClient(WorkspaceOptions{HTTPClient: server.Client()})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewWorkspaceClient error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Secrets.Get(context.Background(), "secret"); err != nil {
				t.Fatal(err)
			}
			if len(requests) != 1 || requests[0].authorization != tt.want {
				t.Fatalf("requests = %+v, want authorization %q", requests, tt.want)
			}
		})
	}
}
