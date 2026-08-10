package sandbox

import (
	"testing"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

func TestWildcardStatusFromProto(t *testing.T) {
	t.Parallel()

	cases := []struct {
		proto sandboxv1.WildcardStatus
		want  WildcardStatus
	}{
		{sandboxv1.WildcardStatus_WILDCARD_STATUS_UNSPECIFIED, WildcardStatusUnspecified},
		{sandboxv1.WildcardStatus_WILDCARD_STATUS_PENDING, WildcardStatusPending},
		{sandboxv1.WildcardStatus_WILDCARD_STATUS_READY, WildcardStatusReady},
		{sandboxv1.WildcardStatus_WILDCARD_STATUS_FAILED, WildcardStatusFailed},
		{sandboxv1.WildcardStatus_WILDCARD_STATUS_DISABLED, WildcardStatusDisabled},
		{sandboxv1.WildcardStatus(999), WildcardStatusUnspecified},
	}
	for _, tc := range cases {
		if got := wildcardStatusFromProto(tc.proto); got != tc.want {
			t.Fatalf("wildcardStatusFromProto(%v) = %q, want %q", tc.proto, got, tc.want)
		}
	}
}

func TestPreviewURLFromProtoSurfacesWildcard(t *testing.T) {
	t.Parallel()

	wildcard := true
	status := sandboxv1.WildcardStatus_WILDCARD_STATUS_FAILED
	reason := "certificate issuance failed"
	proto := &sandboxv1.PreviewUrl{
		Id:                   "pu-1",
		Slug:                 "demo",
		Wildcard:             &wildcard,
		WildcardStatus:       &status,
		WildcardStatusReason: &reason,
	}

	got := previewURLFromProto(proto)
	if got == nil {
		t.Fatal("previewURLFromProto returned nil")
	}
	if !got.Wildcard {
		t.Fatalf("Wildcard = false, want true")
	}
	if got.WildcardStatus != WildcardStatusFailed {
		t.Fatalf("WildcardStatus = %q, want %q", got.WildcardStatus, WildcardStatusFailed)
	}
	if got.WildcardStatusReason != reason {
		t.Fatalf("WildcardStatusReason = %q, want %q", got.WildcardStatusReason, reason)
	}
}

func TestPreviewURLFromProtoWildcardDefaults(t *testing.T) {
	t.Parallel()

	got := previewURLFromProto(&sandboxv1.PreviewUrl{Id: "pu-2", Slug: "plain"})
	if got == nil {
		t.Fatal("previewURLFromProto returned nil")
	}
	if got.Wildcard {
		t.Fatalf("Wildcard = true for non-wildcard preview, want false")
	}
	if got.WildcardStatus != WildcardStatusUnspecified {
		t.Fatalf("WildcardStatus = %q, want %q", got.WildcardStatus, WildcardStatusUnspecified)
	}
	if got.WildcardStatusReason != "" {
		t.Fatalf("WildcardStatusReason = %q, want empty", got.WildcardStatusReason)
	}
}
