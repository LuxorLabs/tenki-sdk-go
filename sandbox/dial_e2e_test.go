//go:build sdk_e2e

package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	sandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
)

// Mirrors packages/e2e/src/sandbox/dial_*.test.ts. Run with:
//   SANDBOX_E2E=1 TENKI_AUTH_TOKEN=... TENKI_API_ENDPOINT=... TENKI_SANDBOX_GATEWAY_URL=... \
//   TENKI_SANDBOX_WORKSPACE_ID=... \
//   go test -tags=sdk_e2e -run TestDial ./sdk/sandbox/go/...

func newSession(t *testing.T) (*sandbox.Session, func()) {
	t.Helper()
	if os.Getenv("SANDBOX_E2E") != "1" {
		t.Skip("set SANDBOX_E2E=1 to run sandbox SDK e2e tests")
	}

	opts := []sandbox.Option{
		sandbox.WithCookieName(envOr("TENKI_COOKIE_NAME", "ory_kratos_session")),
	}
	client, err := sandbox.New(opts...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	createOpts := []sandbox.CreateOption{
		sandbox.WithName(fmt.Sprintf("dial-go-e2e-%d", time.Now().UnixNano())),
		sandbox.WithMaxDuration(20 * time.Minute),
		sandbox.WithIdleTimeout(10 * time.Minute),
	}
	if ws := os.Getenv("TENKI_SANDBOX_WORKSPACE_ID"); ws != "" {
		createOpts = append(createOpts, sandbox.WithWorkspaceID(ws))
	}
	if image := os.Getenv("TENKI_SANDBOX_IMAGE"); image != "" {
		createOpts = append(createOpts, sandbox.WithImage(image))
	}
	if sid := os.Getenv("TENKI_SANDBOX_SNAPSHOT_ID"); sid != "" {
		createOpts = append(createOpts, sandbox.WithSnapshot(sid))
	}

	session, err := client.CreateAndWait(ctx, 5*time.Minute, createOpts...)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cleanup := func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		_ = session.CloseIfOpen(closeCtx)
		_ = client.Close()
	}
	return session, cleanup
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestDialRejectsPathsOutsideAllowlist(t *testing.T) {
	session, cleanup := newSession(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := session.Dial(ctx, "/etc/passwd", sandbox.DialOptions{ConnectTimeout: time.Second})
	if err == nil {
		t.Fatal("expected dial to fail for /etc/passwd")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "path_denied") &&
		!strings.Contains(strings.ToLower(err.Error()), "denied") {
		t.Fatalf("expected path_denied error, got: %v", err)
	}
}

func TestDialRejectsSymlinkEscape(t *testing.T) {
	session, cleanup := newSession(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := session.Exec(ctx, "sh", sandbox.WithArgs("-lc", "ln -sf /etc/passwd /home/tenki/escape.sock"), sandbox.WithTimeout(5*time.Second)); err != nil {
		t.Fatalf("setup symlink: %v", err)
	}

	_, err := session.Dial(ctx, "/home/tenki/escape.sock", sandbox.DialOptions{ConnectTimeout: time.Second})
	if err == nil {
		t.Fatal("expected dial to fail for symlink escape")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "path_denied") &&
		!strings.Contains(strings.ToLower(err.Error()), "denied") {
		t.Fatalf("expected path_denied error, got: %v", err)
	}
}

func TestDialReportsRefusedWhenNoListener(t *testing.T) {
	session, cleanup := newSession(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bind := "rm -f /home/tenki/stale.sock && python3 -c \"import socket; s=socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.bind('/home/tenki/stale.sock'); s.listen(1)\""
	if _, err := session.Exec(ctx, "sh", sandbox.WithArgs("-lc", bind), sandbox.WithTimeout(10*time.Second)); err != nil {
		t.Fatalf("bind stale socket: %v", err)
	}

	_, err := session.Dial(ctx, "/home/tenki/stale.sock", sandbox.DialOptions{ConnectTimeout: time.Second})
	if err == nil {
		t.Fatal("expected dial to fail for stale socket")
	}
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "refused") && !strings.Contains(low, "connection") && !strings.Contains(low, "dial") {
		t.Fatalf("expected refused error, got: %v", err)
	}
}

func TestDialSurfacesPeerResetWhenDaemonExits(t *testing.T) {
	session, cleanup := newSession(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const socketPath = "/home/tenki/dial_reset_go.sock"
	const script = `#!/usr/bin/env python3
import os, socket, sys
path = sys.argv[1]
try: os.unlink(path)
except FileNotFoundError: pass
server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(path)
server.listen(1)
conn, _ = server.accept()
conn.close()
server.close()
`
	if err := session.WriteFile(ctx, "/home/tenki/dial_reset_go.py", []byte(script)); err != nil {
		t.Fatalf("write script: %v", err)
	}
	launch := fmt.Sprintf(
		"chmod +x /home/tenki/dial_reset_go.py && rm -f %q && nohup python3 /home/tenki/dial_reset_go.py %q >/tmp/dial_reset_go.log 2>&1 &",
		socketPath, socketPath,
	)
	if _, err := session.Exec(ctx, "sh", sandbox.WithArgs("-lc", launch), sandbox.WithTimeout(10*time.Second)); err != nil {
		t.Fatalf("launch daemon: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		res, err := session.Exec(ctx, "sh",
			sandbox.WithArgs("-lc", fmt.Sprintf("test -S %q && echo ready || true", socketPath)),
			sandbox.WithTimeout(5*time.Second))
		if err == nil && strings.TrimSpace(string(res.Stdout)) == "ready" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	conn, err := session.Dial(ctx, socketPath, sandbox.DialOptions{ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("boom"))
	buf := make([]byte, 64)
	_, readErr := conn.Read(buf)
	if readErr == nil || (!errors.Is(readErr, io.EOF) && !strings.Contains(strings.ToLower(readErr.Error()), "closed")) {
		t.Fatalf("expected EOF/closed after daemon exit, got: %v", readErr)
	}
}
