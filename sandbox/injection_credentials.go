package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
)

type InjectionRule struct {
	Origin     string
	Methods    []string
	PathPrefix string
	Header     string
}

type InjectionCredentialInput struct {
	Name  string
	Value []byte `json:"-"`
	Rules []InjectionRule
}

func (InjectionCredentialInput) String() string               { return "InjectionCredentialInput([REDACTED])" }
func (i InjectionCredentialInput) GoString() string           { return i.String() }
func (i InjectionCredentialInput) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, i.String()) }

type InjectionCredentialMetadata struct {
	CredentialID string
	Name         string
	Revision     uint32
	Revoked      bool
}

func (i InjectionCredentialInput) toProto() *sandboxv1.SessionInjectionCredentialInput {
	rules := make([]*sandboxv1.SessionInjectionRule, 0, len(i.Rules))
	for _, r := range i.Rules {
		rules = append(rules, &sandboxv1.SessionInjectionRule{
			Origin: r.Origin, Methods: append([]string(nil), r.Methods...),
			PathPrefix: r.PathPrefix, Header: r.Header,
		})
	}
	return &sandboxv1.SessionInjectionCredentialInput{Name: i.Name, Value: append([]byte(nil), i.Value...), Rules: rules}
}

func injectionMetadata(m *sandboxv1.SessionInjectionCredentialMetadata) InjectionCredentialMetadata {
	return InjectionCredentialMetadata{CredentialID: m.GetCredentialId(), Name: m.GetName(), Revision: m.GetRevision(), Revoked: m.GetRevoked()}
}

// safeInjectionError discards remote text and details that could echo credential values.
func safeInjectionError(err error, client *Client) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	safe := connect.NewError(connect.CodeOf(err), errors.New("session injection credential operation failed"))
	var rpcErr *connect.Error
	if errors.As(err, &rpcErr) {
		for _, detail := range rpcErr.Details() {
			value, detailErr := detail.Value()
			if detailErr != nil {
				continue
			}
			if failure, ok := value.(*sandboxv1.TemplateRuntimeFailure); ok {
				return &TemplateRuntimeFailedError{
					Session: newSession(client, &sandboxv1.SandboxSession{
						Id: failure.GetSession().GetId(), State: failure.GetSession().GetState(),
						RuntimeState: failure.GetSession().GetRuntimeState(),
						Egress:       &sandboxv1.SessionEgressPolicy{PublicEgress: failure.GetSession().GetEgress().GetPublicEgress()},
					}),
					Reason: "template runtime failed", Err: safe,
				}
			}
		}
	}
	if rpcErr == nil {
		return safe
	}
	mapped := mapError(rpcErr)
	switch classified := mapped.(type) {
	case interface{ Unwrap() []error }:
		return fmt.Errorf("%w: %w", classified.Unwrap()[0], safe)
	case *GitOperationFailedError:
		return &GitOperationFailedError{Message: safe.Message(), ExitCode: classified.ExitCode, Retryable: classified.Retryable, Err: safe}
	case *FileTooLargeError:
		return &FileTooLargeError{Operation: classified.Operation, SizeBytes: classified.SizeBytes, MaxBytes: classified.MaxBytes, Err: safe}
	case *CapabilityUnavailableError:
		return &CapabilityUnavailableError{Primitive: classified.Primitive, Message: safe.Message(), Err: safe}
	default:
		return mapError(safe)
	}
}
