package sandbox

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// plaintextRecorder is a raw TCP listener that counts connections and bytes.
type plaintextRecorder struct {
	listener net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    int
	bytes    int
}

func newPlaintextRecorder(t *testing.T) *plaintextRecorder {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &plaintextRecorder{listener: listener}
	recorder.wg.Add(1)
	go func() {
		defer recorder.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			recorder.mu.Lock()
			recorder.conns++
			recorder.mu.Unlock()
			recorder.wg.Add(1)
			go func() {
				defer recorder.wg.Done()
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				n, _ := io.Copy(io.Discard, conn)
				recorder.mu.Lock()
				recorder.bytes += int(n)
				recorder.mu.Unlock()
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return recorder
}

func (r *plaintextRecorder) url() string { return "http://" + r.listener.Addr().String() + "/rpc" }

func (r *plaintextRecorder) assertUntouched(t *testing.T) {
	t.Helper()
	_ = r.listener.Close()
	r.wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns != 0 || r.bytes != 0 {
		t.Fatalf("plaintext server got %d connections and %d bytes", r.conns, r.bytes)
	}
}

func postSecret(client *http.Client, target string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader("request body"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer tk_test")
	return client.Do(req)
}

func TestHTTPSTransportRefusesUnencryptedRequests(t *testing.T) {
	builders := map[string]func(baseURL string) *http.Client{
		"control plane": func(baseURL string) *http.Client {
			return &http.Client{Timeout: 5 * time.Second, Transport: newHTTP2Transport(baseURL)}
		},
		"data plane": func(baseURL string) *http.Client {
			client := newDataPlaneHTTPClient(baseURL)
			client.Timeout = 5 * time.Second
			return client
		},
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			recorder := newPlaintextRecorder(t)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ok" {
					_, _ = io.WriteString(w, r.Proto)
					return
				}
				http.Redirect(w, r, recorder.url(), http.StatusTemporaryRedirect)
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			t.Cleanup(server.Close)
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())

			client := build(server.URL)
			client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots}
			defer client.CloseIdleConnections()

			response, err := postSecret(client, server.URL+"/ok")
			if err != nil {
				t.Fatalf("TLS request: %v", err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if string(body) != "HTTP/2.0" {
				t.Fatalf("TLS request used %q, want HTTP/2.0", body)
			}

			for _, target := range []string{recorder.url(), server.URL + "/redirect"} {
				response, err := postSecret(client, target)
				if err == nil {
					_ = response.Body.Close()
					t.Fatalf("POST %s succeeded, want refusal", target)
				}
				if !errors.Is(err, errUnencryptedHTTP2) {
					t.Fatalf("POST %s: got %v, want %v", target, err, errUnencryptedHTTP2)
				}
			}
			recorder.assertUntouched(t)
		})
	}
}

func TestClientHTTPSBaseURLRefusesUnencryptedRequests(t *testing.T) {
	recorder := newPlaintextRecorder(t)
	client, err := New(WithAuthToken("tk_test"), WithBaseURL("https://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := postSecret(client.httpClient, recorder.url())
	if err == nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, errUnencryptedHTTP2) {
		t.Fatalf("got %v, want %v", err, errUnencryptedHTTP2)
	}
	recorder.assertUntouched(t)
}

func TestClientHTTPBaseURLUsesH2C(t *testing.T) {
	server := h2cTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	defer server.Close()
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := postSecret(client.httpClient, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "HTTP/2.0" {
		t.Fatalf("got %q, want HTTP/2.0", body)
	}
}
