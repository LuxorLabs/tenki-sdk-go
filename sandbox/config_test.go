package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func clearLuxorAIEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{EnvLuxorAIAuthToken, EnvLuxorAIAPIKey, EnvLuxorAIAPIEndpoint, EnvLuxorAIAPIURL, EnvLuxorAIGatewayURL} {
		t.Setenv(name, "")
	}
}

func TestNewUsesCanonicalEnvFallbacks(t *testing.T) {
	clearLuxorAIEnv(t)
	t.Setenv(EnvAuthToken, "tk_env_auth")
	t.Setenv(EnvAPIEndpoint, "https://api.example.com")

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.authToken.reveal(); got != "tk_env_auth" {
		t.Fatalf("unexpected auth token: %q", got)
	}
	if client.baseURL != "https://api.example.com" {
		t.Fatalf("unexpected base url: %q", client.baseURL)
	}
}

func TestNewPrefersLuxorAIEnv(t *testing.T) {
	t.Setenv(EnvAuthToken, "tk_tenki_auth")
	t.Setenv(EnvAPIKey, "tk_tenki_key")
	t.Setenv(EnvLuxorAIAPIKey, "lx_luxorai_key")
	t.Setenv(EnvLuxorAIAuthToken, "")
	t.Setenv(EnvAPIEndpoint, "https://api.tenki.example")
	t.Setenv(EnvLuxorAIAPIURL, "https://api.luxor.example")
	t.Setenv(EnvLuxorAIAPIEndpoint, "")
	t.Setenv(EnvGatewayURL, "https://gateway.tenki.example")
	t.Setenv(EnvLuxorAIGatewayURL, "https://gateway.luxor.example")

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.authToken.reveal(); got != "lx_luxorai_key" {
		t.Fatalf("auth token = %q, want LUXORAI_API_KEY", got)
	}
	if client.baseURL != "https://api.luxor.example" {
		t.Fatalf("base url = %q, want LUXORAI_API_URL", client.baseURL)
	}
	if client.gatewayAddress != "https://gateway.luxor.example" {
		t.Fatalf("gateway = %q, want LUXORAI_SANDBOX_GATEWAY_URL", client.gatewayAddress)
	}
}

func TestNewWhitespaceLuxorAIAuthTokenFallsBackToTenki(t *testing.T) {
	clearLuxorAIEnv(t)
	t.Setenv(EnvLuxorAIAuthToken, " \t ")
	t.Setenv(EnvAuthToken, "tk_tenki_auth")
	t.Setenv(EnvAPIEndpoint, "https://api.example.com")

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.authToken.reveal(); got != "tk_tenki_auth" {
		t.Fatalf("auth token = %q, want TENKI_AUTH_TOKEN", got)
	}
}

func TestNewLuxorAIAuthTokenWinsOverAPIKey(t *testing.T) {
	clearLuxorAIEnv(t)
	t.Setenv(EnvLuxorAIAuthToken, "lx_auth")
	t.Setenv(EnvLuxorAIAPIKey, "lx_key")
	t.Setenv(EnvLuxorAIAPIEndpoint, "https://api.luxor.example")
	t.Setenv(EnvLuxorAIAPIURL, "https://api-url.luxor.example")

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.authToken.reveal(); got != "lx_auth" {
		t.Fatalf("auth token = %q, want LUXORAI_AUTH_TOKEN", got)
	}
	if client.baseURL != "https://api.luxor.example" {
		t.Fatalf("base url = %q, want LUXORAI_API_ENDPOINT", client.baseURL)
	}
	if client.gatewayAddress != "https://sandbox-gateway.luxor.example" {
		t.Fatalf("gateway = %q, want derived from LUXORAI_API_ENDPOINT", client.gatewayAddress)
	}
}

func TestNewAcceptsLuxorAIKeyPrefixes(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"lx_key", "lx_svc_token", "tk_key", "tk_svc_token"} {
		client, err := New(WithAuthToken(token), WithBaseURL("https://api.example.com"))
		if err != nil {
			t.Fatalf("New(%q): %v", token, err)
		}
		_ = client.Close()
	}
}

func TestAuthTokenErrorsNameLuxorAIFirst(t *testing.T) {
	t.Parallel()

	if msg := ErrMissingAuthToken.Error(); !strings.Contains(msg, "LUXORAI_AUTH_TOKEN") || strings.Index(msg, "LUXORAI_") > strings.Index(msg, "TENKI_") {
		t.Fatalf("ErrMissingAuthToken = %q, want LUXORAI_* before TENKI_*", msg)
	}
	if msg := ErrInvalidAuthToken.Error(); !strings.Contains(msg, "lx_ or tk_") {
		t.Fatalf("ErrInvalidAuthToken = %q, want both prefixes", msg)
	}
}

func TestNewRejectsWhitespaceAuthToken(t *testing.T) {
	t.Parallel()

	client, err := New(WithAuthToken(" \t "))
	if client != nil {
		_ = client.Close()
		t.Fatal("New returned a client for a whitespace auth token")
	}
	if !errors.Is(err, ErrMissingAuthToken) {
		t.Fatalf("New error = %v, want ErrMissingAuthToken", err)
	}
}

func TestNewRejectsInvalidAuthTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
	}{
		{name: "Ory session token", token: "ory_st_test-session-token"},
		{name: "arbitrary cookie value", token: "browser-cookie-token"},
		{name: "Luxor mining pool style prefix", token: "lxr_token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(WithAuthToken(test.token))
			if client != nil {
				_ = client.Close()
				t.Fatal("New returned a client for an invalid auth token")
			}
			if !errors.Is(err, ErrInvalidAuthToken) {
				t.Fatalf("New error = %v, want ErrInvalidAuthToken", err)
			}
		})
	}
}
