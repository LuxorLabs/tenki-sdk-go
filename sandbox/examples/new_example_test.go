package examples_test

import (
	"time"

	tenkisandbox "go.luxor.ai/sandbox"
)

func ExampleNew() {
	client, err := tenkisandbox.New(
		tenkisandbox.WithAuthToken("tk_test_api_key"),
		tenkisandbox.WithHTTPTimeout(10*time.Second),
	)
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()
}
