package sandbox

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

// Coverage for git operation failure classification (retryable vs terminal).

func TestMapError_TypedMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "port limit exceeded",
			err:  connect.NewError(connect.CodeResourceExhausted, errors.New("maximum exposed ports reached")),
			want: ErrPortLimitExceeded,
		},
		{
			name: "inbound disabled",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("inbound access is disabled for session")),
			want: ErrInboundDisabled,
		},
		{
			name: "ssh unavailable",
			err:  connect.NewError(connect.CodeUnavailable, errors.New("failed to open ssh tunnel")),
			want: ErrSSHUnavailable,
		},
		{
			name: "rate limited",
			err:  connect.NewError(connect.CodeResourceExhausted, errors.New("api key rate limit exceeded")),
			want: ErrRateLimited,
		},
		{
			name: "template not found",
			err:  connect.NewError(connect.CodeNotFound, errors.New("template not found")),
			want: ErrTemplateNotFound,
		},
		{
			name: "registry image not found",
			err:  connect.NewError(connect.CodeNotFound, errors.New("registry image version not found")),
			want: ErrRegistryImageNotFound,
		},
		{
			name: "template build already in progress",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("template build already in progress")),
			want: ErrTemplateBuildInProgress,
		},
		{
			name: "snapshot not yet durable",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("snapshot not yet durable on a reachable host")),
			want: ErrSnapshotNotDurable,
		},
		{
			name: "session metrics not configured",
			err:  connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox session metrics are not configured")),
			want: ErrInvalidState,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mapped := mapError(tc.err)
			if !errors.Is(mapped, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, mapped)
			}
		})
	}
}

func TestMapError_GitOperationFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		message       string
		wantRetryable bool
		wantStderr    string
	}{
		{
			name:          "transient egress reset is retryable",
			message:       "git clone failed (exit_code=128); check the repository, ref and access token: fatal: unable to access 'https://github.com/x/y': Recv failure: Connection reset by peer",
			wantRetryable: true,
			wantStderr:    "fatal: unable to access 'https://github.com/x/y': Recv failure: Connection reset by peer",
		},
		{
			name:          "auth failure is terminal",
			message:       "git fetch PR failed (exit_code=128); check the repository, ref and access token: fatal: Authentication failed for 'https://github.com/x/y'",
			wantRetryable: false,
			wantStderr:    "fatal: Authentication failed for 'https://github.com/x/y'",
		},
		{
			name:          "repository not found is terminal",
			message:       "git clone failed (exit_code=128); check the repository, ref and access token: fatal: repository 'https://github.com/x/y' not found",
			wantRetryable: false,
			wantStderr:    "fatal: repository 'https://github.com/x/y' not found",
		},
		{
			name:          "terminal fetch-pack data corruption is not retryable",
			message:       "git fetch PR failed (exit_code=128); check the repository, ref and access token: fatal: fetch-pack: invalid index-pack output",
			wantRetryable: false,
			wantStderr:    "fatal: fetch-pack: invalid index-pack output",
		},
		{
			name:          "ambiguous git failure fails closed",
			message:       "git clone failed (exit_code=128); check the repository, ref and access token: something unexpected",
			wantRetryable: false,
			wantStderr:    "something unexpected",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mapped := mapError(connect.NewError(connect.CodeFailedPrecondition, errors.New(tc.message)))
			var gitErr *GitOperationFailedError
			if !errors.As(mapped, &gitErr) {
				t.Fatalf("expected *GitOperationFailedError, got %T (%v)", mapped, mapped)
			}
			if gitErr.IsRetryable() != tc.wantRetryable {
				t.Fatalf("retryable: got %v, want %v", gitErr.IsRetryable(), tc.wantRetryable)
			}
			if gitErr.ExitCode == nil || *gitErr.ExitCode != 128 {
				t.Fatalf("exit code: got %v, want 128", gitErr.ExitCode)
			}
			// Stderr must be just the git output, not the engine wrapper prefix.
			if gitErr.Stderr != tc.wantStderr {
				t.Fatalf("stderr: got %q, want %q", gitErr.Stderr, tc.wantStderr)
			}
			// Must no longer collapse into the un-actionable ErrInvalidState.
			if errors.Is(mapped, ErrInvalidState) {
				t.Fatalf("git failure must not map to ErrInvalidState")
			}
		})
	}
}
