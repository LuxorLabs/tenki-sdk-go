package examples_test

import (
	"context"
	"testing"

	tenkisandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
)

func TestSessionTokenUsesXSessionToken(t *testing.T) {
	t.Parallel()

	server := newExampleSandboxServer(&exampleSandboxHandler{
		t:                 t,
		expectedHeaderKey: "X-Session-Token",
		expectedHeaderVal: "ory_st_test-session-token",
	})
	defer server.Close()

	client, err := tenkisandbox.New(
		tenkisandbox.WithAuthToken("ory_st_test-session-token"),
		tenkisandbox.WithBaseURL(server.URL),
		tenkisandbox.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Create(context.Background()); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

func TestBrowserSessionUsesCookieHeader(t *testing.T) {
	t.Parallel()

	server := newExampleSandboxServer(&exampleSandboxHandler{
		t:                 t,
		expectedHeaderKey: "Cookie",
		expectedHeaderVal: "tenki_session=browser-cookie-token",
	})
	defer server.Close()

	client, err := tenkisandbox.New(
		tenkisandbox.WithAuthToken("browser-cookie-token"),
		tenkisandbox.WithBaseURL(server.URL),
		tenkisandbox.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Create(context.Background()); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

func TestQuickstartSmoke(t *testing.T) {
	t.Parallel()

	server := newExampleSandboxServer(&exampleSandboxHandler{
		t:                 t,
		expectedHeaderKey: "Authorization",
		expectedHeaderVal: "Bearer tk_test_api_key",
	})
	defer server.Close()

	client, err := tenkisandbox.New(
		tenkisandbox.WithAuthToken("tk_test_api_key"),
		tenkisandbox.WithBaseURL(server.URL),
		tenkisandbox.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	session, err := client.Create(ctx)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.ID != "session-1" {
		t.Fatalf("unexpected session id: %q", session.ID)
	}

	result, err := session.Exec(ctx, "whoami")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if string(result.Stdout) != "sandbox\n" {
		t.Fatalf("unexpected stdout: %q", string(result.Stdout))
	}

	if err := session.Close(ctx); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if session.State != tenkisandbox.SessionStateTerminated {
		t.Fatalf("unexpected session state after close: %q", session.State)
	}
}
