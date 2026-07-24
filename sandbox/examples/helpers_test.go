package examples_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type exampleSandboxHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	t                 *testing.T
	expectedHeaderKey string
	expectedHeaderVal string
	executeCommandFn  func(*connect.Request[sandboxv1.ExecuteCommandRequest]) (*connect.Response[sandboxv1.ExecuteCommandResponse], error)
	dataPlaneEndpoint string
}

func (h *exampleSandboxHandler) CreateSession(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateSessionRequest],
) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	if h.t != nil {
		h.t.Helper()
		if got := req.Header().Get(h.expectedHeaderKey); got != h.expectedHeaderVal {
			h.t.Fatalf("unexpected %s header: %q", h.expectedHeaderKey, got)
		}
		if req.Msg.GetOwnerType() != "SERVICE" {
			h.t.Fatalf("unexpected owner_type: %q", req.Msg.GetOwnerType())
		}
		if req.Msg.GetOwnerId() != "self" {
			h.t.Fatalf("unexpected owner_id: %q", req.Msg.GetOwnerId())
		}
	}

	return connect.NewResponse(&sandboxv1.CreateSessionResponse{
		Session: &sandboxv1.SandboxSession{
			Id:        "session-1",
			State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
			OwnerType: "user",
			OwnerId:   "user-1",
		},
		DataPlaneEndpoint: h.dataPlaneEndpoint,
		Credential: &sandboxv1.SessionCredential{
			Credential: "test-session-cert",
			ExpiresAt:  timestamppb.New(time.Now().Add(time.Hour)),
		},
	}), nil
}

func (h *exampleSandboxHandler) ExecuteCommand(
	_ context.Context,
	req *connect.Request[sandboxv1.ExecuteCommandRequest],
) (*connect.Response[sandboxv1.ExecuteCommandResponse], error) {
	if h.t != nil {
		h.t.Helper()
		if req.Msg.GetSessionId() != "session-1" {
			h.t.Fatalf("unexpected session id: %q", req.Msg.GetSessionId())
		}
		if req.Msg.GetCommand() != "whoami" {
			h.t.Fatalf("unexpected command: %q", req.Msg.GetCommand())
		}
	}
	if h.executeCommandFn != nil {
		return h.executeCommandFn(req)
	}
	return connect.NewResponse(&sandboxv1.ExecuteCommandResponse{
		Execution: &sandboxv1.CommandExecution{
			Id:        "exec-1",
			SessionId: "session-1",
			Command:   req.Msg.GetCommand(),
			Status:    sandboxv1.CommandStatus_COMMAND_STATUS_SUCCEEDED,
			ExitCode:  0,
		},
	}), nil
}

func (h *exampleSandboxHandler) StreamCommandOutput(
	_ context.Context,
	req *connect.Request[sandboxv1.StreamCommandOutputRequest],
	stream *connect.ServerStream[sandboxv1.StreamCommandOutputResponse],
) error {
	if h.t != nil {
		h.t.Helper()
		if req.Msg.GetSessionId() != "session-1" {
			h.t.Fatalf("unexpected stream session id: %q", req.Msg.GetSessionId())
		}
		if req.Msg.GetExecutionId() != "exec-1" {
			h.t.Fatalf("unexpected execution id: %q", req.Msg.GetExecutionId())
		}
	}

	return stream.Send(&sandboxv1.StreamCommandOutputResponse{
		ExecutionId: "exec-1",
		Data:        []byte("sandbox\n"),
		IsFinal:     true,
	})
}

type exampleDataPlaneHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	t *testing.T
}

func (h *exampleDataPlaneHandler) Run(
	_ context.Context,
	stream *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse],
) error {
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	start := first.GetFrame().GetStart()
	if start == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("first frame must be start"))
	}
	if h.t != nil {
		h.t.Helper()
		if start.GetSessionId() != "session-1" {
			h.t.Fatalf("unexpected run session id: %q", start.GetSessionId())
		}
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{Pid: 42}},
	}}); err != nil {
		return err
	}
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Stdout{Stdout: []byte("sandbox\n")},
	}}); err != nil {
		return err
	}
	return stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: &sandboxv1.RunResponse{
		Payload: &sandboxv1.RunResponse_Exit{Exit: &sandboxv1.RunExit{ExitCode: 0, Reason: "exit"}},
	}})
}

func (h *exampleSandboxHandler) TerminateSession(
	_ context.Context,
	req *connect.Request[sandboxv1.TerminateSessionRequest],
) (*connect.Response[sandboxv1.TerminateSessionResponse], error) {
	if h.t != nil {
		h.t.Helper()
		if req.Msg.GetSessionId() != "session-1" {
			h.t.Fatalf("unexpected terminate session id: %q", req.Msg.GetSessionId())
		}
	}

	return connect.NewResponse(&sandboxv1.TerminateSessionResponse{
		Session: &sandboxv1.SandboxSession{
			Id:        "session-1",
			State:     sandboxv1.SessionState_SESSION_STATE_TERMINATED,
			OwnerType: "SERVICE",
			OwnerId:   "self",
		},
	}), nil
}

func newExampleSandboxServer(handler *exampleSandboxHandler) *httptest.Server {
	dataMux := http.NewServeMux()
	dataPath, dataSvc := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(&exampleDataPlaneHandler{t: handler.t})
	dataMux.Handle(dataPath, dataSvc)
	dataServer := httptest.NewServer(h2c.NewHandler(dataMux, &http2.Server{}))
	handler.dataPlaneEndpoint = dataServer.URL
	if handler.t != nil {
		handler.t.Cleanup(dataServer.Close)
	}

	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(handler)
	mux.Handle(path, svc)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	return server
}
