package sandbox

import (
	pb "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"testing"
)

func TestGuestControlCapability(t *testing.T) {
	for _, denied := range []bool{false, true} {
		session := &Session{}
		session.apply(&pb.SandboxSession{GuestControlDenied: denied})
		if session.GuestControlDenied != denied {
			t.Fatal("capability lost during response mapping")
		}
		copy := &Session{}
		copy.copyFrom(session)
		if copy.GuestControlDenied != denied {
			t.Fatal("capability lost during refresh")
		}
		session.apply(&pb.SandboxSession{})
		if session.GuestControlDenied {
			t.Fatal("absent capability must retain legacy behavior")
		}
	}
}
