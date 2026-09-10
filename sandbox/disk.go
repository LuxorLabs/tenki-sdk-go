package sandbox

// DiskUsage is the sandbox filesystem state observed while a command ran.
type DiskUsage struct {
	TotalBytes int64
	// FreeBytes counts the filesystem reserve that AvailableBytes excludes.
	// FreeBytes - AvailableBytes is the reserve itself, which is why a
	// root-owned write can still land on a filesystem the workload sees as
	// full. Zero from a guest too old to report it.
	FreeBytes int64
	// AvailableBytes is free space when the command exited, excluding the
	// reserve: what the workload itself can still write.
	AvailableBytes int64
	// MinAvailableBytes is the least free space seen while the command ran. It is
	// the field that identifies a run defeated by a full disk: pip, npm and
	// containerd all delete their partial downloads after hitting ENOSPC, so
	// AvailableBytes recovers before the command exits and only this stays low.
	// Session.DiskUsage takes a single sample, so there it equals AvailableBytes.
	MinAvailableBytes int64
}

// diskExhaustionAvailableBytes is the free-space floor under which a run is
// treated as having exhausted the disk. Not zero: allocators fail short of the
// last byte, and ext4 keeps a root reserve the workload cannot touch, so a
// workload-visible "full" disk still reports a small non-zero Bavail.
const diskExhaustionAvailableBytes = 16 << 20 // 16 MiB

// Known reports whether the guest supplied disk stats for this run.
func (d DiskUsage) Known() bool { return d.TotalBytes > 0 }

// Exhausted reports whether the sandbox filesystem ran out of usable space at
// any point during the command. It is deliberately independent of exit status:
// npm hits ENOSPC while unpacking, prints it as a warning and still exits 0,
// leaving a broken install behind, so a caller that only checks this on failure
// misses the case that is hardest to diagnose.
func (d DiskUsage) Exhausted() bool {
	return d.Known() && d.MinAvailableBytes < diskExhaustionAvailableBytes
}

// UsedBytes returns space consumed on the sandbox filesystem, counted as df
// does: total minus free, so the reserve is neither used nor available. A guest
// too old to report FreeBytes falls back to treating the reserve as used, which
// overstates usage rather than inventing headroom.
func (d DiskUsage) UsedBytes() int64 {
	if !d.Known() {
		return 0
	}
	free := d.FreeBytes
	if free <= 0 {
		free = d.AvailableBytes
	}
	return d.TotalBytes - free
}

// UsedPercent returns usage as df reports it, against usable capacity rather
// than raw total. The reserve is excluded from both sides, so this matches what
// `df` prints inside the sandbox instead of disagreeing with it by a few points.
func (d DiskUsage) UsedPercent() float64 {
	used := d.UsedBytes()
	usable := used + d.AvailableBytes
	if usable <= 0 {
		return 0
	}
	return float64(used) / float64(usable) * 100
}
