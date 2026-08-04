package sandbox

import (
	"errors"
	"testing"
)

func TestNewUsesCanonicalEnvFallbacks(t *testing.T) {
	t.Setenv(EnvAuthToken, "tk_env_auth")
	t.Setenv(EnvAPIEndpoint, "https://api.example.com")

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if client.authToken != "tk_env_auth" {
		t.Fatalf("unexpected auth token: %q", client.authToken)
	}
	if client.baseURL != "https://api.example.com" {
		t.Fatalf("unexpected base url: %q", client.baseURL)
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
