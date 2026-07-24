package examples_test

import (
	"context"
	"fmt"

	tenkisandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
)

func ExampleClient_Create() {
	ctx := context.Background()
	server := newExampleSandboxServer(&exampleSandboxHandler{})
	defer server.Close()

	client, err := tenkisandbox.New(
		tenkisandbox.WithAuthToken("tk_test_api_key"),
		// Optional. Shown here only because the example uses a local test server.
		tenkisandbox.WithBaseURL(server.URL),
		tenkisandbox.WithHTTPClient(server.Client()),
	)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	session, err := client.Create(
		ctx,
		tenkisandbox.WithAllowInbound(true),
		tenkisandbox.WithEnvs(map[string]string{"OPENAI_API_KEY": "***"}),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(session.ID, session.State)

	// Output:
	// session-1 RUNNING
}
