package sandbox

import (
	"strings"
	"time"
)

// CommandStatus represents command execution status.
type CommandStatus string

const (
	CommandStatusUnspecified CommandStatus = "UNSPECIFIED"
	CommandStatusQueued      CommandStatus = "QUEUED"
	CommandStatusRunning     CommandStatus = "RUNNING"
	CommandStatusSucceeded   CommandStatus = "SUCCEEDED"
	CommandStatusFailed      CommandStatus = "FAILED"
	CommandStatusTimedOut    CommandStatus = "TIMED_OUT"
)

// Output is a stream chunk emitted by data-plane Run.
type Output struct {
	Data     []byte
	IsStderr bool
	IsFinal  bool
}

// Result is the normalized command execution result.
type Result struct {
	SessionID string
	Command   string
	Args      []string
	Status    CommandStatus
	ExitCode  int32
	Duration  time.Duration
	StartedAt *time.Time
	EndedAt   *time.Time
	Outputs   []Output
	Stdout    []byte
	Stderr    []byte
	// Reason is the guest's terminal reason: "exit", "signaled", "timeout" or
	// "grace_timeout".
	Reason string
}

// StdoutString returns trimmed stdout as a string.
func (r *Result) StdoutString() string {
	return strings.TrimSpace(string(r.Stdout))
}

// StderrString returns trimmed stderr as a string.
func (r *Result) StderrString() string {
	return strings.TrimSpace(string(r.Stderr))
}

// IsSuccess returns true if the command succeeded.
func (s CommandStatus) IsSuccess() bool {
	return s == CommandStatusSucceeded
}

// IsFailed returns true if the command failed.
func (s CommandStatus) IsFailed() bool {
	return s == CommandStatusFailed
}

// IsTimedOut returns true if the command timed out.
func (s CommandStatus) IsTimedOut() bool {
	return s == CommandStatusTimedOut
}
