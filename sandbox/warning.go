package sandbox

import (
	"fmt"
	"os"
	"strings"

	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
)

type SandboxWarningCode string

const (
	SandboxWarningCodeUnspecified                SandboxWarningCode = "UNSPECIFIED"
	SandboxWarningCodeStickyOverridesMaxDuration SandboxWarningCode = "STICKY_OVERRIDES_MAX_DURATION"
	SandboxWarningCodeStickyOverridesIdleTimeout SandboxWarningCode = "STICKY_OVERRIDES_IDLE_TIMEOUT"
	SandboxWarningCodeMaxDurationCapped          SandboxWarningCode = "MAX_DURATION_CAPPED"
	SandboxWarningCodeRestoreSecretsSkipped      SandboxWarningCode = "RESTORE_SECRETS_SKIPPED"
	// SandboxWarningCodeSpecVersionDeprecated is returned by template create, update and build calls.
	SandboxWarningCodeSpecVersionDeprecated SandboxWarningCode = "SPEC_VERSION_DEPRECATED"
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

func templateWarningsFromProto(protoWarnings []*sandboxv1.TemplateWarning) []SandboxWarning {
	warnings := make([]SandboxWarning, 0, len(protoWarnings))
	for _, warning := range protoWarnings {
		if warning == nil {
			continue
		}
		code := SandboxWarningCodeUnspecified
		if value := strings.TrimPrefix(warning.GetCode().String(), "TEMPLATE_WARNING_CODE_"); value != warning.GetCode().String() &&
			value != "UNSPECIFIED" {
			code = SandboxWarningCode(value)
		}
		warnings = append(warnings, SandboxWarning{Code: code, Message: warning.GetMessage()})
	}
	return warnings
}

func (c *Client) emitWarnings(warnings []SandboxWarning) {
	if c == nil || c.warningHandler == nil {
		return
	}
	for _, warning := range warnings {
		c.warningHandler(warning)
	}
}

func sandboxWarningCodeFromProto(code sandboxv1.SandboxWarningCode) SandboxWarningCode {
	value := strings.TrimPrefix(code.String(), "SANDBOX_WARNING_CODE_")
	if value == "" || value == code.String() {
		return SandboxWarningCodeUnspecified
	}
	return SandboxWarningCode(value)
}
