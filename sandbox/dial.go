package sandbox

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type DialOptions struct {
	ConnectTimeout time.Duration
}

func (s *Session) Dial(ctx context.Context, unixSocketPath string, opts ...DialOptions) (net.Conn, error) {
	var opt DialOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	connectTimeoutMs := uint32(0)
	if opt.ConnectTimeout > 0 {
		connectTimeoutMs = uint32(opt.ConnectTimeout / time.Millisecond)
	}
	for attempt := 0; ; attempt++ {
		dp, err := s.dataPlane(ctx)
		if err != nil {
			return nil, err
		}
		stream := dp.Dial(ctx)
		if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceDialRequest{Frame: &sandboxv1.DialRequest{Payload: &sandboxv1.DialRequest_Open{Open: &sandboxv1.DialOpen{
			SessionId:        s.ID,
			Target:           &sandboxv1.DialOpen_UnixSocketPath{UnixSocketPath: unixSocketPath},
			ConnectTimeoutMs: connectTimeoutMs,
		}}}}); err != nil {
			if attempt == 0 && s.reauthOnUnauthenticated(ctx, err) {
				_ = stream.CloseRequest()
				continue
			}
			return nil, err
		}
		first, err := stream.Receive()
		if err != nil {
			if attempt == 0 && s.reauthOnUnauthenticated(ctx, err) {
				_ = stream.CloseRequest()
				continue
			}
			return nil, err
		}
		firstFrame := first.GetFrame()
		if closed := firstFrame.GetClosed(); closed != nil {
			if closed.GetReason() == sandboxv1.DialClosed_REASON_CAPABILITY_UNAVAILABLE {
				if strings.HasPrefix(closed.GetDetail(), "first_frame_timeout") {
					return nil, &PrimitiveTimeoutError{Primitive: "dial", Message: closed.GetDetail()}
				}
				return nil, &CapabilityUnavailableError{Primitive: "dial", Message: closed.GetDetail()}
			}
			return nil, errors.New(closed.GetDetail())
		}
		if firstFrame.GetOpened() == nil {
			return nil, errors.New("sandbox dial did not open")
		}

		appConn, sdkConn := net.Pipe()
		go func() {
			defer sdkConn.Close()
			defer stream.CloseRequest()
			buf := make([]byte, 32*1024)
			for {
				n, readErr := sdkConn.Read(buf)
				if n > 0 {
					if err := stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceDialRequest{Frame: &sandboxv1.DialRequest{Payload: &sandboxv1.DialRequest_Data{Data: append([]byte(nil), buf[:n]...)}}}); err != nil {
						return
					}
				}
				if readErr != nil {
					if errors.Is(readErr, io.EOF) {
						_ = stream.Send(&sandboxv1.SandboxSessionDataPlaneServiceDialRequest{Frame: &sandboxv1.DialRequest{Payload: &sandboxv1.DialRequest_HalfClose{HalfClose: true}}})
					}
					return
				}
			}
		}()
		go func() {
			defer sdkConn.Close()
			for {
				frame, err := stream.Receive()
				if err != nil {
					return
				}
				if data := frame.GetFrame().GetData(); len(data) > 0 {
					if _, err := sdkConn.Write(data); err != nil {
						return
					}
				}
				if frame.GetFrame().GetClosed() != nil {
					return
				}
			}
		}()
		return appConn, nil
	}
}
