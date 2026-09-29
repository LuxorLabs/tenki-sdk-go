package sandbox

import (
	"errors"
	"fmt"
	"strings"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

type TailnetExitPolicy string

const (
	TailnetExitPolicyTenkiAllowlist  TailnetExitPolicy = "tenki_allowlist"
	TailnetExitPolicyExitNodeManaged TailnetExitPolicy = "exit_node_managed"
)

type TailnetEphemeralPausePolicy string

const (
	TailnetEphemeralPausePolicyReject           TailnetEphemeralPausePolicy = "reject"
	TailnetEphemeralPausePolicyRecreateOnResume TailnetEphemeralPausePolicy = "recreate_on_resume"
)

type TailnetState string

type TailnetProvider string

const TailnetProviderTailscale TailnetProvider = "tailscale"

const (
	TailnetStatePending  TailnetState = "pending"
	TailnetStateJoining  TailnetState = "joining"
	TailnetStateOnline   TailnetState = "online"
	TailnetStateOffline  TailnetState = "offline"
	TailnetStateError    TailnetState = "error"
	TailnetStateDetached TailnetState = "detached"
)

type TailnetAuthKey struct {
	value string
}

func NewTailnetAuthKey(value string) TailnetAuthKey {
	return TailnetAuthKey{value: value}
}

func (TailnetAuthKey) String() string {
	return "[REDACTED]"
}

func (TailnetAuthKey) GoString() string {
	return "sandbox.TailnetAuthKey([REDACTED])"
}

func (TailnetAuthKey) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED]"))
}

type TailnetAttachment struct {
	AuthKey              TailnetAuthKey
	Provider             TailnetProvider
	Hostname             string
	Tags                 []string
	Ephemeral            bool
	ControlURL           string
	ExitNode             string
	AcceptRoutes         bool
	WakeOnConnect        bool
	ExposePorts          []uint32
	WaitForOnline        bool
	ExitPolicy           TailnetExitPolicy
	EphemeralPausePolicy TailnetEphemeralPausePolicy
}

func (value TailnetAttachment) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprintf(state,
		"TailnetAttachment{AuthKey:[REDACTED] Provider:%q Hostname:%q Tags:%v Ephemeral:%t ControlURL:%q ExitNode:%q AcceptRoutes:%t WakeOnConnect:%t ExposePorts:%v WaitForOnline:%t ExitPolicy:%q EphemeralPausePolicy:%q}",
		value.Provider, value.Hostname, value.Tags, value.Ephemeral, value.ControlURL, value.ExitNode, value.AcceptRoutes,
		value.WakeOnConnect, value.ExposePorts, value.WaitForOnline, value.ExitPolicy, value.EphemeralPausePolicy,
	)
}

type TailnetStatus struct {
	Provider       string
	State          TailnetState
	NodeID         string
	FQDN           string
	IPs            []string
	Tags           []string
	Owner          string
	Error          string
	DeviceRetained bool
}

func cloneTailnetAttachment(value TailnetAttachment) TailnetAttachment {
	value.Tags = append([]string(nil), value.Tags...)
	value.ExposePorts = append([]uint32(nil), value.ExposePorts...)
	return value
}

func tailnetAttachmentProto(value TailnetAttachment) (*sandboxv1.TailnetAttachment, error) {
	provider := value.Provider
	if provider == "" {
		provider = TailnetProviderTailscale
	}
	if provider != TailnetProviderTailscale {
		return nil, fmt.Errorf("sandbox: unsupported tailnet provider %q", provider)
	}
	if strings.TrimSpace(value.AuthKey.value) == "" {
		return nil, errors.New("sandbox: tailnet auth key is required")
	}
	exitPolicy, err := tailnetExitPolicyProto(value.ExitPolicy)
	if err != nil {
		return nil, err
	}
	pausePolicy, err := tailnetEphemeralPausePolicyProto(value.EphemeralPausePolicy)
	if err != nil {
		return nil, err
	}
	return &sandboxv1.TailnetAttachment{
		Provider:             string(provider),
		Hostname:             value.Hostname,
		Tags:                 append([]string(nil), value.Tags...),
		Ephemeral:            value.Ephemeral,
		ControlUrl:           value.ControlURL,
		ExitNode:             value.ExitNode,
		AcceptRoutes:         value.AcceptRoutes,
		WakeOnConnect:        value.WakeOnConnect,
		ExposePorts:          append([]uint32(nil), value.ExposePorts...),
		WaitForOnline:        value.WaitForOnline,
		Credential:           &sandboxv1.TailnetAttachment_AuthKey{AuthKey: value.AuthKey.value},
		ExitPolicy:           exitPolicy,
		EphemeralPausePolicy: pausePolicy,
	}, nil
}

func tailnetExitPolicyProto(value TailnetExitPolicy) (sandboxv1.TailnetExitPolicy, error) {
	switch value {
	case "":
		return sandboxv1.TailnetExitPolicy_TAILNET_EXIT_POLICY_UNSPECIFIED, nil
	case TailnetExitPolicyTenkiAllowlist:
		return sandboxv1.TailnetExitPolicy_TAILNET_EXIT_POLICY_TENKI_ALLOWLIST, nil
	case TailnetExitPolicyExitNodeManaged:
		return sandboxv1.TailnetExitPolicy_TAILNET_EXIT_POLICY_EXIT_NODE_MANAGED, nil
	default:
		return sandboxv1.TailnetExitPolicy_TAILNET_EXIT_POLICY_UNSPECIFIED, fmt.Errorf("sandbox: unsupported tailnet exit policy %q", value)
	}
}

func tailnetEphemeralPausePolicyProto(value TailnetEphemeralPausePolicy) (sandboxv1.TailnetEphemeralPausePolicy, error) {
	switch value {
	case "":
		return sandboxv1.TailnetEphemeralPausePolicy_TAILNET_EPHEMERAL_PAUSE_POLICY_UNSPECIFIED, nil
	case TailnetEphemeralPausePolicyReject:
		return sandboxv1.TailnetEphemeralPausePolicy_TAILNET_EPHEMERAL_PAUSE_POLICY_REJECT, nil
	case TailnetEphemeralPausePolicyRecreateOnResume:
		return sandboxv1.TailnetEphemeralPausePolicy_TAILNET_EPHEMERAL_PAUSE_POLICY_RECREATE_ON_RESUME, nil
	default:
		return sandboxv1.TailnetEphemeralPausePolicy_TAILNET_EPHEMERAL_PAUSE_POLICY_UNSPECIFIED, fmt.Errorf("sandbox: unsupported tailnet ephemeral pause policy %q", value)
	}
}

func tailnetStatusFromProto(value *sandboxv1.TailnetStatus) *TailnetStatus {
	if value == nil {
		return nil
	}
	provider := value.GetProvider()
	if provider == "" {
		provider = string(TailnetProviderTailscale)
	}
	return &TailnetStatus{
		Provider:       provider,
		State:          TailnetState(value.GetState()),
		NodeID:         value.GetNodeId(),
		FQDN:           value.GetFqdn(),
		IPs:            append([]string(nil), value.GetIps()...),
		Tags:           append([]string(nil), value.GetTags()...),
		Owner:          value.GetOwner(),
		Error:          value.GetError(),
		DeviceRetained: value.GetDeviceRetained(),
	}
}
