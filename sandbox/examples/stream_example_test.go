package examples_test

import (
	"context"
	"fmt"
	"io"

	tenkisandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
)

func ExampleSession_Stream() {
	server, closeServers := newExampleSandboxServer(&exampleSandboxHandler{})
	defer closeServers()

	client, err := tenkisandbox.New(
		tenkisandbox.WithAuthToken("tk_test_api_key"),
		// Optional. Shown here only because the example uses a local test server.
		tenkisandbox.WithBaseURL(server.URL),
		tenkisandbox.WithHTTPClient(server.Client()),
	)
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	session, err := client.Create(ctx)
	if err != nil {
		panic(err)
	}

	stream, err := session.Stream(ctx, "whoami") //nolint:staticcheck // SA1019: Preserve the documented legacy Stream example.
	if err != nil {
		panic(err)
	}

	for {
		output, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		fmt.Print(string(output.Data))
	}

	result, err := stream.Wait()
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Status, result.ExitCode)

	// Output:
	// sandbox
	// SUCCEEDED 0
}
