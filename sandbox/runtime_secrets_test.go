package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	rpc "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

func TestRuntimeSecretReferencesRoundtrip(t *testing.T) {
	refs := map[string]string{"API_TOKEN": "shared-token"}
	for _, spec := range []TemplateSpec{NewTemplateSpec().Start("app", StartOptions{SecretEnv: refs}), NewTemplateSpec().StartArgs([]string{"app"}, StartOptions{SecretEnv: refs}), NewTemplateSpec().ProcessCompose("compose.yaml", ProcessComposeOptions{SecretEnv: refs})} {
		if err := spec.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, err := spec.ToJSON()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := TemplateSpecFromJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := directRuntimeProto(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(runtime.SecretEnv, refs) {
			t.Fatal("roundtrip dropped references")
		}
	}
	for _, spec := range []TemplateSpec{NewTemplateSpec().Run("setup").Start("app"), NewTemplateSpec().Start("app", StartOptions{RunAt: RunAtManual}), NewTemplateSpec().Start("app", StartOptions{SecretEnv: map[string]string{"BAD-TARGET": "token"}}), NewTemplateSpec().RuntimeEnv(map[string]string{"API_TOKEN": "plain"}).Start("app", StartOptions{SecretEnv: refs})} {
		if _, err := directRuntimeProto(spec); err == nil {
			t.Fatal("invalid direct runtime accepted")
		}
	}
}

type runtimeSecretHandler struct {
	rpc.UnimplementedSandboxServiceHandler
	requests []*pb.CreateSessionRequest
}

func (h *runtimeSecretHandler) CreateSession(_ context.Context, req *connect.Request[pb.CreateSessionRequest]) (*connect.Response[pb.CreateSessionResponse], error) {
	h.requests = append(h.requests, req.Msg)
	return connect.NewResponse(&pb.CreateSessionResponse{Session: &pb.SandboxSession{Id: "session", State: pb.SessionState_SESSION_STATE_RUNNING, HasRuntimeSecrets: true}}), nil
}
func TestRuntimeSecretCreateContract(t *testing.T) {
	h := &runtimeSecretHandler{}
	mux := http.NewServeMux()
	path, handler := rpc.NewSandboxServiceHandler(h)
	mux.Handle(path, handler)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	spec := NewTemplateSpec().Workdir("/app").Start("app", StartOptions{SecretEnv: map[string]string{"API_TOKEN": "shared-token"}})
	overrides := map[string]string{"shared-token": "local-token"}
	overrideOption := WithSecretOverrides(overrides)
	overrides["shared-token"] = "mutated"
	session, err := client.Create(context.Background(), WithImage("team/base:v1"), WithDirectRuntime(spec), overrideOption, WithWaitReady(false))
	if err != nil {
		t.Fatal(err)
	}
	req := h.requests[0]
	if req.GetRegistryRef() != "team/base:v1" || req.Runtime.GetStart().Workdir != "/app" || req.Runtime.SecretEnv["API_TOKEN"] != "shared-token" || req.SecretOverrides["shared-token"] != "local-token" {
		t.Fatal("runtime launch fields lost")
	}
	if !session.HasRuntimeSecrets || !snapshotFromProto(&pb.Snapshot{HasRuntimeSecrets: true}).HasRuntimeSecrets {
		t.Fatal("read-only markers lost")
	}
	if _, err := client.Create(context.Background(), WithDirectRuntime(spec), WithEnvs(map[string]string{"API_TOKEN": "plain"}), WithWaitReady(false)); err == nil {
		t.Fatal("session env conflict accepted")
	}
	if _, err := client.Create(context.Background(), WithDirectRuntime(spec), FromTemplateSpec("template"), WithWaitReady(false)); err == nil {
		t.Fatal("template runtime conflict accepted")
	}
	if len(h.requests) != 1 {
		t.Fatal("invalid request reached server")
	}
	if _, err := client.Create(context.Background(), FromTemplateSpec("template"), WithSecretOverrides(map[string]string{"shared-token": "local-token"}), WithWaitReady(false)); err != nil {
		t.Fatal(err)
	}
	if h.requests[1].Runtime != nil || h.requests[1].SecretOverrides["shared-token"] != "local-token" {
		t.Fatal("template override fields lost")
	}
}

func TestRuntimeSecretFilesRoundtrip(t *testing.T) {
	files := []*RuntimeSecretFile{
		{Path: "/app/raw", Format: &RuntimeSecretFileRaw{Raw: &RuntimeSecretReference{Name: "RAW"}}},
		{Path: "/app/.env", Format: &RuntimeSecretFileContent{Content: "TOKEN=secrets://TOKEN"}},
		{Path: "/app/config.yaml", Format: &RuntimeSecretFileSource{Source: "/app/config.tpl.yaml"}},
	}
	for _, spec := range []TemplateSpec{NewTemplateSpec().Start("app", StartOptions{SecretFiles: files}), NewTemplateSpec().StartArgs([]string{"app"}, StartOptions{SecretFiles: files}), NewTemplateSpec().ProcessCompose("compose.yaml", ProcessComposeOptions{SecretFiles: files})} {
		encoded, err := spec.ToJSON()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := TemplateSpecFromJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := directRuntimeProto(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if len(runtime.SecretFiles) != 3 || runtime.SecretFiles[1].GetContent() != "TOKEN=secrets://TOKEN" {
			t.Fatal("file contract lost")
		}
	}
	spec := NewTemplateSpec().Start("app", StartOptions{SecretFiles: files})
	files[0].Path = "/etc/unsafe"
	if spec.Validate() != nil {
		t.Fatal("builder retained caller-owned file declarations")
	}
	if NewTemplateSpec().Start("app", StartOptions{SecretFiles: files}).Validate() == nil {
		t.Fatal("unsafe destination accepted")
	}
}

func TestDirectFileLaunchCarriesUnresolvedContents(t *testing.T) {
	h := &runtimeSecretHandler{}
	mux := http.NewServeMux()
	path, handler := rpc.NewSandboxServiceHandler(h)
	mux.Handle(path, handler)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	file := &RuntimeSecretFile{Path: "/app/env.tpl-sandbox", Format: &RuntimeSecretFileContent{Content: "TOKEN=secrets://TOKEN"}}
	option := WithSecretFiles(file)
	file.Path = "/etc/unsafe"
	if _, err := client.Create(context.Background(), option, WithWaitReady(false)); err != nil {
		t.Fatal(err)
	}
	if h.requests[0].SecretFiles[0].GetContent() != "TOKEN=secrets://TOKEN" || h.requests[0].SecretFiles[0].Path != "/app/env.tpl-sandbox" {
		t.Fatal("file input lost or caller mutation retained")
	}
	names, ok := SecretFileNames(`a=secrets://TOKEN,b=secrets://TOKEN,literal=\secrets://IGNORE,header=\secrets://API`)
	if !ok || len(names) != 1 || names[0] != "TOKEN" {
		t.Fatal("incorrect reference discovery")
	}
}
