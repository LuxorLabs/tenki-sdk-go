package sandbox

import (
	"testing"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

func TestIsRunCapabilityUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"empty", "", false},
		{"random", "echo: command not found", false},
		{"capability_unavailable_bare", "capability_unavailable", true},
		{"capability_unavailable_with_detail", "capability_unavailable: sandbox run capability unavailable", true},
		{"capability_unavailable_guest_agent", "capability_unavailable: unknown request type: *sandboxv1.GuestRequest_Run", true},
		{"first_frame_timeout_must_not_match", "first_frame_timeout: no response from guest-agent within deadline", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit := &sandboxv1.RunExit{Reason: tc.reason}
			if got := isRunCapabilityUnavailable(exit); got != tc.want {
				t.Fatalf("isRunCapabilityUnavailable(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

func TestIsRunFirstFrameTimeout(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"empty", "", false},
		{"capability_unavailable_must_not_match", "capability_unavailable: foo", false},
		{"first_frame_timeout_bare", "first_frame_timeout", true},
		{"first_frame_timeout_with_detail", "first_frame_timeout: no response from guest-agent within deadline", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit := &sandboxv1.RunExit{Reason: tc.reason}
			if got := isRunFirstFrameTimeout(exit); got != tc.want {
				t.Fatalf("isRunFirstFrameTimeout(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

func TestCapabilityUnavailablePredicateContract(t *testing.T) {
	capErr := &CapabilityUnavailableError{Primitive: "run", Message: "capability_unavailable: x"}
	timeoutErr := &PrimitiveTimeoutError{Primitive: "run", Message: "first_frame_timeout: x"}

	// Existing callers still classify watchdog timeouts as capability errors
	// for compatibility with callers that group run-establishment failures.
	if !IsCapabilityUnavailable(capErr) {
		t.Fatal("IsCapabilityUnavailable should match CapabilityUnavailableError")
	}
	if !IsCapabilityUnavailable(timeoutErr) {
		t.Fatal("IsCapabilityUnavailable must keep matching PrimitiveTimeoutError")
	}

	// New callers can discriminate the transient case.
	if !IsPrimitiveTimeout(timeoutErr) {
		t.Fatal("IsPrimitiveTimeout should match PrimitiveTimeoutError")
	}
	if IsPrimitiveTimeout(capErr) {
		t.Fatal("IsPrimitiveTimeout must NOT match CapabilityUnavailableError")
	}
}
