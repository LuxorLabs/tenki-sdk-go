package sandbox

import (
	"context"
	"testing"
)

func TestResolveDataPlaneRejectsSessionWithoutClient(t *testing.T) {
	_, err := (&Session{ID: "session-1"}).resolveDataPlane(context.Background(), false)
	if err == nil || err.Error() != "sandbox: nil session" {
		t.Fatalf("expected nil session error, got %v", err)
	}
}
