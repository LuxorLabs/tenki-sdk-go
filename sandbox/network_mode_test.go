package sandbox

import (
	"testing"

	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
)

const futureEnumValue = 99

func TestSessionNetworkMode(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		proto      *sandboxv1.SandboxSession
		wantMode   NetworkMode
		wantReason NetworkRestrictionReason
	}{
		"older server": {
			proto:    &sandboxv1.SandboxSession{Id: "session-1"},
			wantMode: NetworkModeUnspecified, wantReason: NetworkRestrictionReasonUnspecified,
		},
		"full": {
			proto:    &sandboxv1.SandboxSession{Id: "session-1", NetworkMode: sandboxv1.SessionNetworkMode_SESSION_NETWORK_MODE_FULL},
			wantMode: NetworkModeFull, wantReason: NetworkRestrictionReasonUnspecified,
		},
		"restricted": {
			proto: &sandboxv1.SandboxSession{
				Id: "session-1", NetworkMode: sandboxv1.SessionNetworkMode_SESSION_NETWORK_MODE_RESTRICTED,
				NetworkRestrictionReason: sandboxv1.SessionNetworkRestrictionReason_SESSION_NETWORK_RESTRICTION_REASON_NEVER_PURCHASED,
			},
			wantMode: NetworkModeRestricted, wantReason: NetworkRestrictionReasonNeverPurchased,
		},
		"newer server": {
			proto: &sandboxv1.SandboxSession{
				Id: "session-1", NetworkMode: futureEnumValue, NetworkRestrictionReason: futureEnumValue,
			},
			wantMode: NetworkModeUnspecified, wantReason: NetworkRestrictionReasonUnspecified,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			session := newSession(nil, tc.proto)
			if session.NetworkMode != tc.wantMode || session.NetworkRestrictionReason != tc.wantReason {
				t.Fatalf("got %q/%q, want %q/%q", session.NetworkMode, session.NetworkRestrictionReason, tc.wantMode, tc.wantReason)
			}
			copied := newSession(nil, &sandboxv1.SandboxSession{Id: "session-1"})
			copied.copyFrom(session)
			if copied.NetworkMode != tc.wantMode || copied.NetworkRestrictionReason != tc.wantReason {
				t.Fatalf("copy got %q/%q, want %q/%q", copied.NetworkMode, copied.NetworkRestrictionReason, tc.wantMode, tc.wantReason)
			}
		})
	}
}

func TestRestrictedNetworkWarningCode(t *testing.T) {
	t.Parallel()

	got := sandboxWarningCodeFromProto(sandboxv1.SandboxWarningCode_SANDBOX_WARNING_CODE_RESTRICTED_NETWORK)
	if got != SandboxWarningCodeRestrictedNetwork {
		t.Fatalf("got %q, want %q", got, SandboxWarningCodeRestrictedNetwork)
	}
}
