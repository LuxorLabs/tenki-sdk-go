package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// writeFaultHandler acknowledges writes with a skewed byte count, or rejects a stream after its first data frame.
type writeFaultHandler struct {
	sandboxv1connect.UnimplementedSandboxSessionDataPlaneServiceHandler
	delta     int64
	rejectErr *connect.Error
}

func (h *writeFaultHandler) WriteFileStream(_ context.Context,
	stream *connect.ClientStream[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse], error) {
	var received int64
	for stream.Receive() {
		received += int64(len(stream.Msg().GetFrame().GetData()))
		if h.rejectErr != nil && received > 0 {
			return nil, h.rejectErr
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse{
		Response: &sandboxv1.WriteFileStreamResponse{BytesWritten: received + h.delta},
	}), nil
}

func (h *writeFaultHandler) WriteFile(_ context.Context,
	req *connect.Request[sandboxv1.SandboxSessionDataPlaneServiceWriteFileRequest],
) (*connect.Response[sandboxv1.SandboxSessionDataPlaneServiceWriteFileResponse], error) {
	return connect.NewResponse(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileResponse{
		Response: &sandboxv1.WriteFileResponse{SizeBytes: int64(len(req.Msg.GetRequest().GetContent())) + h.delta},
	}), nil
}

func newWriteFaultSession(t *testing.T, handler *writeFaultHandler) *Session {
	t.Helper()
	mux := http.NewServeMux()
	path, service := sandboxv1connect.NewSandboxSessionDataPlaneServiceHandler(handler)
	mux.Handle(path, service)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)
	return newStreamTestSession(newStreamTestClient(t, &streamTestHandler{}), server.URL)
}

func TestWritesFailWhenTheSandboxSavesADifferentByteCount(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 64<<10)
	writes := map[string]func(context.Context, *Session) error{
		"WriteFile": func(ctx context.Context, s *Session) error {
			return s.WriteFile(ctx, "/home/tenki/out", data)
		},
		"WriteFileStream": func(ctx context.Context, s *Session) error {
			return s.WriteFileStream(ctx, "/home/tenki/out", bytes.NewReader(data))
		},
		"WriteStream": func(ctx context.Context, s *Session) error {
			w, err := s.WriteStream(ctx, "/home/tenki/out")
			if err != nil {
				return err
			}
			if _, err := w.Write(data); err != nil {
				return err
			}
			return w.Close()
		},
	}
	for name, write := range writes {
		for _, delta := range []int64{0, -1, 1, -int64(len(data))} {
			session := newWriteFaultSession(t, &writeFaultHandler{delta: delta})
			err := write(t.Context(), session)
			if delta == 0 {
				if err != nil {
					t.Fatalf("%s full write: %v", name, err)
				}
				continue
			}
			if !errors.Is(err, ErrIncompleteWrite) {
				t.Fatalf("%s delta %d: err = %v, want ErrIncompleteWrite", name, delta, err)
			}
		}
	}
}

func TestWriteFileStreamReturnsTheServerRejection(t *testing.T) {
	session := newWriteFaultSession(t, &writeFaultHandler{
		rejectErr: connect.NewError(connect.CodeResourceExhausted, errors.New("no space left on device")),
	})
	// Larger than the HTTP/2 send window, so Send runs into the rejected stream.
	err := session.WriteFileStream(t.Context(), "/home/tenki/out", io.LimitReader(zeroReader{}, 256<<20))
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want the server rejection instead of EOF", err)
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want resource_exhausted (err = %v)", connect.CodeOf(err), err)
	}
}

func TestDataLossMapsToErrIncompleteWriteOnlyForWrites(t *testing.T) {
	dataLoss := connect.NewError(connect.CodeDataLoss, errors.New("incomplete write: sandbox wrote 2 of 4 bytes"))
	if err := mapWriteError(dataLoss); !errors.Is(err, ErrIncompleteWrite) {
		t.Fatalf("mapWriteError = %v, want ErrIncompleteWrite", err)
	}
	if err := mapError(dataLoss); errors.Is(err, ErrIncompleteWrite) {
		t.Fatalf("mapError = %v, want DATA_LOSS outside writes left unclassified", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
