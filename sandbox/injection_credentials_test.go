package sandbox

import (
	"connectrpc.com/connect"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	pb "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const injectionCanary = "SYNTHETIC_CANARY_P4_NOT_A_CREDENTIAL"

type injectionHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	public       *pb.CreateSessionWithPublicEgressRequest
	egress       *pb.SessionEgressPolicy
	ordinary     *pb.CreateSessionRequest
	create       *pb.CreateSessionWithInjectionCredentialsRequest
	set          *pb.SetSessionInjectionCredentialRequest
	revoke       *pb.RevokeSessionInjectionCredentialRequest
	code         connect.Code
	rpcFailure   error
	acknowledge  bool
	terminated   string
	terminateErr error
}

func (h *injectionHandler) created() *pb.CreateSessionResponse {
	if h.acknowledge {
		req := h.ordinary
		if h.create != nil {
			req = h.create.Session
		}
		return &pb.CreateSessionResponse{Session: &pb.SandboxSession{Id: "session-synthetic", State: pb.SessionState_SESSION_STATE_RUNNING, TailnetStatus: &pb.TailnetStatus{AcceptDns: req.GetTailnet().AcceptDns, ExposedPorts: req.GetTailnet().ExposedPorts}}}
	}
	return &pb.CreateSessionResponse{Session: &pb.SandboxSession{Id: "session-synthetic", State: pb.SessionState_SESSION_STATE_RUNNING, Egress: h.egress}}
}
func (*injectionHandler) metadata() *pb.SessionInjectionCredentialMetadata {
	return &pb.SessionInjectionCredentialMetadata{CredentialId: "credential-synthetic", Name: "SYNTHETIC", Revision: 7}
}
func (h *injectionHandler) failure() error {
	if h.rpcFailure != nil {
		return h.rpcFailure
	}
	if h.code != 0 {
		return connect.NewError(h.code, errors.New(injectionCanary))
	}
	return nil
}
func (h *injectionHandler) CreateSession(_ context.Context, req *connect.Request[pb.CreateSessionRequest]) (*connect.Response[pb.CreateSessionResponse], error) {
	h.ordinary = req.Msg
	return connect.NewResponse(h.created()), nil
}
func (h *injectionHandler) CreateSessionWithInjectionCredentials(_ context.Context, req *connect.Request[pb.CreateSessionWithInjectionCredentialsRequest]) (*connect.Response[pb.CreateSessionWithInjectionCredentialsResponse], error) {
	h.create = req.Msg
	return connect.NewResponse(&pb.CreateSessionWithInjectionCredentialsResponse{Created: h.created(), Credentials: []*pb.SessionInjectionCredentialMetadata{h.metadata()}}), h.failure()
}
func (h *injectionHandler) SetSessionInjectionCredential(_ context.Context, req *connect.Request[pb.SetSessionInjectionCredentialRequest]) (*connect.Response[pb.SetSessionInjectionCredentialResponse], error) {
	h.set = req.Msg
	return connect.NewResponse(&pb.SetSessionInjectionCredentialResponse{Credential: h.metadata()}), h.failure()
}
func (h *injectionHandler) RevokeSessionInjectionCredential(_ context.Context, req *connect.Request[pb.RevokeSessionInjectionCredentialRequest]) (*connect.Response[pb.RevokeSessionInjectionCredentialResponse], error) {
	h.revoke = req.Msg
	m := h.metadata()
	m.Revoked = true
	return connect.NewResponse(&pb.RevokeSessionInjectionCredentialResponse{Credential: m}), h.failure()
}
func injectionTestClient(t *testing.T, h sandboxv1connect.SandboxServiceHandler) *Client {
	t.Helper()
	path, handler := sandboxv1connect.NewSandboxServiceHandler(h)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewUnstartedServer(mux)
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetHTTP1(true)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	t.Cleanup(server.Close)
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
func injectionTestInput() InjectionCredentialInput {
	return InjectionCredentialInput{Name: "SYNTHETIC", Value: []byte(injectionCanary), Rules: []InjectionRule{{Origin: "https://example.invalid", Methods: []string{"GET"}, PathPrefix: "/repo", Header: "Authorization"}}}
}
func TestInjectionCredentialWireAndOrdinaryCreate(t *testing.T) {
	h := &injectionHandler{}
	client := injectionTestClient(t, h)
	input := injectionTestInput()
	opts := []CreateOption{WithName("synthetic"), WithEnvs(map[string]string{"EXISTING": "unchanged"}), WithGitHubToken("SYNTHETIC_GITHUB_CANARY"), WithWaitReady(false)}
	if _, err := client.Create(t.Context(), opts...); err != nil {
		t.Fatal(err)
	}
	session, metadata, err := client.CreateWithInjectionCredentials(t.Context(), []InjectionCredentialInput{input}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(h.ordinary, h.create.Session) || !proto.Equal(input.toProto(), h.create.Credentials[0]) {
		t.Fatal("wire shape differs")
	}
	if session.ID != "session-synthetic" || len(metadata) != 1 || metadata[0].Revision != 7 {
		t.Fatal("incorrect result")
	}
	for _, revision := range []*uint32{nil, proto.Uint32(0), proto.Uint32(7)} {
		got, err := session.SetInjectionCredential(t.Context(), input, revision)
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(h.set, &pb.SetSessionInjectionCredentialRequest{SessionId: session.ID, Credential: input.toProto(), ExpectedRevision: revision}) || got != metadata[0] {
			t.Fatal("set wire shape differs")
		}
		revoked, err := session.RevokeInjectionCredential(t.Context(), input.Name, revision)
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(h.revoke, &pb.RevokeSessionInjectionCredentialRequest{SessionId: session.ID, Name: input.Name, ExpectedRevision: revision}) || !revoked.Revoked {
			t.Fatal("revoke wire shape differs")
		}
	}
	h.create = nil
	if _, _, err := client.CreateWithInjectionCredentials(t.Context(), nil, opts...); err != nil {
		t.Fatal(err)
	}
	if h.create == nil || len(h.create.Credentials) != 0 {
		t.Fatal("empty credential create used wrong RPC")
	}
}
func TestInjectionCredentialErrorsAndRedaction(t *testing.T) {
	h := &injectionHandler{}
	client := injectionTestClient(t, h)
	session, _ := client.Create(t.Context())
	input := injectionTestInput()
	wire, _ := json.Marshal(input)
	output := fmt.Sprintf("%v %+v %#v %s %x %v", input, input, input, input, input, []InjectionCredentialInput{input}) + string(wire)
	for _, code := range []connect.Code{connect.CodePermissionDenied, connect.CodeFailedPrecondition, connect.CodeAborted, connect.CodeUnimplemented} {
		h.code = code
		_, _, createErr := client.CreateWithInjectionCredentials(t.Context(), []InjectionCredentialInput{input}, WithPublicEgress(PublicEgress{}))
		_, setErr := session.SetInjectionCredential(t.Context(), input, nil)
		_, revokeErr := session.RevokeInjectionCredential(t.Context(), input.Name, nil)
		for _, err := range []error{createErr, setErr, revokeErr} {
			if connect.CodeOf(err) != code {
				t.Fatal("error code lost")
			}
			if code == connect.CodePermissionDenied && !errors.Is(err, ErrPermissionDenied) {
				t.Fatal("permission mapping lost")
			}
			if code == connect.CodeFailedPrecondition && !errors.Is(err, ErrInvalidState) {
				t.Fatal("precondition mapping lost")
			}
			output += fmt.Sprintf("%v %+v %#v", err, err, err)
		}
	}
	if strings.Contains(output, injectionCanary) || strings.Contains(output, base64.StdEncoding.EncodeToString(input.Value)) {
		t.Fatal("credential leaked")
	}
}

func (h *waitSessionHandler) CreateSessionWithInjectionCredentials(ctx context.Context, req *connect.Request[pb.CreateSessionWithInjectionCredentialsRequest]) (*connect.Response[pb.CreateSessionWithInjectionCredentialsResponse], error) {
	response, err := h.CreateSession(ctx, connect.NewRequest(req.Msg.GetSession()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.CreateSessionWithInjectionCredentialsResponse{Created: response.Msg}), nil
}

func TestInjectionCredentialCreatePartialFailureIdentity(t *testing.T) {
	for _, runtimeFailure := range []bool{false, true} {
		handler := &waitSessionHandler{runtimeFailure: runtimeFailure, waitErr: connect.NewError(connect.CodePermissionDenied, errors.New("synthetic wait denial"))}
		server, client := newWaitSessionTestServer(t, handler)
		t.Cleanup(server.Close)
		_, _, err := client.CreateWithInjectionCredentials(t.Context(), nil)
		var session *Session
		if runtimeFailure {
			var failure *TemplateRuntimeFailedError
			if !errors.As(err, &failure) {
				t.Fatalf("unexpected error type %T", err)
			}
			session = failure.Session
		} else {
			var failure *WaitReadyFailedError
			if !errors.As(err, &failure) {
				t.Fatalf("unexpected error type %T", err)
			}
			session = failure.Session
		}
		if session == nil || session.ID != "019e84bc-6df8-765f-8507-2734f87156c7" {
			t.Fatal("cleanup identity lost")
		}
		if err := session.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if handler.terminateCalls.Load() != 1 {
			t.Fatal("admitted session not terminated")
		}
	}
}

func (h *injectionHandler) TerminateSession(_ context.Context, req *connect.Request[pb.TerminateSessionRequest]) (*connect.Response[pb.TerminateSessionResponse], error) {
	h.terminated = req.Msg.GetSessionId()
	return connect.NewResponse(&pb.TerminateSessionResponse{}), h.terminateErr
}

func TestInjectionCredentialTailnetCompatibility(t *testing.T) {
	for _, wait := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			dns   *bool
			ports []TailnetPort
		}{
			{name: "omitted"},
			{name: "dns_disabled", dns: proto.Bool(false)},
			{name: "dns_enabled", dns: proto.Bool(true)},
			{name: "udp", ports: []TailnetPort{{Port: 6081, Protocol: "udp"}}},
		} {
			for _, acknowledged := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/wait=%t/acknowledged=%t", tc.name, wait, acknowledged), func(t *testing.T) {
					h := &injectionHandler{acknowledge: acknowledged}
					client := injectionTestClient(t, h)
					opts := []CreateOption{WithWaitReady(wait), WithTailnet(TailnetAttachment{ProfileID: "11111111-1111-4111-8111-111111111111", AcceptDNS: tc.dns, ExposedPorts: tc.ports})}
					_, ordinaryErr := client.Create(t.Context(), opts...)
					h.terminated = ""
					session, _, err := client.CreateWithInjectionCredentials(t.Context(), nil, opts...)
					if !proto.Equal(h.ordinary, h.create.Session) {
						t.Fatal("create options differ")
					}
					tailnet := h.create.Session.Tailnet
					if (tailnet.AcceptDns == nil) != (tc.dns == nil) || tailnet.GetAcceptDns() != (tc.dns != nil && *tc.dns) || len(tailnet.ExposedPorts) != len(tc.ports) {
						t.Fatal("tailnet options lost")
					}
					if len(tc.ports) > 0 && (tailnet.ExposedPorts[0].Port != 6081 || tailnet.ExposedPorts[0].Protocol != pb.TailnetPortProtocol_TAILNET_PORT_PROTOCOL_UDP) {
						t.Fatal("typed port lost")
					}
					rejected := !acknowledged && tc.name != "omitted"
					if (err != nil) != rejected || (ordinaryErr != nil) != rejected {
						t.Fatalf("unexpected compatibility result: ordinary=%v credential=%v", ordinaryErr, err)
					}
					if rejected {
						if session != nil || h.terminated != "session-synthetic" {
							t.Fatal("unacknowledged allocation not cleaned up")
						}
					} else if session == nil || h.terminated != "" {
						t.Fatal("acknowledged allocation not retained")
					}
				})
			}
		}
	}
}

func (h *injectionHandler) CreateSessionWithPublicEgress(
	_ context.Context, req *connect.Request[pb.CreateSessionWithPublicEgressRequest],
) (*connect.Response[pb.CreateSessionWithPublicEgressResponse], error) {
	h.public = req.Msg
	return connect.NewResponse(&pb.CreateSessionWithPublicEgressResponse{Response: h.created()}), nil
}

func TestInjectionCredentialPublicEgressCompatibility(t *testing.T) {
	cases := []struct {
		wait      bool
		name      string
		requested *PublicEgress
		accepted  *pb.PublicEgress
		rejected  bool
	}{
		{name: "omitted"},
		{name: "deny_all", requested: &PublicEgress{}, accepted: &pb.PublicEgress{}},
		{name: "narrowed", requested: &PublicEgress{TCPPorts: []uint32{80, 443}}, accepted: &pb.PublicEgress{TcpPorts: []uint32{443}}},
		{name: "missing", requested: &PublicEgress{}, rejected: true},
		{name: "wider", requested: &PublicEgress{TCPPorts: []uint32{443}}, accepted: &pb.PublicEgress{TcpPorts: []uint32{22}}, rejected: true},
	}
	for _, tc := range cases {
		tc.wait = true
		cases = append(cases, tc)
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/wait=%t", tc.name, tc.wait), func(t *testing.T) {
			h := &injectionHandler{egress: &pb.SessionEgressPolicy{PublicEgress: tc.accepted}}
			client := injectionTestClient(t, h)
			opts := []CreateOption{WithWaitReady(tc.wait)}
			if tc.requested != nil {
				opts = append(opts, WithPublicEgress(*tc.requested))
			}
			_, ordinaryErr := client.Create(t.Context(), opts...)
			ordinary := h.ordinary
			if (h.public != nil) != (tc.requested != nil) || (h.ordinary != nil) != (tc.requested == nil) {
				t.Fatal("plain create changed RPC selection")
			}
			if h.public != nil {
				ordinary = h.public.Request
			}
			if (h.terminated != "") != tc.rejected {
				t.Fatal("plain create cleanup differs")
			}
			h.terminated = ""
			session, metadata, err := client.CreateWithInjectionCredentials(t.Context(), []InjectionCredentialInput{injectionTestInput()}, opts...)
			if len(metadata) != 1 || metadata[0].CredentialID != "credential-synthetic" || metadata[0].Revision != 7 {
				t.Fatal("credential metadata lost")
			}
			if !proto.Equal(ordinary, h.create.GetSession()) {
				t.Fatal("inner create options differ")
			}
			if (err != nil) != tc.rejected || (ordinaryErr != nil) != tc.rejected {
				t.Fatal("unexpected compatibility result")
			}
			if (session == nil) != tc.rejected || (h.terminated == "session-synthetic") != tc.rejected {
				t.Fatal("allocation cleanup differs")
			}
			if err != nil && !strings.Contains(err.Error(), "does not support public egress") {
				t.Fatal("compatibility error lost")
			}
		})
	}
}

func TestInjectionCredentialRuntimeFailurePublicEgressCleanup(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup_failure=%t", cleanupFails), func(t *testing.T) {
			failure := connect.NewError(connect.CodeFailedPrecondition, errors.New(injectionCanary))
			detail, err := connect.NewErrorDetail(&pb.TemplateRuntimeFailure{Reason: injectionCanary, Session: &pb.SandboxSession{
				Id: "session-synthetic", State: pb.SessionState_SESSION_STATE_RUNNING,
				Metadata: map[string]string{"echo": injectionCanary},
			}})
			if err != nil {
				t.Fatal(err)
			}
			failure.AddDetail(detail)
			handler := &injectionHandler{rpcFailure: failure}
			if cleanupFails {
				handler.terminateErr = connect.NewError(connect.CodePermissionDenied, errors.New("synthetic cleanup denial"))
			}
			client := injectionTestClient(t, handler)
			session, metadata, err := client.CreateWithInjectionCredentials(t.Context(), []InjectionCredentialInput{injectionTestInput()}, WithPublicEgress(PublicEgress{}))
			if session != nil || err == nil || !strings.Contains(err.Error(), "does not support public egress") {
				t.Fatal("runtime failure bypassed public egress verification")
			}
			var runtimeFailure *TemplateRuntimeFailedError
			if !errors.As(err, &runtimeFailure) || !errors.Is(err, ErrTemplateRuntimeFailed) {
				t.Fatalf("runtime classification lost: %v", err)
			}
			if runtimeFailure.Session.ID != "session-synthetic" || len(runtimeFailure.Session.Metadata) != 0 || len(metadata) != 0 {
				t.Fatal("unexpected runtime identity or credential metadata")
			}
			if handler.terminated != "session-synthetic" {
				t.Fatal("runtime failure allocation not cleaned up")
			}
			if cleanupFails && !errors.Is(err, ErrPermissionDenied) {
				t.Fatal("cleanup failure classification lost")
			}
			if strings.Contains(err.Error(), injectionCanary) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestInjectionCredentialTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		code    connect.Code
		message string
		want    error
	}{
		{connect.CodeNotFound, "template not found", ErrTemplateNotFound},
		{connect.CodeNotFound, "egress policy not found", ErrEgressPolicyNotFound},
		{connect.CodeFailedPrecondition, "session terminated", ErrSessionTerminated},
		{connect.CodeResourceExhausted, "rate limit", ErrRateLimited},
		{connect.CodeResourceExhausted, "capacity unavailable", ErrCapacityUnavailable},
	} {
		t.Run(tc.message, func(t *testing.T) {
			h := &injectionHandler{rpcFailure: connect.NewError(tc.code, errors.New(tc.message+injectionCanary))}
			client := injectionTestClient(t, h)
			session, err := client.Create(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, _, createErr := client.CreateWithInjectionCredentials(t.Context(), []InjectionCredentialInput{injectionTestInput()})
			_, setErr := session.SetInjectionCredential(t.Context(), injectionTestInput(), nil)
			_, revokeErr := session.RevokeInjectionCredential(t.Context(), "SYNTHETIC", nil)
			for _, err := range []error{createErr, setErr, revokeErr} {
				if !errors.Is(err, tc.want) || connect.CodeOf(err) != tc.code {
					t.Fatalf("wrong classification: %v", err)
				}
				if strings.Contains(fmt.Sprintf("%+v %#v", err, err), injectionCanary) {
					t.Fatal("remote text leaked")
				}
			}
		})
	}
}

func TestInjectionCredentialMutationRuntimeFailure(t *testing.T) {
	failure := connect.NewError(connect.CodeFailedPrecondition, errors.New(injectionCanary))
	detail, err := connect.NewErrorDetail(&pb.TemplateRuntimeFailure{Reason: injectionCanary, Session: &pb.SandboxSession{Id: "session-synthetic", State: pb.SessionState_SESSION_STATE_RUNNING, Metadata: map[string]string{"echo": injectionCanary}}})
	if err != nil {
		t.Fatal(err)
	}
	failure.AddDetail(detail)
	h := &injectionHandler{rpcFailure: failure}
	client := injectionTestClient(t, h)
	session, err := client.Create(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, setErr := session.SetInjectionCredential(t.Context(), injectionTestInput(), nil)
	_, revokeErr := session.RevokeInjectionCredential(t.Context(), "SYNTHETIC", nil)
	for _, err := range []error{setErr, revokeErr} {
		var runtimeFailure *TemplateRuntimeFailedError
		if !errors.Is(err, ErrTemplateRuntimeFailed) || !errors.As(err, &runtimeFailure) {
			t.Fatalf("runtime classification lost: %v", err)
		}
		if runtimeFailure.Session.ID != session.ID || len(runtimeFailure.Session.Metadata) != 0 {
			t.Fatal("identity not sanitized")
		}
		if strings.Contains(fmt.Sprintf("%+v %#v", err, err), injectionCanary) {
			t.Fatal("remote text leaked")
		}
	}
}

func TestInjectionCredentialContextErrors(t *testing.T) {
	client := injectionTestClient(t, &injectionHandler{})
	session, err := client.Create(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		}
		cancel()
		_, _, createErr := client.CreateWithInjectionCredentials(ctx, []InjectionCredentialInput{injectionTestInput()})
		_, setErr := session.SetInjectionCredential(ctx, injectionTestInput(), nil)
		_, revokeErr := session.RevokeInjectionCredential(ctx, "SYNTHETIC", nil)
		for _, err := range []error{createErr, setErr, revokeErr} {
			if !errors.Is(err, want) {
				t.Fatalf("wanted %v, got %v", want, err)
			}
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := safeInjectionError(fmt.Errorf("%s: %w", injectionCanary, cause), client)
		if !errors.Is(err, cause) || strings.Contains(err.Error(), injectionCanary) {
			t.Fatalf("unsafe cancellation: %v", err)
		}
	}
}

func TestInjectionCredentialRuntimeFailurePreservesPublicEgress(t *testing.T) {
	failure := connect.NewError(connect.CodeFailedPrecondition, errors.New(injectionCanary))
	detail, err := connect.NewErrorDetail(&pb.TemplateRuntimeFailure{Reason: injectionCanary, Session: &pb.SandboxSession{
		Id: "session-synthetic", State: pb.SessionState_SESSION_STATE_RUNNING,
		Egress:   &pb.SessionEgressPolicy{PublicEgress: &pb.PublicEgress{TcpPorts: []uint32{443}}},
		Metadata: map[string]string{"echo": injectionCanary},
	}})
	if err != nil {
		t.Fatal(err)
	}
	failure.AddDetail(detail)
	h := &injectionHandler{rpcFailure: failure}
	client := injectionTestClient(t, h)
	_, _, err = client.CreateWithInjectionCredentials(t.Context(), []InjectionCredentialInput{injectionTestInput()}, WithPublicEgress(PublicEgress{TCPPorts: []uint32{443}}))
	var runtimeFailure *TemplateRuntimeFailedError
	if !errors.As(err, &runtimeFailure) {
		t.Fatalf("wanted runtime failure, got %v", err)
	}
	if h.terminated != "" {
		t.Fatal("acknowledged allocation terminated")
	}
	if strings.Contains(fmt.Sprintf("%+v", err), injectionCanary) || len(runtimeFailure.Session.Metadata) != 0 {
		t.Fatal("remote text leaked")
	}
}

func TestInjectionCredentialCopiesWireInput(t *testing.T) {
	input := injectionTestInput()
	wire := input.toProto()
	input.Value[0] = 'X'
	input.Rules[0].Origin = "https://changed.invalid"
	input.Rules[0].Methods[0] = "POST"
	if string(wire.Value) != injectionCanary || wire.Rules[0].Origin != "https://example.invalid" || wire.Rules[0].Methods[0] != "GET" {
		t.Fatal("wire input aliased caller data")
	}
}

func TestInjectionCredentialSafeDiagnostics(t *testing.T) {
	capability := safeInjectionError(connect.NewError(connect.CodeUnimplemented, errors.New("unknown request type: *sandboxv1.GuestRequest_Stat "+injectionCanary)), nil)
	var unavailable *CapabilityUnavailableError
	if !errors.As(capability, &unavailable) || unavailable.Primitive != "Stat" {
		t.Fatalf("capability primitive lost: %v", capability)
	}
	git := safeInjectionError(connect.NewError(connect.CodeFailedPrecondition, errors.New("git clone failed (exit_code=128); check the repository, ref and access token: "+injectionCanary)), nil)
	var failed *GitOperationFailedError
	if !errors.As(git, &failed) || failed.Stderr != "" || failed.ExitCode == nil || *failed.ExitCode != 128 {
		t.Fatalf("unexpected git diagnostics: %v", git)
	}
	if strings.Contains(fmt.Sprintf("%+v %+v", capability, git), injectionCanary) {
		t.Fatal("credential leaked")
	}
}
