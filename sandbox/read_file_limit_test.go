package sandbox

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

// Node-agent wire text for a refused unary ReadFile; the TypeScript and Python SDK tests use the same string.
const fileTooLargeWireText = "file too large for ReadFile: size_bytes=1100000000 max_bytes=33554432; use ReadFileStream"

func TestMapError_FileTooLarge(t *testing.T) {
	t.Parallel()

	mapped := mapError(connect.NewError(connect.CodeResourceExhausted, errors.New(fileTooLargeWireText)))

	if !errors.Is(mapped, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", mapped)
	}
	var tooLarge *FileTooLargeError
	if !errors.As(mapped, &tooLarge) {
		t.Fatalf("expected *FileTooLargeError, got %T", mapped)
	}
	if tooLarge.SizeBytes != 1100000000 || tooLarge.MaxBytes != 33554432 {
		t.Fatalf("sizes = %d/%d, want 1100000000/33554432", tooLarge.SizeBytes, tooLarge.MaxBytes)
	}
	if tooLarge.Operation != "ReadFile" {
		t.Fatalf("operation = %q, want ReadFile", tooLarge.Operation)
	}
	if errors.Is(mapped, ErrQuotaExceeded) {
		t.Fatal("a refused read is not a quota error")
	}
}

func TestMapError_FileTooLargeForWrite(t *testing.T) {
	t.Parallel()

	mapped := mapError(connect.NewError(connect.CodeResourceExhausted,
		errors.New("file too large for WriteFile: size_bytes=94371840 max_bytes=33554432; use WriteFileStream")))

	var tooLarge *FileTooLargeError
	if !errors.As(mapped, &tooLarge) || tooLarge.Operation != "WriteFile" || tooLarge.SizeBytes != 94371840 {
		t.Fatalf("got %#v", mapped)
	}
}

func TestMapError_FileTooLargeWithOverlongNumberStaysUntyped(t *testing.T) {
	t.Parallel()

	mapped := mapError(connect.NewError(connect.CodeResourceExhausted,
		errors.New("file too large for ReadFile: size_bytes=12345678901234567890 max_bytes=33554432; use ReadFileStream")))

	if errors.Is(mapped, ErrFileTooLarge) {
		t.Fatalf("an unparseable size must not produce a typed error, got %v", mapped)
	}
}

func TestMapError_ResourceExhaustedWithoutFileSizeStaysQuota(t *testing.T) {
	t.Parallel()

	mapped := mapError(connect.NewError(connect.CodeResourceExhausted, errors.New("workspace quota exceeded")))

	if !errors.Is(mapped, ErrQuotaExceeded) || errors.Is(mapped, ErrFileTooLarge) {
		t.Fatalf("expected ErrQuotaExceeded only, got %v", mapped)
	}
}
