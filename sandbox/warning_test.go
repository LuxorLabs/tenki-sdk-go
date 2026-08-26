package sandbox

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

type warningTestHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	updateRequest *sandboxv1.UpdateSessionRequest
	updateCalls   int
}

func (h *warningTestHandler) UpdateSession(_ context.Context, req *connect.Request[sandboxv1.UpdateSessionRequest]) (*connect.Response[sandboxv1.UpdateSessionResponse], error) {
	h.updateCalls++
	h.updateRequest = req.Msg
	return connect.NewResponse(&sandboxv1.UpdateSessionResponse{
		Session: &sandboxv1.SandboxSession{Id: "session-1", Sticky: false},
		Warnings: []*sandboxv1.SandboxWarning{{
			Code:    sandboxv1.SandboxWarningCode_SANDBOX_WARNING_CODE_MAX_DURATION_CAPPED,
			Message: "duration capped",
		}},
	}), nil
}

func TestCreateRetainsAndEmitsWarnings(t *testing.T) {
	var emitted []SandboxWarning
	client := &Client{warningHandler: func(warning SandboxWarning) {
		emitted = append(emitted, warning)
	}}
	response := &sandboxv1.CreateSessionResponse{
		Session: &sandboxv1.SandboxSession{Id: "session-1"},
		Warnings: []*sandboxv1.SandboxWarning{{
			Code:    sandboxv1.SandboxWarningCode_SANDBOX_WARNING_CODE_STICKY_OVERRIDES_MAX_DURATION,
			Message: "duration discarded",
		}},
	}

	session := newSessionFromCreate(client, response)

	if len(session.Warnings) != 1 {
		t.Fatalf("warnings got %d, want 1", len(session.Warnings))
	}
	if session.Warnings[0].Code != SandboxWarningCodeStickyOverridesMaxDuration {
		t.Fatalf("warning code got %q", session.Warnings[0].Code)
	}
	if len(emitted) != 1 || emitted[0] != session.Warnings[0] {
		t.Fatalf("emitted warnings got %#v", emitted)
	}
}

func TestCreateCanSuppressWarnings(t *testing.T) {
	client := &Client{}
	response := &sandboxv1.CreateSessionResponse{
		Session:  &sandboxv1.SandboxSession{Id: "session-1"},
		Warnings: []*sandboxv1.SandboxWarning{{Message: "duration discarded"}},
	}

	session := newSessionFromCreate(client, response)

	if len(session.Warnings) != 1 {
		t.Fatalf("warnings got %d, want 1", len(session.Warnings))
	}
}

func TestUpdateRetainsAndEmitsWarnings(t *testing.T) {
	handler := &warningTestHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()
	var emitted []SandboxWarning
	client.warningHandler = func(warning SandboxWarning) {
		emitted = append(emitted, warning)
	}
	session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1", Sticky: true})

	maxDuration := 3 * time.Hour
	if err := session.Update(context.Background(), WithSetSticky(false), WithSetMaxDuration(maxDuration)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if handler.updateRequest == nil || handler.updateRequest.GetMaxDuration().AsDuration() != maxDuration {
		t.Fatalf("max_duration got %v, want %v", handler.updateRequest.GetMaxDuration(), maxDuration)
	}
	if handler.updateRequest.Sticky == nil || handler.updateRequest.GetSticky() {
		t.Fatalf("sticky got %v, want explicit false", handler.updateRequest.Sticky)
	}
	if len(session.Warnings) != 1 || session.Warnings[0].Code != SandboxWarningCodeMaxDurationCapped {
		t.Fatalf("warnings got %#v", session.Warnings)
	}
	if len(emitted) != 1 || emitted[0] != session.Warnings[0] {
		t.Fatalf("emitted warnings got %#v", emitted)
	}
}

func TestMaxDurationOptionsConfigureCreateAndUpdate(t *testing.T) {
	maxDuration := 45 * time.Minute
	createCfg := createConfig{}
	updateCfg := updateSessionConfig{}
	var createOptionFactory func(time.Duration) CreateOption = WithMaxDuration

	createOptionFactory(maxDuration).applyCreate(&createCfg)
	WithSetMaxDuration(maxDuration).applyUpdateSession(&updateCfg)

	if createCfg.maxDuration == nil || *createCfg.maxDuration != maxDuration {
		t.Fatalf("create max duration got %v, want %v", createCfg.maxDuration, maxDuration)
	}
	if updateCfg.maxDuration == nil || *updateCfg.maxDuration != maxDuration {
		t.Fatalf("update max duration got %v, want %v", updateCfg.maxDuration, maxDuration)
	}
}

func TestUpdateRejectsNonPositiveMaxDurationBeforeRPC(t *testing.T) {
	handler := &warningTestHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()
	session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1"})

	for _, maxDuration := range []time.Duration{0, -time.Second} {
		err := session.Update(context.Background(), WithSetSticky(false), WithSetMaxDuration(maxDuration))
		if err == nil || err.Error() != "sandbox: max duration must be positive" {
			t.Fatalf("Update duration %v error got %v", maxDuration, err)
		}
	}
	if handler.updateCalls != 0 {
		t.Fatalf("UpdateSession calls got %d, want 0", handler.updateCalls)
	}
}

func TestUpdateRequiresStickyWithMaxDurationBeforeRPC(t *testing.T) {
	handler := &warningTestHandler{}
	server, client := newWaitSessionTestServer(t, handler)
	defer server.Close()
	session := newSession(client, &sandboxv1.SandboxSession{Id: "session-1"})

	err := session.Update(context.Background(), WithSetMaxDuration(time.Minute))
	if err == nil || err.Error() != "sandbox: sticky must be set when max duration is provided" {
		t.Fatalf("Update error got %v", err)
	}
	if handler.updateCalls != 0 {
		t.Fatalf("UpdateSession calls got %d, want 0", handler.updateCalls)
	}
}
