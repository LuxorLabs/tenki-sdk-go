package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
)

// Node-agent wire text for Stat on a guest agent that predates the fs primitives; the TypeScript and Python SDK tests use the same string.
const legacyGuestStatWireText = "guest_error: unknown request type: *sandboxv1.GuestRequest_Stat"

func TestMapError_LegacyGuestIsCapabilityUnavailable(t *testing.T) {
	t.Parallel()

	for _, code := range []connect.Code{connect.CodeUnimplemented, connect.CodeInternal} {
		mapped := mapError(connect.NewError(code, errors.New(legacyGuestStatWireText)))

		var unavailable *CapabilityUnavailableError
		if !errors.As(mapped, &unavailable) || !IsCapabilityUnavailable(mapped) {
			t.Fatalf("code %v: expected *CapabilityUnavailableError, got %T %v", code, mapped, mapped)
		}
		if unavailable.Primitive != "Stat" {
			t.Fatalf("code %v: primitive = %q, want Stat", code, unavailable.Primitive)
		}
		if connect.CodeOf(mapped) != code {
			t.Fatalf("code %v: the connect error must stay reachable, CodeOf = %v", code, connect.CodeOf(mapped))
		}
	}
}

func TestMapError_FileErrorIsNotCapabilityUnavailable(t *testing.T) {
	t.Parallel()

	mapped := mapError(connect.NewError(connect.CodeInternal, errors.New("guest_error: read /home/tenki/f: input/output error")))

	if IsCapabilityUnavailable(mapped) {
		t.Fatalf("a guest I/O error must not read as a missing capability, got %v", mapped)
	}
}

func TestStatOnLegacyGuestIsCapabilityUnavailable(t *testing.T) {
	t.Parallel()
	dataPlane := &readinessDataPlaneHandler{
		statErr: connect.NewError(connect.CodeUnimplemented, errors.New(legacyGuestStatWireText)),
	}
	client, _ := newReadinessHarness(t, &readinessEngineHandler{}, dataPlane, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}

	_, err := session.Stat(context.Background(), "/home/tenki/f.bin")

	if !IsCapabilityUnavailable(err) {
		t.Fatalf("expected a capability-unavailable error over the wire, got %T %v", err, err)
	}
}

func TestWriteFileStreamOnLegacyGuestIsCapabilityUnavailable(t *testing.T) {
	t.Parallel()
	dataPlane := &readinessDataPlaneHandler{
		writeStreamErr: connect.NewError(connect.CodeUnimplemented, errors.New("unknown request type: *sandboxv1.GuestRequest_WriteStream")),
	}
	client, _ := newReadinessHarness(t, &readinessEngineHandler{}, dataPlane, WithDataPlaneReadyTimeout(time.Second))
	session := &Session{client: client, ID: "session-1"}

	err := session.WriteFileStream(context.Background(), "/home/tenki/f.bin", strings.NewReader("payload"))

	if !IsCapabilityUnavailable(err) {
		t.Fatalf("expected a capability-unavailable error over the wire, got %T %v", err, err)
	}
}
