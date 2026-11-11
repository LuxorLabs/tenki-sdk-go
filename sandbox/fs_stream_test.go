package sandbox

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

type rejectingWriteDataPlane struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
}

func (rejectingWriteDataPlane) WriteFileStream(
	context.Context,
	*connect.ClientStream[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, errors.New("upstream unavailable"))
}

// acceptingEarlyWriteDataPlane ends the stream successfully without reading the client's data.
type acceptingEarlyWriteDataPlane struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
}

func (acceptingEarlyWriteDataPlane) WriteFileStream(
	context.Context,
	*connect.ClientStream[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse], error) {
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse{}), nil
}

// consumingWriteDataPlane reads the stream to the half-close and reports the data it received.
type consumingWriteDataPlane struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	received chan []byte
}

func (d consumingWriteDataPlane) WriteFileStream(
	_ context.Context,
	stream *connect.ClientStream[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse], error) {
	var data []byte
	for stream.Receive() {
		data = append(data, stream.Msg().GetFrame().GetData()...)
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	d.received <- data
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse{}), nil
}

func newTestFileStreamWriter(t *testing.T, handler sandboxv1connect.SandboxSessionDataPlaneServiceHandler) *fileStreamWriter {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(handler))
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	httpClient := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	stream := sandboxv1connect.NewSandboxSessionDataPlaneServiceClient(httpClient, server.URL).WriteFileStream(context.Background())
	return &fileStreamWriter{stream: stream}
}

// writeUntilError writes until the writer observes that the server ended the stream.
func writeUntilError(writer *fileStreamWriter) error {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := writer.Write([]byte("chunk")); err != nil {
			return err
		}
	}
	return nil
}

func TestFileStreamWriterFailsWhenServerEndsStreamEarly(t *testing.T) {
	writer := newTestFileStreamWriter(t, acceptingEarlyWriteDataPlane{})
	err := writeUntilError(writer)
	if err == nil {
		t.Fatal("writes kept succeeding after the server ended the stream")
	}
	if errors.Is(err, io.EOF) || !errors.Is(err, ErrStreamClosed) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err=%v, want ErrStreamClosed and io.ErrClosedPipe for data the server never received", err)
	}
}

func TestFileStreamWriterReturnsServerErrorAfterEarlyReject(t *testing.T) {
	writer := newTestFileStreamWriter(t, rejectingWriteDataPlane{})
	err := writeUntilError(writer)
	if err == nil {
		t.Fatal("writes kept succeeding after the server rejected the stream")
	}
	if code := connect.CodeOf(err); code != connect.CodeUnavailable {
		t.Fatalf("code=%v err=%v, want the server's unavailable error instead of a bare EOF", code, err)
	}
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("close after a failed write: %v", closeErr)
	}
}

func TestFileStreamWriterSucceedsWhenServerConsumesAllData(t *testing.T) {
	handler := consumingWriteDataPlane{received: make(chan []byte, 1)}
	writer := newTestFileStreamWriter(t, handler)
	for _, chunk := range []string{"foo", "bar", "baz"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatalf("write %q: %v", chunk, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := string(<-handler.received); got != "foobarbaz" {
		t.Fatalf("server received %q, want %q", got, "foobarbaz")
	}
}
