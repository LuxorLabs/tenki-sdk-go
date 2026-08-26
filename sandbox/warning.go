package sandbox

import (
	"fmt"
	"os"
	"strings"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type SandboxWarningCode string

const (
	SandboxWarningCodeUnspecified                SandboxWarningCode = "UNSPECIFIED"
	SandboxWarningCodeStickyOverridesMaxDuration SandboxWarningCode = "STICKY_OVERRIDES_MAX_DURATION"
	SandboxWarningCodeStickyOverridesIdleTimeout SandboxWarningCode = "STICKY_OVERRIDES_IDLE_TIMEOUT"
	SandboxWarningCodeMaxDurationCapped          SandboxWarningCode = "MAX_DURATION_CAPPED"
)

type SandboxWarning struct {
	Code    SandboxWarningCode `json:"code"`
	Message string             `json:"message"`
}

type WarningHandler func(SandboxWarning)

func defaultWarningHandler(warning SandboxWarning) {
	_, _ = fmt.Fprintf(os.Stderr, "tenki sandbox warning [%s]: %s\n", warning.Code, warning.Message)
}

func sandboxWarningsFromProto(protoWarnings []*sandboxv1.SandboxWarning) []SandboxWarning {
	warnings := make([]SandboxWarning, 0, len(protoWarnings))
	for _, warning := range protoWarnings {
		if warning == nil {
			continue
		}
		warnings = append(warnings, SandboxWarning{
			Code:    sandboxWarningCodeFromProto(warning.GetCode()),
			Message: warning.GetMessage(),
		})
	}
	return warnings
}

func sandboxWarningCodeFromProto(code sandboxv1.SandboxWarningCode) SandboxWarningCode {
	value := strings.TrimPrefix(code.String(), "SANDBOX_WARNING_CODE_")
	if value == "" || value == code.String() {
		return SandboxWarningCodeUnspecified
	}
	return SandboxWarningCode(value)
}
