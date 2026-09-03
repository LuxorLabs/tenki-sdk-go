package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type renewalSandboxHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	calls atomic.Int32
}

func (h *renewalSandboxHandler) CreateSessionCredential(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateSessionCredentialRequest],
) (*connect.Response[sandboxv1.CreateSessionCredentialResponse], error) {
	if got := req.Header().Get(headerAuthorization); got != "Bearer tk_test" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing original auth"))
	}
	n := h.calls.Add(1)
	if n == 2 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("session paused"))
	}
	return connect.NewResponse(&sandboxv1.CreateSessionCredentialResponse{
		Credential: testSessionCredential("renewed", time.Now().Add(60*time.Millisecond)),
	}), nil
}

type renewalDataPlaneHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
}

func (h *renewalDataPlaneHandler) Run(
	_ context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
) error {
	if got := stream.RequestHeader().Get(dataPlaneCredentialHeader); got != "initial" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("unexpected session credential"))
	}
	if _, err := stream.Receive(); err != nil {
		return err
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{Pid: 1}},
	}}); err != nil {
		return err
	}
	for i := 0; i < 5; i++ {
		time.Sleep(30 * time.Millisecond)
		if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
			Payload: &sandboxv1.RunResponse_Stdout{Stdout: []byte("x")},
		}}); err != nil {
			return err
		}
	}
	return stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Exit{Exit: &sandboxv1.RunExit{ExitCode: 0}},
	}})
}

func TestRenewalContinuityAcrossPausedMintRejection(t *testing.T) {
	t.Parallel()

	engine := &renewalSandboxHandler{}
	mux := http.NewServeMux()
	servicePath, serviceHandler := sandboxv1connect.NewSandboxServiceHandler(engine)
	mux.Handle(servicePath, serviceHandler)
	dataPath, dataHandler := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(&renewalDataPlaneHandler{})
	mux.Handle(dataPath, dataHandler)

	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)

	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	session := &Session{client: client, ID: "session-1"}
	session.configureDataPlane(server.URL, testSessionCredential("initial", time.Now().Add(60*time.Millisecond)), false)
	result, err := session.Command([]string{"echo"}).Exec(context.Background())
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := string(result.Stdout); got != "xxxxx" {
		t.Fatalf("stdout got %q", got)
	}
	deadline := time.Now().Add(time.Second)
	for engine.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := engine.calls.Load(); got < 3 {
		t.Fatalf("renewals got %d, want >=3", got)
	}
}

func testSessionCredential(value string, expiresAt time.Time) *sandboxv1.SessionCredential {
	return &sandboxv1.SessionCredential{
		Credential: value,
		ExpiresAt:  timestamppb.New(expiresAt),
	}
}
