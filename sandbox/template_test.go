package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type templateHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler

	createTemplateFn           func(*connect.Request[sandboxv1.CreateTemplateRequest]) (*connect.Response[sandboxv1.CreateTemplateResponse], error)
	getTemplateFn              func(*connect.Request[sandboxv1.GetTemplateRequest]) (*connect.Response[sandboxv1.GetTemplateResponse], error)
	listTemplatesFn            func(*connect.Request[sandboxv1.ListTemplatesRequest]) (*connect.Response[sandboxv1.ListTemplatesResponse], error)
	updateTemplateFn           func(*connect.Request[sandboxv1.UpdateTemplateRequest]) (*connect.Response[sandboxv1.UpdateTemplateResponse], error)
	deleteTemplateFn           func(*connect.Request[sandboxv1.DeleteTemplateRequest]) (*connect.Response[sandboxv1.DeleteTemplateResponse], error)
	buildTemplateFn            func(*connect.Request[sandboxv1.BuildTemplateRequest]) (*connect.Response[sandboxv1.BuildTemplateResponse], error)
	getTemplateBuildFn         func(*connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error)
	listActiveTemplateBuildsFn func(*connect.Request[sandboxv1.ListActiveTemplateBuildsRequest]) (*connect.Response[sandboxv1.ListActiveTemplateBuildsResponse], error)
	cancelTemplateBuildFn      func(*connect.Request[sandboxv1.CancelTemplateBuildRequest]) (*connect.Response[sandboxv1.CancelTemplateBuildResponse], error)
	createSessionFn            func(*connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error)
	getRegistryImageFn         func(*connect.Request[sandboxv1.GetRegistryImageRequest]) (*connect.Response[sandboxv1.GetRegistryImageResponse], error)
	resolveRegistryRefFn       func(*connect.Request[sandboxv1.ResolveRegistryRefRequest]) (*connect.Response[sandboxv1.ResolveRegistryRefResponse], error)
}

func (h *templateHandler) CreateTemplate(_ context.Context, req *connect.Request[sandboxv1.CreateTemplateRequest]) (*connect.Response[sandboxv1.CreateTemplateResponse], error) {
	if h.createTemplateFn != nil {
		return h.createTemplateFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) GetTemplate(_ context.Context, req *connect.Request[sandboxv1.GetTemplateRequest]) (*connect.Response[sandboxv1.GetTemplateResponse], error) {
	if h.getTemplateFn != nil {
		return h.getTemplateFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) ListTemplates(_ context.Context, req *connect.Request[sandboxv1.ListTemplatesRequest]) (*connect.Response[sandboxv1.ListTemplatesResponse], error) {
	if h.listTemplatesFn != nil {
		return h.listTemplatesFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) UpdateTemplate(_ context.Context, req *connect.Request[sandboxv1.UpdateTemplateRequest]) (*connect.Response[sandboxv1.UpdateTemplateResponse], error) {
	if h.updateTemplateFn != nil {
		return h.updateTemplateFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) DeleteTemplate(_ context.Context, req *connect.Request[sandboxv1.DeleteTemplateRequest]) (*connect.Response[sandboxv1.DeleteTemplateResponse], error) {
	if h.deleteTemplateFn != nil {
		return h.deleteTemplateFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) BuildTemplate(_ context.Context, req *connect.Request[sandboxv1.BuildTemplateRequest]) (*connect.Response[sandboxv1.BuildTemplateResponse], error) {
	if h.buildTemplateFn != nil {
		return h.buildTemplateFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) GetTemplateBuild(_ context.Context, req *connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
	if h.getTemplateBuildFn != nil {
		return h.getTemplateBuildFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) ListActiveTemplateBuilds(_ context.Context, req *connect.Request[sandboxv1.ListActiveTemplateBuildsRequest]) (*connect.Response[sandboxv1.ListActiveTemplateBuildsResponse], error) {
	if h.listActiveTemplateBuildsFn != nil {
		return h.listActiveTemplateBuildsFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) CancelTemplateBuild(_ context.Context, req *connect.Request[sandboxv1.CancelTemplateBuildRequest]) (*connect.Response[sandboxv1.CancelTemplateBuildResponse], error) {
	if h.cancelTemplateBuildFn != nil {
		return h.cancelTemplateBuildFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) CreateSession(_ context.Context, req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	if h.createSessionFn != nil {
		return h.createSessionFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) GetRegistryImage(_ context.Context, req *connect.Request[sandboxv1.GetRegistryImageRequest]) (*connect.Response[sandboxv1.GetRegistryImageResponse], error) {
	if h.getRegistryImageFn != nil {
		return h.getRegistryImageFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *templateHandler) ResolveRegistryRef(_ context.Context, req *connect.Request[sandboxv1.ResolveRegistryRefRequest]) (*connect.Response[sandboxv1.ResolveRegistryRefResponse], error) {
	if h.resolveRegistryRefFn != nil {
		return h.resolveRegistryRefFn(req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func newTemplateTestClient(t *testing.T, h *templateHandler) *Client {
	t.Helper()

	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(h)
	mux.Handle(path, svc)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := New(WithAuthToken("tk_test_api_key"), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestCreateTemplate(t *testing.T) {
	t.Parallel()

	h := &templateHandler{}
	h.createTemplateFn = func(req *connect.Request[sandboxv1.CreateTemplateRequest]) (*connect.Response[sandboxv1.CreateTemplateResponse], error) {
		if req.Msg.GetWorkspaceId() != "" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		if req.Msg.GetName() != "python" {
			t.Fatalf("unexpected name: %q", req.Msg.GetName())
		}
		if req.Msg.GetBaseImageId() != "sandbox" {
			t.Fatalf("unexpected base_image_id: %q", req.Msg.GetBaseImageId())
		}
		if req.Msg.GetSetupScript() != "uv sync" {
			t.Fatalf("unexpected setup_script: %q", req.Msg.GetSetupScript())
		}
		if req.Msg.GetStartCmd() != "uv run app" {
			t.Fatalf("unexpected start_cmd: %q", req.Msg.GetStartCmd())
		}
		if req.Msg.GetEnvVars()["FOO"] != "bar" {
			t.Fatalf("unexpected env_vars: %#v", req.Msg.GetEnvVars())
		}
		if req.Msg.GetResources().GetCpuCores() != 4 || req.Msg.GetResources().GetMemoryMb() != 8192 {
			t.Fatalf("unexpected resources: %#v", req.Msg.GetResources())
		}
		if req.Msg.GetParentImage() != "acme/base:prod" {
			t.Fatalf("unexpected parent_image: %q", req.Msg.GetParentImage())
		}
		return connect.NewResponse(&sandboxv1.CreateTemplateResponse{
			Template: &sandboxv1.Template{
				Id:             "tpl-001",
				WorkspaceId:    "ws-001",
				OwnerType:      "workspace",
				OwnerId:        "ws-001",
				Name:           "python",
				BaseImageId:    "sandbox",
				SetupScript:    "uv sync",
				StartCmd:       stringPtr("uv run app"),
				EnvVars:        map[string]string{"FOO": "bar"},
				Resources:      &sandboxv1.TemplateResources{CpuCores: 4, MemoryMb: 8192},
				DefinitionMode: sandboxv1.TemplateDefinitionMode_TEMPLATE_DEFINITION_MODE_LEGACY,
				CreatedAt:      timestamppb.New(time.Unix(1, 0)),
				UpdatedAt:      timestamppb.New(time.Unix(2, 0)),
			},
		}), nil
	}

	client := newTemplateTestClient(t, h)
	template, err := client.CreateTemplate(context.Background(),
		WithTemplateName("python"),
		WithBaseImageID("sandbox"),
		WithSetupScript("uv sync"),
		WithStartCmd("uv run app"),
		WithEnvs(map[string]string{"FOO": "bar"}),
		WithTemplateResources(4, 8192),
		WithParentImage("acme/base:prod"),
	)
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if template.ID != "tpl-001" || template.Resources == nil || template.Resources.CPUCores != 4 {
		t.Fatalf("unexpected template: %#v", template)
	}
	if template.DefinitionMode != TemplateDefinitionModeLegacy {
		t.Fatalf("unexpected definition mode: %q", template.DefinitionMode)
	}
}

func TestTemplateCRUDAndBuild(t *testing.T) {
	t.Parallel()

	h := &templateHandler{}
	h.getTemplateFn = func(req *connect.Request[sandboxv1.GetTemplateRequest]) (*connect.Response[sandboxv1.GetTemplateResponse], error) {
		if req.Msg.GetTemplateId() != "tpl-001" {
			t.Fatalf("unexpected template_id: %q", req.Msg.GetTemplateId())
		}
		return connect.NewResponse(&sandboxv1.GetTemplateResponse{
			Template: &sandboxv1.Template{Id: "tpl-001", WorkspaceId: "ws-001", OwnerType: "workspace", OwnerId: "ws-001", Name: "alpha"},
		}), nil
	}
	h.listTemplatesFn = func(req *connect.Request[sandboxv1.ListTemplatesRequest]) (*connect.Response[sandboxv1.ListTemplatesResponse], error) {
		if req.Msg.GetWorkspaceId() != "" {
			t.Fatalf("unexpected workspace_id: %q", req.Msg.GetWorkspaceId())
		}
		return connect.NewResponse(&sandboxv1.ListTemplatesResponse{
			Templates: []*sandboxv1.Template{
				{Id: "tpl-001", WorkspaceId: "ws-001", OwnerType: "workspace", OwnerId: "ws-001", Name: "alpha"},
				{Id: "tpl-002", WorkspaceId: "ws-001", OwnerType: "workspace", OwnerId: "ws-001", Name: "beta"},
			},
		}), nil
	}
	h.updateTemplateFn = func(req *connect.Request[sandboxv1.UpdateTemplateRequest]) (*connect.Response[sandboxv1.UpdateTemplateResponse], error) {
		if req.Msg.GetTemplateId() != "tpl-001" {
			t.Fatalf("unexpected template_id: %q", req.Msg.GetTemplateId())
		}
		if req.Msg.GetSetupScript() != "uv sync --frozen" {
			t.Fatalf("unexpected setup_script: %q", req.Msg.GetSetupScript())
		}
		if req.Msg.GetEnvVars()["FOO"] != "baz" {
			t.Fatalf("unexpected env_vars: %#v", req.Msg.GetEnvVars())
		}
		return connect.NewResponse(&sandboxv1.UpdateTemplateResponse{
			Template: &sandboxv1.Template{Id: "tpl-001", WorkspaceId: "ws-001", OwnerType: "workspace", OwnerId: "ws-001", Name: "alpha", SetupScript: "uv sync --frozen"},
		}), nil
	}
	h.deleteTemplateFn = func(req *connect.Request[sandboxv1.DeleteTemplateRequest]) (*connect.Response[sandboxv1.DeleteTemplateResponse], error) {
		if req.Msg.GetTemplateId() != "tpl-001" {
			t.Fatalf("unexpected template_id: %q", req.Msg.GetTemplateId())
		}
		return connect.NewResponse(&sandboxv1.DeleteTemplateResponse{
			Template: &sandboxv1.Template{Id: "tpl-001", WorkspaceId: "ws-001", OwnerType: "workspace", OwnerId: "ws-001", Name: "alpha"},
		}), nil
	}
	h.buildTemplateFn = func(req *connect.Request[sandboxv1.BuildTemplateRequest]) (*connect.Response[sandboxv1.BuildTemplateResponse], error) {
		if req.Msg.GetTemplateId() != "tpl-001" {
			t.Fatalf("unexpected template_id: %q", req.Msg.GetTemplateId())
		}
		if !reflect.DeepEqual(req.Msg.GetBuildSecrets(), map[string]string{"GIT_TOKEN": "token", "NPM_TOKEN": "npm"}) {
			t.Fatalf("unexpected build_secrets: %#v", req.Msg.GetBuildSecrets())
		}
		return connect.NewResponse(&sandboxv1.BuildTemplateResponse{
			Build: &sandboxv1.TemplateBuild{
				Id:         "build-001",
				TemplateId: "tpl-001",
				State:      sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_PENDING,
				Version:    1,
				StartedAt:  timestamppb.Now(),
			},
		}), nil
	}
	h.getTemplateBuildFn = func(req *connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
		if req.Msg.GetBuildId() != "build-001" {
			t.Fatalf("unexpected build_id: %q", req.Msg.GetBuildId())
		}
		return connect.NewResponse(&sandboxv1.GetTemplateBuildResponse{
			Build: &sandboxv1.TemplateBuild{
				Id:         "build-001",
				TemplateId: "tpl-001",
				State:      sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_BUILDING,
				Version:    1,
				StartedAt:  timestamppb.Now(),
			},
		}), nil
	}

	client := newTemplateTestClient(t, h)

	template, err := client.GetTemplate(context.Background(), "tpl-001")
	if err != nil || template.Name != "alpha" {
		t.Fatalf("GetTemplate: template=%#v err=%v", template, err)
	}

	templates, err := client.ListTemplates(context.Background())
	if err != nil || len(templates) != 2 {
		t.Fatalf("ListTemplates: templates=%d err=%v", len(templates), err)
	}

	template, err = client.UpdateTemplate(context.Background(), "tpl-001",
		WithSetupScript("uv sync --frozen"),
		WithEnvs(map[string]string{"FOO": "baz"}),
	)
	if err != nil || template.SetupScript != "uv sync --frozen" {
		t.Fatalf("UpdateTemplate: template=%#v err=%v", template, err)
	}

	secrets := map[string]string{"GIT_TOKEN": "token", "NPM_TOKEN": "npm"}
	secretsOption := WithBuildSecrets(secrets)
	secrets["GIT_TOKEN"] = "mutated"
	build, err := client.BuildTemplate(context.Background(), "tpl-001", secretsOption)
	if err != nil || build.State != TemplateBuildStatePending {
		t.Fatalf("BuildTemplate: build=%#v err=%v", build, err)
	}

	build, err = client.GetTemplateBuild(context.Background(), "build-001")
	if err != nil || build.State != TemplateBuildStateBuilding {
		t.Fatalf("GetTemplateBuild: build=%#v err=%v", build, err)
	}

	template, err = client.DeleteTemplate(context.Background(), "tpl-001")
	if err != nil || template.ID != "tpl-001" {
		t.Fatalf("DeleteTemplate: template=%#v err=%v", template, err)
	}
}

func TestTemplateBuildFromProtoPreservesBuildSecretKeys(t *testing.T) {
	t.Parallel()

	protoBuild := &sandboxv1.TemplateBuild{Provenance: &sandboxv1.TemplateBuildProvenance{
		BuildSecretKeys: []string{"GIT_TOKEN", "NPM_TOKEN"},
	}}
	build := templateBuildFromProto(protoBuild)
	protoBuild.Provenance.BuildSecretKeys[0] = "mutated"

	if build == nil || build.Provenance == nil || !reflect.DeepEqual(build.Provenance.BuildSecretKeys, []string{"GIT_TOKEN", "NPM_TOKEN"}) {
		t.Fatalf("unexpected provenance: %#v", build)
	}
}

func TestWaitForTemplateBuild(t *testing.T) {
	t.Parallel()

	orig := templateBuildPollInterval
	templateBuildPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { templateBuildPollInterval = orig })

	state := sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_BUILDING
	h := &templateHandler{}
	h.getTemplateBuildFn = func(_ *connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
		resp := &sandboxv1.GetTemplateBuildResponse{
			Build: &sandboxv1.TemplateBuild{
				Id:         "build-001",
				TemplateId: "tpl-001",
				State:      state,
				Version:    1,
				StartedAt:  timestamppb.Now(),
			},
		}
		state = sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_READY
		return connect.NewResponse(resp), nil
	}

	client := newTemplateTestClient(t, h)
	build, err := client.WaitForTemplateBuild(context.Background(), "build-001")
	if err != nil {
		t.Fatalf("WaitForTemplateBuild: %v", err)
	}
	if build.State != TemplateBuildStateReady {
		t.Fatalf("unexpected state: %q", build.State)
	}
}

func TestWaitForTemplateBuildFailed(t *testing.T) {
	t.Parallel()

	h := &templateHandler{}
	h.getTemplateBuildFn = func(_ *connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
		return connect.NewResponse(&sandboxv1.GetTemplateBuildResponse{
			Build: &sandboxv1.TemplateBuild{
				Id:           "build-001",
				TemplateId:   "tpl-001",
				State:        sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_FAILED,
				Version:      1,
				Error:        stringPtr("pip install failed"),
				BuildLogTail: stringPtr("last lines"),
				StartedAt:    timestamppb.Now(),
			},
		}), nil
	}

	client := newTemplateTestClient(t, h)
	build, err := client.WaitForTemplateBuild(context.Background(), "build-001")
	if !errors.Is(err, ErrTemplateBuildFailed) {
		t.Fatalf("expected ErrTemplateBuildFailed, got %v", err)
	}
	if build == nil || build.Error != "pip install failed" {
		t.Fatalf("unexpected build: %#v", build)
	}
}

func TestWaitForTemplateBuildWithEventsPreservesOrderAndDeduplicates(t *testing.T) {
	orig := templateBuildPollInterval
	templateBuildPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { templateBuildPollInterval = orig })

	progress := &sandboxv1.TemplateBuildEvent{Event: &sandboxv1.TemplateBuildEvent_Progress{Progress: &sandboxv1.TemplateBuildProgressEvent{
		Timestamp: timestamppb.Now(), Phase: "build", State: sandboxv1.TemplateBuildProgressState_TEMPLATE_BUILD_PROGRESS_STATE_STARTED,
	}}}
	logEvent := &sandboxv1.TemplateBuildEvent{Event: &sandboxv1.TemplateBuildEvent_Log{Log: &sandboxv1.TemplateBuildLogEvent{
		Timestamp: timestamppb.Now(), Phase: "build", Stream: sandboxv1.TemplateBuildLogStream_TEMPLATE_BUILD_LOG_STREAM_STDOUT, Data: "ok\n",
	}}}
	calls := 0
	h := &templateHandler{}
	h.getTemplateBuildFn = func(_ *connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
		calls++
		state := sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_BUILDING
		events := []*sandboxv1.TemplateBuildEvent{progress}
		if calls > 1 {
			state = sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_READY
			events = append(events, logEvent)
		}
		return connect.NewResponse(&sandboxv1.GetTemplateBuildResponse{Build: &sandboxv1.TemplateBuild{
			Id: "build-001", TemplateId: "tpl-001", State: state, Version: 1, Events: events,
		}}), nil
	}

	var observed []string
	client := newTemplateTestClient(t, h)
	_, err := client.WaitForTemplateBuildWithEvents(context.Background(), "build-001", func(event TemplateBuildEvent) error {
		if event.Progress != nil {
			observed = append(observed, event.Progress.State)
		} else {
			observed = append(observed, event.Log.Data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WaitForTemplateBuildWithEvents: %v", err)
	}
	if !reflect.DeepEqual(observed, []string{"started", "ok\n"}) {
		t.Fatalf("unexpected events: %#v", observed)
	}
}

func TestCreateWithImageOption(t *testing.T) {
	t.Parallel()

	h := &templateHandler{}
	h.createSessionFn = func(req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
		if req.Msg.GetRegistryRef() != "pub/template:prod" {
			t.Fatalf("unexpected registry_ref: %q", req.Msg.GetRegistryRef())
		}
		if req.Msg.GetSnapshotId() != "" {
			t.Fatalf("expected snapshot_id to be cleared, got %q", req.Msg.GetSnapshotId())
		}
		if req.Msg.CpuCores != nil {
			t.Fatalf("expected cpu_cores to be omitted for template inheritance, got %v", req.Msg.CpuCores)
		}
		if req.Msg.MemoryMb != nil {
			t.Fatalf("expected memory_mb to be omitted for template inheritance, got %v", req.Msg.MemoryMb)
		}
		return connect.NewResponse(&sandboxv1.CreateSessionResponse{
			Session: &sandboxv1.SandboxSession{
				Id:        "session-1",
				State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
				OwnerType: defaultCreateOwnerType,
				OwnerId:   defaultCreateOwnerID,
			},
		}), nil
	}

	client := newTemplateTestClient(t, h)
	session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"), WithImage("pub/template:prod"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if session.ID != "session-1" {
		t.Fatalf("unexpected session id: %q", session.ID)
	}
}

func TestCreateFromTemplateSpec(t *testing.T) {
	t.Parallel()

	templateID := "8d995c1c-8e24-4b3a-a8ab-a00316357385"
	h := &templateHandler{}
	h.createSessionFn = func(req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
		if req.Msg.GetTemplateSpecId() != templateID {
			t.Fatalf("unexpected template_spec_id: %q", req.Msg.GetTemplateSpecId())
		}
		if !reflect.DeepEqual(req.Msg.GetSetupEnv(), map[string]string{"MODE": "dev"}) {
			t.Fatalf("unexpected setup_env: %#v", req.Msg.GetSetupEnv())
		}
		if !reflect.DeepEqual(req.Msg.GetSetupSecrets(), map[string]string{"GH_TOKEN": "secret"}) {
			t.Fatalf("unexpected setup_secrets: %#v", req.Msg.GetSetupSecrets())
		}
		if req.Msg.CpuCores != nil || req.Msg.MemoryMb != nil || req.Msg.DiskSizeGb != nil {
			t.Fatal("expected default resources to be omitted for template spec")
		}
		return connect.NewResponse(&sandboxv1.CreateSessionResponse{Session: &sandboxv1.SandboxSession{
			Id: "session-template-spec", State: sandboxv1.SessionState_SESSION_STATE_RUNNING,
			OwnerType: defaultCreateOwnerType, OwnerId: defaultCreateOwnerID,
		}}), nil
	}

	client := newTemplateTestClient(t, h)
	_, err := client.Create(context.Background(),
		WithWorkspaceID("ws-1"),
		FromTemplateSpec(&Template{ID: templateID}),
		WithSetupEnvs(map[string]string{"MODE": "dev"}),
		WithSetupSecrets(map[string]string{"GH_TOKEN": "secret"}),
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestCreateRejectsConflictingTemplateSpecSource(t *testing.T) {
	t.Parallel()

	client := newTemplateTestClient(t, &templateHandler{})
	_, err := client.Create(context.Background(),
		WithWorkspaceID("ws-1"),
		WithImage("acme/app:latest"),
		FromTemplateSpec("8d995c1c-8e24-4b3a-a8ab-a00316357385"),
	)
	if err == nil || !strings.Contains(err.Error(), "only one of image, snapshot, or template spec") {
		t.Fatalf("expected source conflict, got %v", err)
	}
}

func TestTemplateSpecCreateUsesLongDefaultWaitTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultCreateConfig(nil)
	FromTemplateSpec("8d995c1c-8e24-4b3a-a8ab-a00316357385").applyCreate(&cfg)
	if got := effectiveCreateWaitTimeout(cfg); got != DefaultTemplateSpecCreateTimeout {
		t.Fatalf("effectiveCreateWaitTimeout() = %s, want %s", got, DefaultTemplateSpecCreateTimeout)
	}
	WithWaitTimeout(time.Minute).applyCreate(&cfg)
	if got := effectiveCreateWaitTimeout(cfg); got != time.Minute {
		t.Fatalf("explicit effectiveCreateWaitTimeout() = %s, want 1m", got)
	}
}

func TestCreateWithSnapshotOmitsDefaultResourceOverrides(t *testing.T) {
	t.Parallel()

	h := &templateHandler{}
	h.createSessionFn = func(req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
		if req.Msg.GetSnapshotId() != "snap-001" {
			t.Fatalf("unexpected snapshot_id: %q", req.Msg.GetSnapshotId())
		}
		if req.Msg.CpuCores != nil {
			t.Fatalf("expected cpu_cores to be omitted for snapshot restore, got %v", req.Msg.CpuCores)
		}
		if req.Msg.MemoryMb != nil {
			t.Fatalf("expected memory_mb to be omitted for snapshot restore, got %v", req.Msg.MemoryMb)
		}
		if req.Msg.DiskSizeGb != nil {
			t.Fatalf("expected disk_size_gb to be omitted for snapshot restore, got %v", req.Msg.DiskSizeGb)
		}
		return connect.NewResponse(&sandboxv1.CreateSessionResponse{
			Session: &sandboxv1.SandboxSession{
				Id:        "session-snapshot",
				State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
				OwnerType: defaultCreateOwnerType,
				OwnerId:   defaultCreateOwnerID,
			},
		}), nil
	}

	client := newTemplateTestClient(t, h)
	session, err := client.Create(context.Background(), WithWorkspaceID("ws-1"), WithSnapshot("snap-001"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if session.ID != "session-snapshot" {
		t.Fatalf("unexpected session id: %q", session.ID)
	}
}

func TestCreateWithImageExplicitResources(t *testing.T) {
	t.Parallel()

	h := &templateHandler{}
	h.createSessionFn = func(req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
		if req.Msg.CpuCores == nil || *req.Msg.CpuCores != 4 {
			t.Fatalf("expected cpu_cores override 4, got %v", req.Msg.CpuCores)
		}
		if req.Msg.MemoryMb == nil || *req.Msg.MemoryMb != 8192 {
			t.Fatalf("expected memory_mb override 8192, got %v", req.Msg.MemoryMb)
		}
		return connect.NewResponse(&sandboxv1.CreateSessionResponse{
			Session: &sandboxv1.SandboxSession{
				Id:        "session-2",
				State:     sandboxv1.SessionState_SESSION_STATE_RUNNING,
				OwnerType: defaultCreateOwnerType,
				OwnerId:   defaultCreateOwnerID,
			},
		}), nil
	}

	client := newTemplateTestClient(t, h)
	session, err := client.Create(
		context.Background(),
		WithWorkspaceID("ws-1"),
		WithImage("pub/template:prod"),
		WithCPUCores(4),
		WithMemoryMB(8192),
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if session.ID != "session-2" {
		t.Fatalf("unexpected session id: %q", session.ID)
	}
}

func TestRegistryLookupsSendWorkspaceID(t *testing.T) {
	t.Parallel()

	workspaceID := "01900000-0000-7000-8000-0000000000aa"
	h := &templateHandler{}
	h.getRegistryImageFn = func(req *connect.Request[sandboxv1.GetRegistryImageRequest]) (*connect.Response[sandboxv1.GetRegistryImageResponse], error) {
		if req.Msg.GetRef() != "base:prod" || req.Msg.GetWorkspaceId() != workspaceID {
			t.Fatalf("unexpected get request: %v", req.Msg)
		}
		return connect.NewResponse(&sandboxv1.GetRegistryImageResponse{Detail: &sandboxv1.RegistryImageDetail{
			Image: &sandboxv1.RegistryImage{
				Id:            "01900000-0000-7000-8000-0000000000bb",
				WorkspaceId:   workspaceID,
				Name:          "base",
				Kind:          sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_TEMPLATE,
				Visibility:    sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_PRIVATE,
				WorkspaceSlug: "acme",
			},
		}}), nil
	}
	h.resolveRegistryRefFn = func(req *connect.Request[sandboxv1.ResolveRegistryRefRequest]) (*connect.Response[sandboxv1.ResolveRegistryRefResponse], error) {
		if req.Msg.GetRef() != "base:prod" || req.Msg.GetWorkspaceId() != workspaceID {
			t.Fatalf("unexpected resolve request: %v", req.Msg)
		}
		return connect.NewResponse(&sandboxv1.ResolveRegistryRefResponse{Resolved: &sandboxv1.ResolvedRegistryRef{
			ImageId:             "01900000-0000-7000-8000-0000000000bb",
			OwningWorkspaceId:   workspaceID,
			OwningWorkspaceSlug: "acme",
			ImageName:           "base",
			Kind:                sandboxv1.RegistryImageKind_REGISTRY_IMAGE_KIND_TEMPLATE,
			Visibility:          sandboxv1.RegistryVisibility_REGISTRY_VISIBILITY_PRIVATE,
		}}), nil
	}

	client := newTemplateTestClient(t, h)
	option := WithRegistryLookupWorkspaceID(workspaceID)
	if _, err := client.GetRegistryImage(context.Background(), "base:prod", option); err != nil {
		t.Fatalf("GetRegistryImage: %v", err)
	}
	if _, err := client.ResolveRegistryRef(context.Background(), "base:prod", option); err != nil {
		t.Fatalf("ResolveRegistryRef: %v", err)
	}
}

func stringPtr(s string) *string {
	return &s
}
