package sandbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

func TestFormattingRedactsCredentials(t *testing.T) {
	t.Parallel()
	const authToken = "tk_dummy-auth-token-private"
	const credential = "dummy-session-credential-private"
	client, err := New(WithAuthToken(authToken), WithBaseURL("http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	session := &Session{client: client, ID: "session-privacy", State: SessionStateRunning, dataPlaneCredential: newSecret(credential)}
	session.Git = &Git{session: session}

	var logs bytes.Buffer
	textLogger := slog.New(slog.NewTextHandler(&logs, nil))
	jsonLogger := slog.New(slog.NewJSONHandler(&logs, nil))
	for _, logger := range []*slog.Logger{textLogger, jsonLogger} {
		logger.Info("diagnostics", "client", client, "session", session)
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}

	rendered := []string{logs.String(), string(encoded)}
	for _, value := range []any{client, session} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			rendered = append(rendered, fmt.Sprintf(verb, value))
		}
		rendered = append(rendered, fmt.Sprint(value), fmt.Sprintf("%v", []any{value}), fmt.Sprintf("%+v", struct{ V any }{value}))
	}
	rendered = append(rendered, client.String(), client.GoString(), session.String(), session.GoString())
	// Struct values bypass the pointer-receiver Format; reflect avoids vet's copylocks check.
	for _, value := range []any{reflect.ValueOf(client).Elem().Interface(), reflect.ValueOf(session).Elem().Interface()} {
		rendered = append(rendered, fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value))
	}
	key := newSecret(authToken)
	rendered = append(rendered, fmt.Sprint(key), fmt.Sprintf("%#v", key), key.String(), key.GoString(), key.LogValue().String())
	for _, output := range rendered {
		if strings.Contains(output, authToken) || strings.Contains(output, credential) {
			t.Fatalf("formatted output contains credential: %q", output)
		}
	}

	if got := fmt.Sprint(client); !strings.Contains(got, "http://127.0.0.1:1") || !strings.Contains(got, redacted) {
		t.Fatalf("client format = %q, want base URL and redaction marker", got)
	}
	if got := fmt.Sprintf("%+v", session); !strings.Contains(got, "ID:session-privacy") || !strings.Contains(got, "State:RUNNING") {
		t.Fatalf("session format = %q, want exported fields", got)
	}
	if got := fmt.Sprintf("%#v", session); !strings.HasPrefix(got, `&sandbox.Session{`) || !strings.Contains(got, `ID:"session-privacy"`) {
		t.Fatalf("session Go syntax = %q, want exported fields", got)
	}
	if !strings.Contains(logs.String(), `"ID":"session-privacy"`) || !strings.Contains(logs.String(), `"State":"RUNNING"`) {
		t.Fatalf("JSON log = %q, want every exported session field", logs.String())
	}
	if key.reveal() != authToken || newSecret("").reveal() != "" {
		t.Fatal("secret does not round-trip its value")
	}
	var nilClient *Client
	var nilSession *Session
	if got := fmt.Sprint(nilClient, nilSession); got != "<nil> <nil>" {
		t.Fatalf("nil format = %q", got)
	}
}
