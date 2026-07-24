package sandbox

import (
	"errors"
	"testing"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

func TestFileInfoFromStatResponse(t *testing.T) {
	t.Parallel()

	t.Run("missing file surfaces ErrFileNotFound", func(t *testing.T) {
		t.Parallel()
		info, err := fileInfoFromStatResponse("/home/tenki/missing", &sandboxv1.StatResponse{Exists: false})
		if info != nil {
			t.Fatalf("expected nil FileInfo, got %+v", info)
		}
		if !errors.Is(err, ErrFileNotFound) {
			t.Fatalf("expected ErrFileNotFound, got %v", err)
		}
	})

	t.Run("existing file maps fields", func(t *testing.T) {
		t.Parallel()
		info, err := fileInfoFromStatResponse("/home/tenki/a.txt", &sandboxv1.StatResponse{
			Exists:         true,
			Size:           5,
			Mode:           0o644,
			IsDir:          false,
			ModifiedUnixNs: 42,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Path != "/home/tenki/a.txt" || info.Size != 5 || info.Mode != 0o644 || info.ModifiedUnixNs != 42 {
			t.Fatalf("unexpected FileInfo: %+v", info)
		}
	})

	t.Run("nil response surfaces ErrFileNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := fileInfoFromStatResponse("/x", nil)
		if !errors.Is(err, ErrFileNotFound) {
			t.Fatalf("expected ErrFileNotFound, got %v", err)
		}
	})
}
