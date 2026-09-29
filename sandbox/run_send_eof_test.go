package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type sendEOFRunStream struct {
	receiveResponse *sandboxv1.SandboxSessionDataPlaneServiceRunResponse
	receiveErr      error
	receiveCalls    int
}

func (*sendEOFRunStream) Send(*sandboxv1.SandboxSessionDataPlaneServiceRunRequest) error {
	return io.EOF
}

func (s *sendEOFRunStream) Receive() (*sandboxv1.SandboxSessionDataPlaneServiceRunResponse, error) {
	s.receiveCalls++
	return s.receiveResponse, s.receiveErr
}

func TestRunStreamSendErrorLeavesNonEOFUntouched(t *testing.T) {
	want := errors.New("send failed")
	fake := &sendEOFRunStream{}
	stream := &dataPlaneRunStream{stream: fake}

	got := runStreamSendError(stream, want)
	if !errors.Is(got, want) {
		t.Fatalf("runStreamSendError() = %v, want %v", got, want)
	}
	if fake.receiveCalls != 0 {
		t.Fatalf("Receive calls got %d, want 0", fake.receiveCalls)
	}
}

func TestRunStreamSendErrorKeepsEOFWhenServerReturnedFrame(t *testing.T) {
	fake := &sendEOFRunStream{receiveResponse: &sandboxv1.SandboxSessionDataPlaneServiceRunResponse{}}
	stream := &dataPlaneRunStream{stream: fake}

	got := runStreamSendError(stream, io.EOF)
	if !errors.Is(got, io.EOF) {
		t.Fatalf("runStreamSendError() = %v, want EOF", got)
	}
	if isRetryableRunStreamEstablishmentError(got, false) {
		t.Fatal("ambiguous send EOF must remain terminal")
	}
}

func TestRunStreamSendErrorRecoversWrappedEOFServerStatus(t *testing.T) {
	want := connect.NewError(connect.CodeAborted, errors.New("read ECONNRESET"))
	stream := &dataPlaneRunStream{stream: &sendEOFRunStream{receiveErr: want}}

	got := runStreamSendError(stream, fmt.Errorf("write envelope: %w", io.EOF))
	if !errors.Is(got, want) {
		t.Fatalf("runStreamSendError() = %v, want %v", got, want)
	}
}

func TestRunStreamSendErrorDoesNotRetryTerminalServerStatus(t *testing.T) {
	want := connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	stream := &dataPlaneRunStream{stream: &sendEOFRunStream{receiveErr: want}}

	got := runStreamSendError(stream, io.EOF)
	if !errors.Is(got, want) {
		t.Fatalf("runStreamSendError() = %v, want %v", got, want)
	}
	if isRetryableRunStreamEstablishmentError(got, false) {
		t.Fatal("terminal permission error must not be retried")
	}
}

func (*sendEOFRunStream) CloseRequest() error { return nil }

func TestRunStreamSendErrorRecoversServerStatus(t *testing.T) {
	want := connect.NewError(connect.CodeAborted, errors.New("read ECONNRESET"))
	stream := &dataPlaneRunStream{stream: &sendEOFRunStream{receiveErr: want}}

	got := runStreamSendError(stream, io.EOF)
	if !errors.Is(got, want) {
		t.Fatalf("runStreamSendError() = %v, want %v", got, want)
	}
	if !isRetryableRunStreamEstablishmentError(got, false) {
		t.Fatal("recovered transient server status must remain retryable")
	}
}

func TestRunSendEOFRecoversHTTP2ServerRejection(t *testing.T) {
	serverHandler := &earlyRejectRunHandler{}
	_, handler := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(serverHandler)
	server := httptest.NewServer(h2c.NewHandler(handler, &http2.Server{}))
	defer server.Close()
	httpClient := newDataPlaneHTTPClient(server.URL)
	defer httpClient.CloseIdleConnections()
	dp := sandboxv1connect.NewSandboxSessionDataPlaneServiceClient(httpClient, server.URL, connect.WithGRPC())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rpc := dp.Run(ctx)
	defer rpc.CloseRequest()
	if err := rpc.Send(nil); err != nil {
		t.Fatal(err)
	}
	_ = rpc.ResponseHeader()
	stream := &dataPlaneRunStream{stream: rpc}
	request := &sandboxv1.RunRequest{Payload: &sandboxv1.RunRequest_Start{Start: &sandboxv1.RunStart{SessionId: "session-1", Cmd: []string{"true"}}}}
	sendErr := stream.Send(request)
	for sendErr == nil && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
		sendErr = stream.Send(request)
	}
	if !errors.Is(sendErr, io.EOF) {
		t.Fatalf("expected send EOF after early rejection, got %v", sendErr)
	}
	recovered := runStreamSendError(stream, sendErr)
	if connect.CodeOf(recovered) != connect.CodePermissionDenied {
		t.Fatalf("lost server status: %v", recovered)
	}
	if serverHandler.calls.Load() != 1 {
		t.Fatal("rejected command was retried")
	}
}

type earlyRejectRunHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	calls atomic.Int32
}

func (h *earlyRejectRunHandler) Run(context.Context, *connect.BidiStream[sandboxv1.SandboxSessionDataPlaneServiceRunRequest, sandboxv1.SandboxSessionDataPlaneServiceRunResponse]) error {
	h.calls.Add(1)
	return connect.NewError(connect.CodePermissionDenied, errors.New("command forbidden"))
}

func TestOpenRunStreamMapsEarlyServerRejection(t *testing.T) {
	serverHandler := &earlyRejectRunHandler{}
	_, handler := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(serverHandler)
	server := httptest.NewServer(h2c.NewHandler(handler, &http2.Server{}))
	defer server.Close()
	httpClient := newDataPlaneHTTPClient(server.URL)
	defer httpClient.CloseIdleConnections()
	dp := sandboxv1connect.NewSandboxSessionDataPlaneServiceClient(httpClient, server.URL, connect.WithGRPC())
	session := &Session{
		client: &Client{}, ID: "session-1", dataPlaneClient: dp,
		dataPlaneEndpoint: server.URL, dataPlaneCredential: "test",
		dataPlaneExpiresAt: time.Now().Add(time.Hour),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := &Command{session: session, argv: []string{"echo", strings.Repeat("x", 8<<20)}}
	_, _, err := command.openRunStream(ctx)
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("openRunStream() = %v, want ErrPermissionDenied", err)
	}
	if serverHandler.calls.Load() != 1 {
		t.Fatal("rejected command was retried")
	}
}

func TestRunStreamSendEOFPreservesAcceptedResponse(t *testing.T) {
	first := &sandboxv1.RunResponse{Payload: &sandboxv1.RunResponse_Started{Started: &sandboxv1.RunStarted{}}}
	fake := &sendEOFRunStream{receiveResponse: &sandboxv1.SandboxSessionDataPlaneServiceRunResponse{Frame: first}}
	stream := &dataPlaneRunStream{stream: fake}
	if err := runStreamSendError(stream, io.EOF); err != nil {
		t.Fatal(err)
	}
	got, err := stream.Receive()
	if err != nil || got != first {
		t.Fatalf("accepted response lost: %v, %v", got, err)
	}
	if stream.pending != nil {
		t.Fatal("response was not consumed")
	}
}
