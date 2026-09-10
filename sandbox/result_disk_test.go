package sandbox

import "testing"

func TestDiskUsageExhausted(t *testing.T) {
	t.Parallel()

	const total = 5 << 30

	tests := []struct {
		name      string
		disk      DiskUsage
		known     bool
		exhausted bool
	}{
		{
			name: "unreported by guest reads as unknown",
			disk: DiskUsage{},
		},
		{
			name:  "healthy run",
			disk:  DiskUsage{TotalBytes: total, AvailableBytes: 2 << 30, MinAvailableBytes: 2 << 30},
			known: true,
		},
		{
			// npm hits ENOSPC while unpacking, prints it as a warning, exits 0,
			// and frees enough on the way out that the exit-time reading looks
			// survivable. Only the low-water mark shows the run was defeated.
			name:      "recovered before exit is still exhausted",
			disk:      DiskUsage{TotalBytes: total, AvailableBytes: 214 << 20, MinAvailableBytes: 0},
			known:     true,
			exhausted: true,
		},
		{
			// Allocators give up short of the last byte, so a disk that is full
			// from the workload's point of view still reports a few MiB free.
			name:      "near-zero floor counts as exhausted",
			disk:      DiskUsage{TotalBytes: total, AvailableBytes: 4 << 20, MinAvailableBytes: 4 << 20},
			known:     true,
			exhausted: true,
		},
		{
			name:  "comfortably above the floor is not exhausted",
			disk:  DiskUsage{TotalBytes: total, AvailableBytes: 64 << 20, MinAvailableBytes: 64 << 20},
			known: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.disk.Known(); got != tt.known {
				t.Fatalf("Known() = %v, want %v", got, tt.known)
			}
			if got := tt.disk.Exhausted(); got != tt.exhausted {
				t.Fatalf("Exhausted() = %v, want %v", got, tt.exhausted)
			}
		})
	}
}

func TestDiskUsageUsedBytes(t *testing.T) {
	t.Parallel()

	if got := (DiskUsage{}).UsedBytes(); got != 0 {
		t.Fatalf("UsedBytes() on unknown disk = %d, want 0", got)
	}

	// Real numbers from a 5 GiB sandbox: df reports 1963040768 used and
	// 2948128768 available, with 285212672 held back as the reserve. Counting
	// the reserve as used would report 43% where df prints 40%.
	disk := DiskUsage{
		TotalBytes:     5196382208,
		FreeBytes:      2948128768 + 285212672,
		AvailableBytes: 2948128768,
	}
	if got, want := disk.UsedBytes(), int64(1963040768); got != want {
		t.Fatalf("UsedBytes() = %d, want %d (df's used)", got, want)
	}
	if got := disk.UsedPercent(); got < 39.5 || got > 40.5 {
		t.Fatalf("UsedPercent() = %f, want df's 40%%", got)
	}
}

func TestDiskUsageOlderGuestWithoutFreeBytes(t *testing.T) {
	t.Parallel()

	// A guest too old to report FreeBytes must overstate usage rather than
	// invent headroom, so the reserve counts as used.
	disk := DiskUsage{TotalBytes: 5 << 30, AvailableBytes: 2 << 30}
	if got, want := disk.UsedBytes(), int64(3<<30); got != want {
		t.Fatalf("UsedBytes() = %d, want %d", got, want)
	}
	if got := disk.UsedPercent(); got < 59.5 || got > 60.5 {
		t.Fatalf("UsedPercent() = %f, want 60%%", got)
	}
}

func TestDiskUsagePercentGuardsEmptyDisk(t *testing.T) {
	t.Parallel()

	if got := (DiskUsage{}).UsedPercent(); got != 0 {
		t.Fatalf("UsedPercent() on unknown disk = %f, want 0", got)
	}
}
