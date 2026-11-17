package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func clearLuxorEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{EnvLuxorAuthToken, EnvLuxorAPIKey, EnvLuxorAPIEndpoint, EnvLuxorAPIURL, EnvLuxorGatewayURL} {
		t.Setenv(name, "")
	}
}

func TestNewUsesCanonicalEnvFallbacks(t *testing.T) {
	clearLuxorEnv(t)
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

func TestNewPrefersLuxorEnv(t *testing.T) {
	t.Setenv(EnvAuthToken, "tk_tenki_auth")
	t.Setenv(EnvAPIKey, "tk_tenki_key")
	t.Setenv(EnvLuxorAPIKey, "lx_luxor_key")
	t.Setenv(EnvLuxorAuthToken, "")
	t.Setenv(EnvAPIEndpoint, "https://api.tenki.example")
	t.Setenv(EnvLuxorAPIURL, "https://api.luxor.example")
	t.Setenv(EnvLuxorAPIEndpoint, "")
	t.Setenv(EnvGatewayURL, "https://gateway.tenki.example")
	t.Setenv(EnvLuxorGatewayURL, "https://gateway.luxor.example")

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.authToken.reveal(); got != "lx_luxor_key" {
		t.Fatalf("auth token = %q, want LUXOR_API_KEY", got)
	}
	if client.baseURL != "https://api.luxor.example" {
		t.Fatalf("base url = %q, want LUXOR_API_URL", client.baseURL)
	}
	if client.gatewayAddress != "https://gateway.luxor.example" {
		t.Fatalf("gateway = %q, want LUXOR_SANDBOX_GATEWAY_URL", client.gatewayAddress)
	}
}

func TestNewWhitespaceLuxorAuthTokenFallsBackToTenki(t *testing.T) {
	clearLuxorEnv(t)
	t.Setenv(EnvLuxorAuthToken, " \t ")
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

func TestNewLuxorAuthTokenWinsOverAPIKey(t *testing.T) {
	clearLuxorEnv(t)
	t.Setenv(EnvLuxorAuthToken, "lx_auth")
	t.Setenv(EnvLuxorAPIKey, "lx_key")
	t.Setenv(EnvLuxorAPIEndpoint, "https://api.luxor.example")
	t.Setenv(EnvLuxorAPIURL, "https://api-url.luxor.example")

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.authToken.reveal(); got != "lx_auth" {
		t.Fatalf("auth token = %q, want LUXOR_AUTH_TOKEN", got)
	}
	if client.baseURL != "https://api.luxor.example" {
		t.Fatalf("base url = %q, want LUXOR_API_ENDPOINT", client.baseURL)
	}
	if client.gatewayAddress != "https://sandbox-gateway.luxor.example" {
		t.Fatalf("gateway = %q, want derived from LUXOR_API_ENDPOINT", client.gatewayAddress)
	}
}

func TestNewLuxorAPIKeyPrefixGuard(t *testing.T) {
	tests := []struct {
		name        string
		luxorAPIKey string
		tenkiAPIKey string
		want        string
		wantErr     error
	}{
		{name: "lx prefix is used", luxorAPIKey: "lx_luxor_key", tenkiAPIKey: "tk_tenki_key", want: "lx_luxor_key"},
		{name: "tk prefix is used", luxorAPIKey: " tk_luxor_key ", tenkiAPIKey: "tk_tenki_key", want: "tk_luxor_key"},
		{name: "other value falls back to TENKI_API_KEY", luxorAPIKey: "pool-key-123", tenkiAPIKey: "tk_tenki_key", want: "tk_tenki_key"},
		{name: "other value alone is missing", luxorAPIKey: "pool-key-123", wantErr: ErrMissingAuthToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearLuxorEnv(t)
			t.Setenv(EnvAuthToken, "")
			t.Setenv(EnvLuxorAPIKey, tt.luxorAPIKey)
			t.Setenv(EnvAPIKey, tt.tenkiAPIKey)

			client, err := New(WithBaseURL("https://api.example.com"))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("New error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer func() { _ = client.Close() }()
			if got := client.authToken.reveal(); got != tt.want {
				t.Fatalf("auth token = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewAcceptsLuxorKeyPrefixes(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"lx_key", "lx_svc_token", "tk_key", "tk_svc_token"} {
		client, err := New(WithAuthToken(token), WithBaseURL("https://api.example.com"))
		if err != nil {
			t.Fatalf("New(%q): %v", token, err)
		}
		_ = client.Close()
	}
}

func TestAuthTokenErrorsNameLuxorFirst(t *testing.T) {
	t.Parallel()

	if msg := ErrMissingAuthToken.Error(); !strings.Contains(msg, "LUXOR_AUTH_TOKEN") || strings.Index(msg, "LUXOR_") > strings.Index(msg, "TENKI_") {
		t.Fatalf("ErrMissingAuthToken = %q, want LUXOR_* before TENKI_*", msg)
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

func TestDeprecatedEnvAliasesResolveToLuxorNames(t *testing.T) {
	aliases := map[string]string{
		EnvLuxorAIAPIEndpoint: EnvLuxorAPIEndpoint,
		EnvLuxorAIAPIURL:      EnvLuxorAPIURL,
		EnvLuxorAIGatewayURL:  EnvLuxorGatewayURL,
		EnvLuxorAIAuthToken:   EnvLuxorAuthToken,
		EnvLuxorAIAPIKey:      EnvLuxorAPIKey,
	}
	for alias, want := range aliases {
		if alias != want {
			t.Errorf("alias = %q, want %q", alias, want)
		}
	}
}
