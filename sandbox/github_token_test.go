package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
)

const githubCanary = "SYNTHETIC_CANARY_P8_NOT_A_CREDENTIAL"

type githubHandler struct {
	injectionHandler
	sets   []*pb.SetSessionInjectionCredentialRequest
	failAt int
}

func (h *githubHandler) SetSessionInjectionCredential(ctx context.Context, req *connect.Request[pb.SetSessionInjectionCredentialRequest]) (*connect.Response[pb.SetSessionInjectionCredentialResponse], error) {
	h.sets = append(h.sets, req.Msg)
	if h.failAt == len(h.sets) {
		h.code = connect.CodeFailedPrecondition
	}
	return h.injectionHandler.SetSessionInjectionCredential(ctx, req)
}

func TestGitHubTokenInjectionCreate(t *testing.T) {
	h := &injectionHandler{}
	client := injectionTestClient(t, h)
	opts := []CreateOption{WithGitHubToken(githubCanary), WithWaitReady(false)}
	_, err := client.Create(t.Context(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	golden := proto.Clone(h.ordinary).(*pb.CreateSessionRequest)
	if !reflect.DeepEqual(golden.Env, map[string]string{"GH_TOKEN": githubCanary, "GIT_TOKEN": githubCanary}) {
		t.Fatal("default environment changed")
	}
	_, err = client.Create(t.Context(), append(opts, WithGitHubTokenInjection(false))...)
	if err != nil || !proto.Equal(golden, h.ordinary) || h.create != nil {
		t.Fatal("disabled request changed")
	}
	session, err := client.Create(t.Context(), append(opts, WithGitHubTokenInjection(true))...)
	if err != nil {
		t.Fatal(err)
	}
	req := h.create
	env := req.Session.Env
	if env["GH_TOKEN"] != "secrets://SDK_GITHUB_API" || env["GIT_TOKEN"] != env["GH_TOKEN"] {
		t.Fatal("wrong placeholders")
	}
	serialized, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), githubCanary) || strings.Contains(string(serialized), base64.StdEncoding.EncodeToString([]byte("x-access-token:"+githubCanary))) {
		t.Fatal("credential in guest env")
	}
	if len(req.Credentials) != 2 || string(req.Credentials[0].Value) != githubCanary || string(req.Credentials[1].Value) != base64.StdEncoding.EncodeToString([]byte("x-access-token:"+githubCanary)) {
		t.Fatal("wrong credentials")
	}
	want := []*pb.SessionInjectionRule{{Origin: "https://github.com", Methods: []string{"GET", "POST"}, PathPrefix: "/", Header: "Authorization"}}
	if !proto.Equal(req.Credentials[1].Rules[0], want[0]) {
		t.Fatal("wrong git scope")
	}
	if req.Credentials[0].Rules[0].Origin != "https://api.github.com" || req.Credentials[0].Name != "SDK_GITHUB_API" || req.Credentials[1].Name != "SDK_GITHUB_GIT" {
		t.Fatal("wrong API scope/names")
	}
	if env["GIT_CONFIG_VALUE_0"] != "Authorization: Basic secrets://SDK_GITHUB_GIT" || env["GIT_CONFIG_VALUE_1"] != "HTTP/1.1" || env["GIT_CONFIG_VALUE_2"] != "" {
		t.Fatal("wrong git config")
	}
	_, _, err = client.CreateWithInjectionCredentials(t.Context(), nil, append(opts, WithGitHubTokenInjection(true))...)
	if err != nil || !proto.Equal(req, h.create) {
		t.Fatal("credential create changed GitHub injection request", err)
	}
	if err := session.RotateGitHubToken(t.Context(), "SYNTHETIC_ROTATED_CANARY"); err != nil {
		t.Fatal(err)
	}
	if h.set.Credential.Name != "SDK_GITHUB_GIT" || h.set.ExpectedRevision != nil {
		t.Fatal("wrong rotation")
	}
}

func TestGitHubTokenInjectionRefusal(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeFailedPrecondition, connect.CodeUnimplemented, connect.CodePermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			h := &injectionHandler{code: code}
			client := injectionTestClient(t, h)
			_, err := client.Create(t.Context(), WithGitHubToken(githubCanary), WithGitHubTokenInjection(true))
			if err == nil || h.ordinary != nil || strings.Contains(err.Error(), githubCanary) {
				t.Fatal("refusal fell back or leaked")
			}
		})
	}
}

func TestGitHubTokenInjectionValidation(t *testing.T) {
	for _, opts := range [][]CreateOption{
		{}, {WithGitHubToken(" ")},
		{WithGitHubToken(githubCanary), WithEnvs(map[string]string{"GH_HOST": "enterprise.invalid"})},
		{WithGitHubToken(githubCanary), WithEnvs(map[string]string{"GIT_CONFIG_COUNT": "1"})},
		{WithGitHubToken(githubCanary), WithCloneRepo("https://github.com/synthetic/repo")},
	} {
		h := &injectionHandler{}
		client := injectionTestClient(t, h)
		_, err := client.Create(t.Context(), append(opts, WithGitHubTokenInjection(true))...)
		if err == nil || h.ordinary != nil || h.create != nil {
			t.Fatal("invalid opt-in reached transport")
		}
	}
}

func TestGitHubTokenRotationFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run([]string{"API", "Git"}[failAt-1], func(t *testing.T) {
			h := &githubHandler{failAt: failAt}
			client := injectionTestClient(t, h)
			session, err := client.Create(t.Context(), WithGitHubToken(githubCanary), WithGitHubTokenInjection(true))
			if err != nil {
				t.Fatal(err)
			}
			err = session.RotateGitHubToken(t.Context(), githubCanary)
			if !errors.Is(err, ErrInvalidState) || len(h.sets) != failAt {
				t.Fatal("rotation did not stop at failure or preserve error identity", err)
			}
			want := "rotate SDK_GITHUB_API failed:"
			if failAt == 2 {
				want = "rotate SDK_GITHUB_GIT failed (SDK_GITHUB_API already rotated):"
			}
			if !strings.HasPrefix(err.Error(), want) || strings.Contains(err.Error(), injectionCanary) || strings.Contains(err.Error(), githubCanary) {
				t.Fatal("rotation error must identify failed credential and partial completion without secrets", err)
			}
			h.code = 0
			h.failAt = 0
			if err = session.RotateGitHubToken(t.Context(), githubCanary); err != nil || len(h.sets) != failAt+2 {
				t.Fatal("retry did not update both", err)
			}
			if h.sets[failAt].Credential.Name != "SDK_GITHUB_API" || h.sets[failAt+1].Credential.Name != "SDK_GITHUB_GIT" {
				t.Fatal("retry rotation sequence incorrect")
			}
		})
	}
}

type githubGitHandler struct {
	injectionHandler
	calls []*pb.GitOperationRequest
}

func (h *githubGitHandler) GitOperation(_ context.Context, req *connect.Request[pb.GitOperationRequest]) (*connect.Response[pb.GitOperationResponse], error) {
	h.calls = append(h.calls, req.Msg)
	return connect.NewResponse(&pb.GitOperationResponse{Output: "synthetic"}), nil
}

func TestGitHubTokenHelpersContainOnlyPlaceholders(t *testing.T) {
	h := &githubGitHandler{}
	client := injectionTestClient(t, h)
	session, err := client.Create(t.Context(), WithGitHubToken(githubCanary), WithGitHubTokenInjection(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.Git.Clone(t.Context(), "https://github.com/synthetic/repo.git", GitCloneParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err = session.Git.Checkout(t.Context(), "main", GitCheckoutParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err = session.Git.Diff(t.Context(), GitDiffParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err = session.Git.Log(t.Context(), GitLogParams{}); err != nil {
		t.Fatal(err)
	}
	run, endpoint := newDataPlaneRunTestServer(t)
	session.configureDataPlane(endpoint, &pb.SessionCredential{Credential: "SYNTHETIC_SESSION_CREDENTIAL", ExpiresAt: timestamppb.New(time.Now().Add(time.Hour))}, false)
	if _, err = session.Git.FetchPR(t.Context(), 1, GitFetchPRParams{}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"fetch", "push"} {
		if _, err = session.Exec(t.Context(), "git", WithArgs(op, "origin")); err != nil {
			t.Fatal(err)
		}
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if len(h.calls) != 4 || len(run.starts) != 4 {
		t.Fatal("missing git commands")
	}
	raw, err := json.Marshal([]any{h.calls, run.starts, h.create.Session.Env})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{githubCanary, base64.StdEncoding.EncodeToString([]byte("x-access-token:" + githubCanary))} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("credential in helper command or environment")
		}
	}
}

func TestGitHubTokenGitConfigScope(t *testing.T) {
	env := map[string]string{}
	if err := githubInjectionEnv(env); err != nil {
		t.Fatal(err)
	}
	for _, setting := range []struct{ key, want string }{
		{"http.extraHeader", "Authorization: Basic secrets://SDK_GITHUB_GIT"},
		{"http.version", "HTTP/1.1"}, {"credential.helper", ""}, {"http.followRedirects", "false"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", "config", "--get-urlmatch", setting.key, "https://github.com/synthetic/repo.git")
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) != setting.want {
			t.Fatal("git did not apply injected config", err)
		}
	}
}

func TestGitHubTokenGitConfigDoesNotAuthorizeOtherHosts(t *testing.T) {
	env := map[string]string{}
	if err := githubInjectionEnv(env); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"https://enterprise.invalid/synthetic/repo.git", "http://github.com/synthetic/repo.git", "https://github.com.attacker.invalid/repo"} {
		cmd := exec.CommandContext(t.Context(), "git", "config", "--get-urlmatch", "http.extraHeader", url)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		output, err := cmd.Output()
		if err == nil || len(output) != 0 {
			t.Fatal("git authorized an unlisted origin")
		}
	}
}

func TestGitHubTokenInjectionSecretTargetConflicts(t *testing.T) {
	for _, target := range []string{"GH_TOKEN", "GIT_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"} {
		t.Run(target, func(t *testing.T) {
			h := &injectionHandler{}
			client := injectionTestClient(t, h)
			runtime := NewTemplateSpec().Start("app", StartOptions{SecretEnv: map[string]string{target: "token"}})
			_, err := client.Create(t.Context(), WithGitHubToken(githubCanary), WithGitHubTokenInjection(true), WithDirectRuntime(runtime))
			if err == nil || !strings.Contains(err.Error(), "secret target conflicts with session environment") {
				t.Fatal("secret target conflict was not rejected", err)
			}
			if h.ordinary != nil || h.create != nil {
				t.Fatal("secret target conflict reached transport")
			}
		})
	}
}
