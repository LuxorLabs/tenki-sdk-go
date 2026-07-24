package examples_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"connectrpc.com/connect"
	tenkisandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

const (
	exampleTemplateID = "01900000-0000-7000-8000-000000000001"
	exampleBuildID    = "01900000-0000-7000-8000-000000000002"
	exampleDigest     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	exampleDigestRef  = "acme/node-api@" + exampleDigest
)

type exampleTemplateHandler struct {
	exampleSandboxHandler

	snapshotMode sandboxv1.TemplateSnapshotMode
	runtimeState sandboxv1.TemplateRuntimeState
	metadata     map[string]string
}

func (h *exampleTemplateHandler) CreateTemplate(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateTemplateRequest],
) (*connect.Response[sandboxv1.CreateTemplateResponse], error) {
	if req.Msg.BuilderSpec != nil && req.Msg.BuilderSpec.Runtime != nil {
		h.snapshotMode = req.Msg.BuilderSpec.Runtime.SnapshotMode
	}
	return connect.NewResponse(&sandboxv1.CreateTemplateResponse{Template: &sandboxv1.Template{
		Id:          exampleTemplateID,
		Name:        req.Msg.Name,
		BuilderSpec: req.Msg.BuilderSpec,
	}}), nil
}

func (h *exampleTemplateHandler) BuildTemplate(
	_ context.Context,
	req *connect.Request[sandboxv1.BuildTemplateRequest],
) (*connect.Response[sandboxv1.BuildTemplateResponse], error) {
	return connect.NewResponse(&sandboxv1.BuildTemplateResponse{Build: &sandboxv1.TemplateBuild{
		Id:         exampleBuildID,
		TemplateId: req.Msg.TemplateId,
		State:      sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_BUILDING,
	}}), nil
}

func (h *exampleTemplateHandler) GetTemplateBuild(
	_ context.Context,
	req *connect.Request[sandboxv1.GetTemplateBuildRequest],
) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
	digest := exampleDigest
	digestRef := exampleDigestRef
	return connect.NewResponse(&sandboxv1.GetTemplateBuildResponse{Build: &sandboxv1.TemplateBuild{
		Id:         req.Msg.BuildId,
		TemplateId: exampleTemplateID,
		State:      sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_READY,
		Events: []*sandboxv1.TemplateBuildEvent{
			{Event: &sandboxv1.TemplateBuildEvent_Progress{Progress: &sandboxv1.TemplateBuildProgressEvent{
				Phase: "steps", State: sandboxv1.TemplateBuildProgressState_TEMPLATE_BUILD_PROGRESS_STATE_COMPLETED,
			}}},
		},
		Image: &sandboxv1.RegistryImage{
			Id: "01900000-0000-7000-8000-000000000003", Name: "node-api", WorkspaceSlug: "acme",
			Digest: &digest, DigestRef: &digestRef,
		},
		ImageDigest:    &digest,
		ImageDigestRef: &digestRef,
	}}), nil
}

func (h *exampleTemplateHandler) CreateSession(
	_ context.Context,
	req *connect.Request[sandboxv1.CreateSessionRequest],
) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
	runtimeState := h.runtimeState
	if runtimeState == sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_UNSPECIFIED {
		runtimeState = sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_READY
	}
	return connect.NewResponse(&sandboxv1.CreateSessionResponse{Session: &sandboxv1.SandboxSession{
		Id:           "01900000-0000-7000-8000-000000000004",
		State:        sandboxv1.SessionState_SESSION_STATE_RUNNING,
		OwnerType:    "USER",
		OwnerId:      "owner-1",
		RuntimeState: runtimeState,
		Metadata:     h.metadata,
	}}), nil
}

func newExampleTemplateClient(handler *exampleTemplateHandler) (*tenkisandbox.Client, func()) {
	mux := http.NewServeMux()
	path, svc := sandboxv1connect.NewSandboxServiceHandler(handler)
	mux.Handle(path, svc)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	client, err := tenkisandbox.New(
		tenkisandbox.WithAuthToken("tk_test_api_key"),
		tenkisandbox.WithBaseURL(server.URL),
		tenkisandbox.WithHTTPClient(server.Client()),
	)
	if err != nil {
		panic(err)
	}
	return client, func() {
		_ = client.Close()
		server.Close()
	}
}

// Filesystem template workflow: typed recipe -> waited build -> launch from
// the private digest-addressed image.
func ExampleNewTemplateSpec_filesystem() {
	ctx := context.Background()
	client, cleanup := newExampleTemplateClient(&exampleTemplateHandler{})
	defer cleanup()

	spec := tenkisandbox.NewTemplateSpec().
		FromImage("sandbox-v2").
		WithGitContext(tenkisandbox.GitContext{
			Repo: "https://github.com/acme/node-api",
			Ref:  "main",
		}).
		Workdir("/home/tenki/app").
		Run("npm ci", tenkisandbox.RunStepOptions{Name: "Install dependencies"}).
		RuntimeEnv(map[string]string{"NODE_ENV": "production"}).
		StartArgs([]string{"npm", "start"}, tenkisandbox.StartOptions{
			RunAt:     tenkisandbox.RunAtBoot,
			ReadyWhen: []tenkisandbox.ReadyCheck{tenkisandbox.ReadyHTTP("http://localhost:3000/health")},
		})

	template, err := client.CreateTemplate(ctx,
		tenkisandbox.WithTemplateName("node-api"),
		tenkisandbox.WithTemplateSpec(spec),
	)
	if err != nil {
		panic(err)
	}

	build, err := client.BuildTemplate(ctx, template,
		tenkisandbox.WithBuildSecrets(map[string]string{"GITHUB_TOKEN": "gh-token"}),
		tenkisandbox.WithWaitForCompletion(true),
		tenkisandbox.WithBuildEventHandler(func(event tenkisandbox.TemplateBuildEvent) error {
			if event.Progress != nil {
				fmt.Println("progress:", event.Progress.Phase, event.Progress.State)
			}
			return nil
		}),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("image:", build.ImageDigestRef)

	session, err := client.Create(ctx,
		tenkisandbox.WithImage(build.Image),
		tenkisandbox.WithWaitReady(false),
		tenkisandbox.WithWaitForRuntime(true),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("session:", session.State)
	// Output:
	// progress: steps completed
	// image: acme/node-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
	// session: RUNNING
}

// Memory template workflow: a build-time runtime is captured as a
// memory-backed image and restored with process state.
func ExampleNewTemplateSpec_memory() {
	ctx := context.Background()
	handler := &exampleTemplateHandler{}
	client, cleanup := newExampleTemplateClient(handler)
	defer cleanup()

	spec := tenkisandbox.NewTemplateSpec().
		WithGitContext(tenkisandbox.GitContext{Repo: "https://github.com/acme/node-api", Ref: "main"}).
		Run("npm ci").
		StartArgs([]string{"npm", "run", "dev"}, tenkisandbox.StartOptions{RunAt: tenkisandbox.RunAtBuild}).
		SnapshotMode(tenkisandbox.SnapshotModeMemory).
		ReadyWhen(tenkisandbox.ReadyWhen{
			Timeout:      60 * time.Second,
			PollInterval: time.Second,
			Checks: []tenkisandbox.ReadyCheck{
				tenkisandbox.ReadyPort(3000),
				tenkisandbox.ReadyExec("test -f /home/tenki/app/.ready"),
			},
		})

	template, err := client.CreateTemplate(ctx,
		tenkisandbox.WithTemplateName("node-api-live"),
		tenkisandbox.WithTemplateSpec(spec),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("snapshot mode:", handler.snapshotMode)

	build, err := client.BuildTemplate(ctx, template, tenkisandbox.WithWaitForCompletion(true))
	if err != nil {
		panic(err)
	}

	// A successful memory restore exposes the captured running workload.
	session, err := client.Create(ctx,
		tenkisandbox.WithImage(build.Image),
		tenkisandbox.WithWaitReady(false),
		tenkisandbox.WithWaitForRuntime(true),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("runtime:", session.RuntimeState)
	// Output:
	// snapshot mode: TEMPLATE_SNAPSHOT_MODE_MEMORY
	// runtime: READY
}

// Cold-fallback workflow: memory restore unavailable -> cold-boot the image
// rootfs, start the declared runtime, and report degraded attribution.
func ExampleNewTemplateSpec_coldFallback() {
	ctx := context.Background()
	handler := &exampleTemplateHandler{
		runtimeState: sandboxv1.TemplateRuntimeState_TEMPLATE_RUNTIME_STATE_READY,
		metadata: map[string]string{
			"restore_mode":     "cold_boot",
			"restore_degraded": "true",
			"restore_reason":   "incompatible_cpu",
		},
	}
	client, cleanup := newExampleTemplateClient(handler)
	defer cleanup()

	build, err := client.GetTemplateBuild(ctx, exampleBuildID)
	if err != nil {
		panic(err)
	}

	session, err := client.Create(ctx,
		tenkisandbox.WithImage(build.Image),
		tenkisandbox.WithWaitReady(false),
		tenkisandbox.WithWaitForRuntime(true),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("runtime:", session.RuntimeState)
	fmt.Println(
		"restore:",
		session.Metadata["restore_mode"],
		session.Metadata["restore_degraded"],
		session.Metadata["restore_reason"],
	)
	// Output:
	// runtime: READY
	// restore: cold_boot true incompatible_cpu
}
