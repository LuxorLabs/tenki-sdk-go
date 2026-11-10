package sandbox

import (
	"net/http"
	"strings"
	"testing"
)

func TestSetClientAuthHeadersAddsAuthorization(t *testing.T) {
	t.Parallel()

	header := http.Header{}
	setClientAuthHeaders(header, "tk_test")

	if got := header.Get(headerAuthorization); got != "Bearer tk_test" {
		t.Fatalf("authorization = %q, want Bearer tk_test", got)
	}
	assertClientMetadata(t, header)
}

func TestSetClientAuthHeadersAddsSDKIdentity(t *testing.T) {
	t.Parallel()

	header := http.Header{}
	setClientAuthHeaders(header, "lx_test")

	if got := header.Get("User-Agent"); !strings.HasPrefix(got, "luxor-sandbox-go/") {
		t.Fatalf("user-agent = %q, want luxor-sandbox-go/<version>", got)
	}

	custom := http.Header{"User-Agent": []string{"my-agent/1.0"}}
	setClientAuthHeaders(custom, "lx_test")
	if got := custom.Get("User-Agent"); !strings.HasPrefix(got, "luxor-sandbox-go/") || !strings.HasSuffix(got, " my-agent/1.0") {
		t.Fatalf("user-agent = %q, want SDK product followed by caller value", got)
	}
	setClientAuthHeaders(custom, "lx_test")
	if got := custom.Get("User-Agent"); strings.Count(got, "luxor-sandbox-go/") != 1 {
		t.Fatalf("user-agent = %q, want SDK product once", got)
	}
}
