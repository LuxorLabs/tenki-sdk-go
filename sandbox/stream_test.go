package sandbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type streamTestHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	pauseSessionFn  func(*connect.Request[sandboxv1.PauseSessionRequest]) (*connect.Response[sandboxv1.PauseSessionResponse], error)
	resumeSessionFn func(*connect.Request[sandboxv1.ResumeSessionRequest]) (*connect.Response[sandboxv1.ResumeSessionResponse], error)
}

func (h *streamTestHandler) PauseSession(_ context.Context, req *connect.Request[sandboxv1.PauseSessionRequest]) (*connect.Response[sandboxv1.PauseSessionResponse], error) {
	if h.pauseSessionFn != nil {
		return h.pauseSessionFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *streamTestHandler) ResumeSession(_ context.Context, req *connect.Request[sandboxv1.ResumeSessionRequest]) (*connect.Response[sandboxv1.ResumeSessionResponse], error) {
	if h.resumeSessionFn != nil {
		return h.resumeSessionFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

type dataPlaneRunTestHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	t *testing.T

	mu                sync.Mutex
	starts            []*sandboxv1.RunStart
	statPaths         []string
	statProbeHeaders  []string
	runErrorsByCwd    map[string]error
	requireStdinClose bool
	requireSignal     bool
	followups         []string
	signals           []sandboxv1.RunSignal_Sig
	stdinCloseSeen    chan struct{}
}

func (h *dataPlaneRunTestHandler) Stat(
	_ context.Context,
	req *connect.Request[sandboxv1.SandboxSessionDataPlaneServiceStatRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceStatResponse], error) {
	h.mu.Lock()
	h.statPaths = append(h.statPaths, req.Msg.GetRequest().GetPath())
	h.statProbeHeaders = append(h.statProbeHeaders, req.Header().Get(dataPlaneProbeHeader))
	h.mu.Unlock()
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceStatResponse{
		Response: &sandboxv1.StatResponse{Exists: true, IsDir: true},
	}), nil
}

func (h *dataPlaneRunTestHandler) Run(
	ctx context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
) error {
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	start := first.GetFrame().GetStart()
	if start == nil {
		h.t.Fatal("first run frame must be start")
	}
	h.mu.Lock()
	h.starts = append(h.starts, start)
	runErr := h.runErrorsByCwd[start.GetCwd()]
	h.mu.Unlock()
	if runErr != nil {
		return runErr
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{Pid: 42}},
	}}); err != nil {
		return err
	}
	if h.requireStdinClose {
		for {
			next, err := stream.Receive()
			if err != nil {
				return err
			}
			kind := runRequestPayloadKind(next.GetFrame())
			h.mu.Lock()
			h.followups = append(h.followups, kind)
			h.mu.Unlock()
			if next.GetFrame().GetStdinClose() {
				if h.stdinCloseSeen != nil {
					close(h.stdinCloseSeen)
				}
				break
			}
		}
	}
	if h.requireSignal {
		next, err := stream.Receive()
		if err != nil {
			return err
		}
		kind := runRequestPayloadKind(next.GetFrame())
		signal := next.GetFrame().GetSignal()
		if signal == nil {
			return errors.New("expected run signal")
		}
		h.mu.Lock()
		h.followups = append(h.followups, kind)
		h.signals = append(h.signals, signal.GetSignal())
		h.mu.Unlock()
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Stdout{Stdout: []byte("out")},
	}}); err != nil {
		return err
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Stderr{Stderr: []byte("err")},
	}}); err != nil {
		return err
	}
	return stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Exit{Exit: &sandboxv1.RunExit{ExitCode: 0, DurationMs: 12}},
	}})
}

func runRequestPayloadKind(req *sandboxv1.RunRequest) string {
	switch req.GetPayload().(type) {
	case *sandboxv1.RunRequest_Start:
		return "start"
	case *sandboxv1.RunRequest_Stdin:
		return "stdin"
	case *sandboxv1.RunRequest_StdinClose:
		return "stdin_close"
	case *sandboxv1.RunRequest_Signal:
		return "signal"
	default:
		return "unknown"
	}
}

func newStreamTestClient(t *testing.T, h *streamTestHandler) *Client {
	t.Helper()

	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(h)
	mux.Handle(path, svc)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newDataPlaneRunTestServer(t *testing.T) (*dataPlaneRunTestHandler, string) {
	t.Helper()

	handler := &dataPlaneRunTestHandler{t: t}
	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(handler)
	mux.Handle(path, svc)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	return handler, server.URL
}

func newStreamTestSession(client *Client, endpoint string) *Session {
	session := &Session{client: client, ID: "session-1"}
	session.configureDataPlane(endpoint, &sandboxv1.SessionCredential{
		Credential: "test-session-cert",
		ExpiresAt:  timestamppb.New(time.Now().Add(time.Hour)),
	})
	return session
}

func TestSessionStreamUsesDataPlaneRun(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	client := newStreamTestClient(t, &streamTestHandler{})
	stream, err := newStreamTestSession(client, endpoint).Stream(
		context.Background(),
		"echo",
		WithArgs("hello"),
		WithEnv("FOO", "bar"),
		WithTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	outputs := []Output{first, second}
	if !hasOutput(outputs, "out", false) || !hasOutput(outputs, "err", true) {
		t.Fatalf("unexpected outputs: %#v", outputs)
	}

	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}

	result, err := stream.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.ExitCode != 0 || result.Duration != 12*time.Millisecond || result.Status != CommandStatusSucceeded {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runHandler.statPaths) != 1 || runHandler.statPaths[0] != "/home/tenki" {
		t.Fatalf("expected readiness stat on /home/tenki, got %#v", runHandler.statPaths)
	}
	if len(runHandler.starts) != 1 {
		t.Fatalf("expected one data-plane run start, got %d", len(runHandler.starts))
	}
	start := runHandler.starts[0]
	if start.GetSessionId() != "session-1" || start.GetCmd()[0] != "echo" || start.GetCmd()[1] != "hello" {
		t.Fatalf("unexpected data-plane run start: %#v", start)
	}
	if start.GetEnv()["FOO"] != "bar" || start.GetTimeoutMs() == 0 {
		t.Fatalf("unexpected data-plane run options: %#v", start)
	}
}

func hasOutput(outputs []Output, data string, stderr bool) bool {
	for _, output := range outputs {
		if string(output.Data) == data && output.IsStderr == stderr && !output.IsFinal {
			return true
		}
	}
	return false
}

func TestSessionExecAggregatesDataPlaneRunOutput(t *testing.T) {
	t.Parallel()

	_, endpoint := newDataPlaneRunTestServer(t)
	client := newStreamTestClient(t, &streamTestHandler{})
	var callbackOutputs []Output
	result, err := newStreamTestSession(client, endpoint).Exec(context.Background(), "echo", WithOnOutput(func(output Output) {
		callbackOutputs = append(callbackOutputs, output)
	}))
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if string(result.Stdout) != "out" || string(result.Stderr) != "err" {
		t.Fatalf("unexpected aggregated output: %#v", result)
	}
	if len(result.Outputs) != 2 || len(callbackOutputs) != 2 {
		t.Fatalf("unexpected outputs: result=%#v callback=%#v", result.Outputs, callbackOutputs)
	}
}

func TestSessionExecPassesCwd(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	client := newStreamTestClient(t, &streamTestHandler{})

	if _, err := newStreamTestSession(client, endpoint).Exec(context.Background(), "pwd", WithDir("project")); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if got := firstRunStartCwd(t, runHandler); got != "project" {
		t.Fatalf("cwd = %q, want project", got)
	}
}

func TestSessionStreamPassesAbsoluteCwd(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	client := newStreamTestClient(t, &streamTestHandler{})

	stream, err := newStreamTestSession(client, endpoint).Stream(context.Background(), "pwd", WithDir("/home/tenki/project"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, err := stream.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if got := firstRunStartCwd(t, runHandler); got != "/home/tenki/project" {
		t.Fatalf("cwd = %q, want /home/tenki/project", got)
	}
}

func TestSessionCommandPassesCwd(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	client := newStreamTestClient(t, &streamTestHandler{})

	result, err := newStreamTestSession(client, endpoint).Command([]string{"pwd"}, RunOptions{Dir: "project"}).Exec(context.Background())
	if err != nil {
		t.Fatalf("Command Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}

	if got := firstRunStartCwd(t, runHandler); got != "project" {
		t.Fatalf("cwd = %q, want project", got)
	}
}

func TestSessionExecMissingCwdMapsFileNotFound(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	runHandler.runErrorsByCwd = map[string]error{
		"missing": connect.NewError(connect.CodeNotFound, errors.New("cwd /home/tenki/missing: no such file or directory")),
	}
	client := newStreamTestClient(t, &streamTestHandler{})

	_, err := newStreamTestSession(client, endpoint).Exec(context.Background(), "pwd", WithDir("missing"))
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("Exec error = %v, want ErrFileNotFound", err)
	}
	if got := firstRunStartCwd(t, runHandler); got != "missing" {
		t.Fatalf("cwd = %q, want missing", got)
	}
}

func TestSessionExecTraversalCwdPassesThroughAndSurfacesError(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	runHandler.runErrorsByCwd = map[string]error{
		"../outside": connect.NewError(connect.CodeInvalidArgument, errors.New("cwd ../outside: invalid argument")),
	}
	client := newStreamTestClient(t, &streamTestHandler{})

	_, err := newStreamTestSession(client, endpoint).Exec(context.Background(), "pwd", WithDir("../outside"))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Exec error = %v, want invalid argument", err)
	}
	if got := firstRunStartCwd(t, runHandler); got != "../outside" {
		t.Fatalf("cwd = %q, want ../outside", got)
	}
}

func TestSessionExecClosesRunStdin(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	runHandler.requireStdinClose = true
	client := newStreamTestClient(t, &streamTestHandler{})

	result, err := newStreamTestSession(client, endpoint).Exec(context.Background(), "cat")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}

	runHandler.mu.Lock()
	followups := append([]string(nil), runHandler.followups...)
	runHandler.mu.Unlock()
	if len(followups) != 1 || followups[0] != "stdin_close" {
		t.Fatalf("expected stdin_close followup, got %#v", followups)
	}
}

func TestSessionStreamClosesRunStdin(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	runHandler.requireStdinClose = true
	client := newStreamTestClient(t, &streamTestHandler{})

	stream, err := newStreamTestSession(client, endpoint).Stream(context.Background(), "cat")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, err := stream.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	runHandler.mu.Lock()
	followups := append([]string(nil), runHandler.followups...)
	runHandler.mu.Unlock()
	if len(followups) != 1 || followups[0] != "stdin_close" {
		t.Fatalf("expected stdin_close followup, got %#v", followups)
	}
}

func TestRunHandleSignalAfterStdinClose(t *testing.T) {
	t.Parallel()

	runHandler, endpoint := newDataPlaneRunTestServer(t)
	runHandler.requireStdinClose = true
	runHandler.requireSignal = true
	runHandler.stdinCloseSeen = make(chan struct{})
	client := newStreamTestClient(t, &streamTestHandler{})

	proc, err := newStreamTestSession(client, endpoint).Command([]string{"cat"}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var drainWG sync.WaitGroup
	drainWG.Add(2)
	go func() {
		defer drainWG.Done()
		_, _ = io.Copy(io.Discard, proc.Stdout)
	}()
	go func() {
		defer drainWG.Done()
		_, _ = io.Copy(io.Discard, proc.Stderr)
	}()
	if err := proc.Stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	select {
	case <-runHandler.stdinCloseSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stdin_close")
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal after stdin close: %v", err)
	}
	if _, err := proc.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drainWG.Wait()

	runHandler.mu.Lock()
	followups := append([]string(nil), runHandler.followups...)
	signals := append([]sandboxv1.RunSignal_Sig(nil), runHandler.signals...)
	runHandler.mu.Unlock()
	if len(followups) != 2 || followups[0] != "stdin_close" || followups[1] != "signal" {
		t.Fatalf("expected stdin_close then signal, got %#v", followups)
	}
	if len(signals) != 1 || signals[0] != sandboxv1.RunSignal_SIG_INT {
		t.Fatalf("expected SIG_INT, got %#v", signals)
	}
}

func TestSessionStreamRejectsWithOnOutput(t *testing.T) {
	t.Parallel()

	_, endpoint := newDataPlaneRunTestServer(t)
	client := newStreamTestClient(t, &streamTestHandler{})
	_, err := newStreamTestSession(client, endpoint).Stream(context.Background(), "echo", WithOnOutput(func(Output) {}))
	if err == nil {
		t.Fatal("expected error")
	}
}

func firstRunStartCwd(t *testing.T, h *dataPlaneRunTestHandler) string {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.starts) == 0 {
		t.Fatal("expected run start")
	}
	return h.starts[0].GetCwd()
}

func TestSessionPauseResume(t *testing.T) {
	t.Parallel()

	h := &streamTestHandler{}
	h.pauseSessionFn = func(req *connect.Request[sandboxv1.PauseSessionRequest]) (*connect.Response[sandboxv1.PauseSessionResponse], error) {
		if req.Msg.GetSessionId() != "session-1" {
			t.Fatalf("unexpected pause session id: %q", req.Msg.GetSessionId())
		}
		return connect.NewResponse(&sandboxv1.PauseSessionResponse{Session: &sandboxv1.SandboxSession{Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_PAUSED}}), nil
	}
	h.resumeSessionFn = func(req *connect.Request[sandboxv1.ResumeSessionRequest]) (*connect.Response[sandboxv1.ResumeSessionResponse], error) {
		if req.Msg.GetSessionId() != "session-1" {
			t.Fatalf("unexpected resume session id: %q", req.Msg.GetSessionId())
		}
		return connect.NewResponse(&sandboxv1.ResumeSessionResponse{Session: &sandboxv1.SandboxSession{Id: "session-1", State: sandboxv1.SessionState_SESSION_STATE_RUNNING}}), nil
	}

	client := newStreamTestClient(t, h)
	_, endpoint := newDataPlaneRunTestServer(t)
	session := newStreamTestSession(client, endpoint)
	session.markCurrentDataPlaneVerified()
	if err := session.Pause(context.Background()); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if session.State != SessionStatePaused {
		t.Fatalf("unexpected paused state: %q", session.State)
	}
	assertSessionDataPlaneReset(t, session)

	session.configureDataPlane(endpoint, &sandboxv1.SessionCredential{
		Credential: "resumed-session-cert",
		ExpiresAt:  timestamppb.New(time.Now().Add(time.Hour)),
	})
	session.markCurrentDataPlaneVerified()
	if err := session.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if session.State != SessionStateRunning {
		t.Fatalf("unexpected resumed state: %q", session.State)
	}
	assertSessionDataPlaneReset(t, session)
}

func assertSessionDataPlaneReset(t *testing.T, session *Session) {
	t.Helper()
	session.dataPlaneMu.RLock()
	endpoint := session.dataPlaneEndpoint
	credential := session.dataPlaneCredential
	expiresAt := session.dataPlaneExpiresAt
	client := session.dataPlaneClient
	renewalCancel := session.renewalCancel
	session.dataPlaneMu.RUnlock()
	if endpoint != "" || credential != "" || !expiresAt.IsZero() || client != nil || renewalCancel != nil {
		t.Fatalf("data-plane not reset: endpoint=%q credential=%q expiresAt=%v clientNil=%t renewalNil=%t", endpoint, credential, expiresAt, client == nil, renewalCancel == nil)
	}
	session.dataPlaneReadyMu.Lock()
	verified := session.dataPlaneVerifiedClient
	session.dataPlaneReadyMu.Unlock()
	if verified != nil {
		t.Fatal("verified data-plane client was not reset")
	}
}
