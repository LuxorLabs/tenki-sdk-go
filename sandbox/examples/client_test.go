package examples_test

import (
	"context"
	"testing"

	tenkisandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
)

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
