package sandbox

import (
	"net/http"
	"testing"
)

func TestSetClientAuthHeadersAddsAuthorization(t *testing.T) {
	t.Parallel()

	header := http.Header{}
	setClientAuthHeaders(header, "tk_test")

	if got := header.Get(headerAuthorization); got != "Bearer tk_test" {
		t.Fatalf("authorization = %q, want Bearer tk_test", got)
	}
}
