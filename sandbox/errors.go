package sandbox

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"connectrpc.com/connect"
)

// Pure SDK errors
var (
	ErrSessionNotFound         = errors.New("sandbox: session not found")
	ErrSessionExpired          = errors.New("sandbox: session expired")
	ErrSessionTerminated       = errors.New("sandbox: session terminated")
	ErrFileNotFound            = errors.New("sandbox: file not found")
	ErrInvalidState            = errors.New("sandbox: invalid session state for operation")
	ErrCommandTimeout          = errors.New("sandbox: command execution timed out")
	ErrUnauthorized            = errors.New("sandbox: unauthorized")
	ErrPermissionDenied        = errors.New("sandbox: permission denied")
	ErrQuotaExceeded           = errors.New("sandbox: quota exceeded")
	ErrCapacityUnavailable     = errors.New("sandbox: capacity unavailable, please retry")
	ErrPortLimitExceeded       = errors.New("sandbox: maximum exposed ports reached")
	ErrInboundDisabled         = errors.New("sandbox: inbound access is disabled")
	ErrSSHUnavailable          = errors.New("sandbox: ssh unavailable")
	ErrRateLimited             = errors.New("sandbox: rate limited")
	ErrVolumeNotFound          = errors.New("sandbox: volume not found")
	ErrVolumeInUse             = errors.New("sandbox: volume is attached to a session")
	ErrVolumeSyncPending       = errors.New("sandbox: volume sync back is still pending")
	ErrVolumeLimitExceeded     = errors.New("sandbox: volume limit exceeded")
	ErrGitOperationFailed      = errors.New("sandbox: git operation failed")
	ErrStreamClosed            = errors.New("sandbox: interactive stream closed")
	ErrInvalidResourceConfig   = errors.New("sandbox: invalid resource configuration")
	ErrSnapshotNotFound        = errors.New("sandbox: snapshot not found")
	ErrSnapshotFailed          = errors.New("sandbox: snapshot failed")
	ErrResumeFailed            = errors.New("sandbox: resume failed")
	ErrSnapshotNotDurable      = errors.New("sandbox: snapshot upload did not become durable")
	ErrTemplateNotFound        = errors.New("sandbox: template not found")
	ErrRegistryImageNotFound   = errors.New("sandbox: registry image not found")
	ErrTemplateExists          = errors.New("sandbox: template already exists")
	ErrTemplateBuildNotFound   = errors.New("sandbox: template build not found")
	ErrTemplateBuildFailed     = errors.New("sandbox: template build failed")
	ErrTemplateBuildInProgress = errors.New("sandbox: template build already in progress")
	ErrTemplateRuntimeFailed   = errors.New("sandbox: template runtime failed")
	ErrWaitReadyFailed         = errors.New("sandbox: session created but not ready within wait budget")
	ErrPaginationStalled       = errors.New("sandbox: pagination token did not advance")
)

// WaitReadyFailedError carries a session admitted before its readiness wait failed.
type WaitReadyFailedError struct {
	Session *Session
	Err     error
}

func (e *WaitReadyFailedError) Error() string {
	if e == nil || e.Err == nil {
		return ErrWaitReadyFailed.Error()
	}
	return ErrWaitReadyFailed.Error() + ": " + e.Err.Error()
}

func (e *WaitReadyFailedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *WaitReadyFailedError) Is(target error) bool {
	return target == ErrWaitReadyFailed
}

type TemplateRuntimeFailedError struct {
	Session *Session
	Reason  string
	Err     error
}

func (e *TemplateRuntimeFailedError) Error() string {
	if e == nil || e.Reason == "" {
		return ErrTemplateRuntimeFailed.Error()
	}
	return ErrTemplateRuntimeFailed.Error() + ": " + e.Reason
}

func (e *TemplateRuntimeFailedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *TemplateRuntimeFailedError) Is(target error) bool {
	return target == ErrTemplateRuntimeFailed
}

type CapabilityUnavailableError struct {
	Primitive string
	Message   string
}

// IsCapabilityUnavailable reports whether err means the server/image lacks a
// requested primitive. Matches both authoritative CapabilityUnavailableError
// and the watchdog-emitted PrimitiveTimeoutError so existing fallback paths
// keep working; callers that want to discriminate use IsPrimitiveTimeout.
func IsCapabilityUnavailable(err error) bool {
	var unavailable *CapabilityUnavailableError
	if errors.As(err, &unavailable) {
		return true
	}
	var timeout *PrimitiveTimeoutError
	return errors.As(err, &timeout)
}

func (e *CapabilityUnavailableError) Error() string {
	if e == nil {
		return "sandbox: capability unavailable"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Primitive != "" {
		return "sandbox: capability unavailable: " + e.Primitive
	}
	return "sandbox: capability unavailable"
}

type DataPlaneNotReadyError struct {
	Message  string
	Err      error
	Terminal bool
}

func IsDataPlaneNotReady(err error) bool {
	var target *DataPlaneNotReadyError
	return errors.As(err, &target)
}

func isPausedSessionStateError(err error) bool {
	if err == nil || !errors.Is(err, ErrInvalidState) {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "STATE=PAUSED") || strings.Contains(msg, "STATE = PAUSED")
}

func (e *DataPlaneNotReadyError) Error() string {
	if e == nil {
		return "sandbox: data-plane edge route not ready"
	}
	if e.Message != "" {
		return e.Message
	}
	return "sandbox: data-plane edge route not ready"
}

func (e *DataPlaneNotReadyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *DataPlaneNotReadyError) IsRetryable() bool {
	return e == nil || !e.Terminal
}

type PrimitiveTimeoutError struct {
	Primitive string
	Message   string
}

func IsPrimitiveTimeout(err error) bool {
	var t *PrimitiveTimeoutError
	return errors.As(err, &t)
}

func (e *PrimitiveTimeoutError) Error() string {
	if e == nil {
		return "sandbox: primitive timed out waiting for guest-agent reply"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Primitive != "" {
		return "sandbox: " + e.Primitive + " timed out waiting for guest-agent reply"
	}
	return "sandbox: primitive timed out waiting for guest-agent reply"
}

// SessionTerminatedError means the platform tore the sandbox session down while
// the stream was open — the command did not finish, so the caller can retry on a
// fresh session. Cause is the machine-readable termination cause, e.g.
// "guest_agent_liveness". It satisfies errors.Is(err, ErrSessionTerminated) so
// callers written against the sentinel keep working.
type SessionTerminatedError struct {
	Primitive string
	Cause     string
	Message   string
}

func IsSessionTerminated(err error) bool {
	return errors.Is(err, ErrSessionTerminated)
}

func (e *SessionTerminatedError) Is(target error) bool {
	return target == ErrSessionTerminated
}

func (e *SessionTerminatedError) Error() string {
	if e == nil {
		return "sandbox: session terminated by the platform"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != "" {
		return "sandbox: session terminated by the platform: " + e.Cause
	}
	return "sandbox: session terminated by the platform"
}

func (e *SessionTerminatedError) IsRetryable() bool { return true }

// GitOperationFailedError classifies an in-guest git failure surfaced by
// node-agent as FailedPrecondition.
type GitOperationFailedError struct {
	Message   string
	Stderr    string
	ExitCode  *int
	Retryable bool
	Err       error
}

func IsGitOperationFailed(err error) bool {
	var t *GitOperationFailedError
	return errors.As(err, &t)
}

func (e *GitOperationFailedError) Error() string {
	if e == nil || e.Message == "" {
		return ErrGitOperationFailed.Error()
	}
	return e.Message
}

func (e *GitOperationFailedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *GitOperationFailedError) IsRetryable() bool {
	return e != nil && e.Retryable
}

// Git failure classification — see the matching block in errors.ts / errors.py.
var (
	gitFailureRe   = regexp.MustCompile(`(?i)git .*failed \(exit_code=`)
	gitExitCodeRe  = regexp.MustCompile(`\(exit_code=(\d+)\)`)
	gitStderrRe    = regexp.MustCompile(`(?s); check the repository, ref and access token: (.+)`)
	transientGitRe = regexp.MustCompile(`(?i)Recv failure|Connection reset by peer|Could not resolve host|Failed to connect|Connection timed out|TLS|gnutls_handshake|SSL_ERROR|RPC failed|HTTP 5\d\d|\b(429|503)\b|early EOF|remote end hung up unexpectedly|unexpected disconnect`)
	terminalGitRe  = regexp.MustCompile(`(?i)Authentication failed|could not read Username|terminal prompts disabled|Repository not found|fatal: repository .* not found|Permission denied|\b403\b|invalid credentials|pathspec|did not match any`)
)

// mapTemplateSpecError surfaces server-side protovalidate violations on
// template submissions as one typed error carrying every violation.
func mapTemplateSpecError(err error) error {
	if validationErr := templateSpecViolationsFromError(err); validationErr != nil {
		return validationErr
	}
	return mapError(err)
}

func templateSpecViolationsFromError(err error) *TemplateSpecValidationError {
	var connectErr *connect.Error
	if err == nil || !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeInvalidArgument {
		return nil
	}
	var violations []TemplateSpecViolation
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		protoViolations, ok := value.(*validate.Violations)
		if !ok {
			continue
		}
		for _, violation := range protoViolations.GetViolations() {
			violations = append(violations, TemplateSpecViolation{
				Field:   validateFieldPathString(violation.GetField()),
				Rule:    violation.GetRuleId(),
				Message: violation.GetMessage(),
			})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return &TemplateSpecValidationError{Violations: violations}
}

func validateFieldPathString(path *validate.FieldPath) string {
	if path == nil {
		return ""
	}
	var builder strings.Builder
	for _, element := range path.GetElements() {
		if name := element.GetFieldName(); name != "" {
			if builder.Len() > 0 {
				builder.WriteByte('.')
			}
			builder.WriteString(name)
		}
		switch subscript := element.GetSubscript().(type) {
		case *validate.FieldPathElement_Index:
			fmt.Fprintf(&builder, "[%d]", subscript.Index)
		case *validate.FieldPathElement_StringKey:
			fmt.Fprintf(&builder, "[%q]", subscript.StringKey)
		case *validate.FieldPathElement_IntKey:
			fmt.Fprintf(&builder, "[%d]", subscript.IntKey)
		case *validate.FieldPathElement_UintKey:
			fmt.Fprintf(&builder, "[%d]", subscript.UintKey)
		case *validate.FieldPathElement_BoolKey:
			fmt.Fprintf(&builder, "[%t]", subscript.BoolKey)
		}
	}
	return builder.String()
}

func mapError(err error) error {
	if err == nil {
		return nil
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return err
	}
	if isEdgeNotReady(err) {
		return dataPlaneNotReadyError(err)
	}

	msg := strings.ToLower(connectErr.Message())

	switch connectErr.Code() {
	case connect.CodeUnauthenticated:
		return fmt.Errorf("%w: %w", ErrUnauthorized, err)
	case connect.CodePermissionDenied:
		return fmt.Errorf("%w: %w", ErrPermissionDenied, err)
	case connect.CodeNotFound:
		switch {
		case strings.Contains(msg, "template build"):
			return fmt.Errorf("%w: %w", ErrTemplateBuildNotFound, err)
		case strings.Contains(msg, "template"):
			return fmt.Errorf("%w: %w", ErrTemplateNotFound, err)
		case strings.Contains(msg, "registry image"):
			return fmt.Errorf("%w: %w", ErrRegistryImageNotFound, err)
		case strings.Contains(msg, "no such file or directory"):
			return fmt.Errorf("%w: %w", ErrFileNotFound, err)
		case strings.Contains(msg, "session"):
			return fmt.Errorf("%w: %w", ErrSessionNotFound, err)
		case strings.Contains(msg, "snapshot"):
			return fmt.Errorf("%w: %w", ErrSnapshotNotFound, err)
		case strings.Contains(msg, "volume"):
			return fmt.Errorf("%w: %w", ErrVolumeNotFound, err)
		case strings.Contains(msg, "artifact"):
			return fmt.Errorf("%w: %w", ErrSessionExpired, err)
		default:
			return fmt.Errorf("%w: %w", ErrSessionNotFound, err)
		}
	case connect.CodeAlreadyExists:
		if strings.Contains(msg, "template") {
			return fmt.Errorf("%w: %w", ErrTemplateExists, err)
		}
		return err
	case connect.CodeDeadlineExceeded:
		return fmt.Errorf("%w: %w", ErrCommandTimeout, err)
	case connect.CodeResourceExhausted:
		if strings.Contains(msg, "maximum exposed ports") {
			return fmt.Errorf("%w: %w", ErrPortLimitExceeded, err)
		}
		if strings.Contains(msg, "volume") {
			return fmt.Errorf("%w: %w", ErrVolumeLimitExceeded, err)
		}
		if strings.Contains(msg, "rate limit") {
			return fmt.Errorf("%w: %w", ErrRateLimited, err)
		}
		if strings.Contains(msg, "capacity") {
			return fmt.Errorf("%w: %w", ErrCapacityUnavailable, err)
		}
		return fmt.Errorf("%w: %w", ErrQuotaExceeded, err)
	case connect.CodeFailedPrecondition:
		switch {
		case strings.Contains(msg, "snapshot not yet durable"):
			return fmt.Errorf("%w: %w", ErrSnapshotNotDurable, err)
		case strings.Contains(msg, "template build already in progress"), strings.Contains(msg, "build in progress"):
			return fmt.Errorf("%w: %w", ErrTemplateBuildInProgress, err)
		case strings.Contains(msg, "terminated"):
			return fmt.Errorf("%w: %w", ErrSessionTerminated, err)
		case gitFailureRe.MatchString(connectErr.Message()):
			msg := connectErr.Message()
			var exitCode *int
			if m := gitExitCodeRe.FindStringSubmatch(msg); m != nil {
				if n, convErr := strconv.Atoi(m[1]); convErr == nil {
					exitCode = &n
				}
			}
			stderr := ""
			if m := gitStderrRe.FindStringSubmatch(msg); m != nil {
				stderr = m[1]
			}
			return &GitOperationFailedError{
				Message:   msg,
				Stderr:    stderr,
				ExitCode:  exitCode,
				Retryable: transientGitRe.MatchString(msg) && !terminalGitRe.MatchString(msg),
				Err:       err,
			}
		case strings.Contains(msg, "sync_pending"), strings.Contains(msg, "sync pending"):
			return fmt.Errorf("%w: %w", ErrVolumeSyncPending, err)
		case strings.Contains(msg, "volume is attached") || strings.Contains(msg, "volume is in use"):
			return fmt.Errorf("%w: %w", ErrVolumeInUse, err)
		case strings.Contains(msg, "inbound access is disabled"):
			return fmt.Errorf("%w: %w", ErrInboundDisabled, err)
		case strings.Contains(msg, "ssh"):
			return fmt.Errorf("%w: %w", ErrSSHUnavailable, err)
		case strings.Contains(msg, "already running"):
			return fmt.Errorf("%w: %w", ErrInvalidState, err)
		case strings.Contains(msg, "not ready"):
			return fmt.Errorf("%w: %w", ErrInvalidState, err)
		default:
			return fmt.Errorf("%w: %w", ErrInvalidState, err)
		}
	case connect.CodeUnavailable:
		if strings.Contains(msg, "ssh") {
			return fmt.Errorf("%w: %w", ErrSSHUnavailable, err)
		}
		return err
	default:
		return err
	}
}
