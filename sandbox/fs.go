package sandbox

import (
	"context"
	"fmt"
	"io"
	"sync"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type FileInfo struct {
	Path           string
	Size           int64
	Mode           uint32
	IsDir          bool
	ModifiedUnixNs int64
	IsSymlink      bool
	SymlinkTarget  string
}

type ListOptions struct {
	IncludeHidden bool
}

type WriteStreamOptions struct {
	Mode     uint32
	Truncate bool
	Sync     bool
}

func (s *Session) ReadFileStream(ctx context.Context, path string) (io.ReadCloser, error) {
	return s.ReadStream(ctx, path, 0, 0)
}

func (s *Session) ReadStream(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return nil, err
	}
	stream, err := dp.ReadFileStream(ctx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceReadFileStreamRequest{Request: &sandboxv1.ReadFileStreamRequest{
		SessionId: s.ID,
		Path:      path,
		Offset:    offset,
		Length:    length,
	}}))
	if err != nil {
		return nil, mapError(err)
	}

	pr, pw := io.Pipe()
	go func() {
		defer stream.Close()
		for stream.Receive() {
			msg := stream.Msg()
			frame := msg.GetResponse()
			if len(frame.GetData()) > 0 {
				if _, err := pw.Write(frame.GetData()); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
			if frame.GetEof() {
				_ = pw.Close()
				return
			}
		}
		if err := stream.Err(); err != nil {
			_ = pw.CloseWithError(mapError(err))
			return
		}
		_ = pw.Close()
	}()

	return pr, nil
}

// WriteFileStream needs no TS-style early-abort/buffering guard: Write sends
// synchronously, so an early server reject stops io.Copy at the next Send.
func (s *Session) WriteFileStream(ctx context.Context, path string, r io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w, err := s.WriteStream(ctx, path, WriteStreamOptions{Truncate: true})
	if err != nil {
		return err
	}
	if _, copyErr := io.Copy(w, r); copyErr != nil {
		cancel() // abort the RPC so the server discards the partial write (SC-012)
		_ = w.Close()
		return copyErr
	}
	return w.Close()
}

func (s *Session) WriteStream(ctx context.Context, path string, opts ...WriteStreamOptions) (io.WriteCloser, error) {
	cfg := WriteStreamOptions{Mode: 0o644, Truncate: true}
	if len(opts) > 0 {
		cfg = opts[0]
		if cfg.Mode == 0 {
			cfg.Mode = 0o644
		}
	}

	dp, err := s.dataPlane(ctx)
	if err != nil {
		return nil, err
	}
	stream := dp.WriteFileStream(ctx)
	if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest{Frame: &sandboxv1.WriteFileStreamRequest{
		Payload: &sandboxv1.WriteFileStreamRequest_Start{
			Start: &sandboxv1.WriteFileStreamStart{
				SessionId: s.ID,
				Path:      path,
				Mode:      cfg.Mode,
				Truncate:  cfg.Truncate,
				Sync:      cfg.Sync,
			},
		},
	}}); err != nil {
		return nil, mapError(err)
	}
	return &fileStreamWriter{stream: stream}, nil
}

func (s *Session) Stat(ctx context.Context, path string) (*FileInfo, error) {
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := dp.Stat(ctx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceStatRequest{Request: &sandboxv1.StatRequest{
		SessionId: s.ID,
		Path:      path,
	}}))
	if err != nil {
		return nil, mapError(err)
	}
	return fileInfoFromStatResponse(path, resp.Msg.GetResponse())
}

// fileInfoFromStatResponse converts a data-plane stat response into a
// FileInfo, surfacing exists=false as ErrFileNotFound instead of a zeroed
// FileInfo for a path that is not there. A nil response is treated as
// not-found (proto getters are nil-safe); keep using the getters here.
func fileInfoFromStatResponse(path string, stat *sandboxv1.StatResponse) (*FileInfo, error) {
	if !stat.GetExists() {
		return nil, fmt.Errorf("%w: %s", ErrFileNotFound, path)
	}
	return &FileInfo{
		Path:           path,
		Size:           stat.GetSize(),
		Mode:           stat.GetMode(),
		IsDir:          stat.GetIsDir(),
		ModifiedUnixNs: stat.GetModifiedUnixNs(),
		IsSymlink:      stat.GetIsSymlink(),
		SymlinkTarget:  stat.GetSymlinkTarget(),
	}, nil
}

func (s *Session) Mkdir(ctx context.Context, path string) error {
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return err
	}
	_, err = dp.Mkdir(ctx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceMkdirRequest{Request: &sandboxv1.MkdirRequest{
		SessionId: s.ID,
		Path:      path,
		Mode:      0o755,
		Recursive: true,
	}}))
	return mapError(err)
}

func (s *Session) Remove(ctx context.Context, path string) error {
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return err
	}
	_, err = dp.Remove(ctx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceRemoveRequest{Request: &sandboxv1.RemoveRequest{
		SessionId: s.ID,
		Path:      path,
		Recursive: true,
	}}))
	return mapError(err)
}

func (s *Session) List(ctx context.Context, path string, opts ...ListOptions) ([]FileInfo, error) {
	cfg := ListOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	dp, err := s.dataPlane(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := dp.List(ctx, connect.NewRequest(&sandboxv1.SandboxSessionDataPlaneServiceListRequest{Request: &sandboxv1.ListRequest{
		SessionId:     s.ID,
		Path:          path,
		IncludeHidden: cfg.IncludeHidden,
	}}))
	if err != nil {
		return nil, mapError(err)
	}
	entries := make([]FileInfo, 0, len(resp.Msg.GetResponse().GetEntries()))
	for _, entry := range resp.Msg.GetResponse().GetEntries() {
		if entry == nil {
			continue
		}
		entries = append(entries, FileInfo{
			Path:           entry.GetName(),
			Size:           entry.GetSize(),
			Mode:           entry.GetMode(),
			IsDir:          entry.GetIsDir(),
			ModifiedUnixNs: entry.GetModifiedUnixNs(),
		})
	}
	return entries, nil
}

type fileStreamWriter struct {
	stream *connect.ClientStreamForClient[sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest, sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamResponse]
	mu     sync.Mutex
	closed bool
}

func (w *fileStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	data := append([]byte(nil), p...)
	if err := w.stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceWriteFileStreamRequest{Frame: &sandboxv1.WriteFileStreamRequest{
		Payload: &sandboxv1.WriteFileStreamRequest_Data{Data: data},
	}}); err != nil {
		return 0, mapError(err)
	}
	return len(p), nil
}

func (w *fileStreamWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	_, err := w.stream.CloseAndReceive()
	return mapError(err)
}
