package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

func typedTestSpec() TemplateSpec {
	return NewTemplateSpec().
		FromImage("sandbox-v2").
		WithGitContext(GitContext{Repo: "https://github.com/acme/node-api", Ref: "main"}).
		Run("npm ci").
		RuntimeEnv(map[string]string{"NODE_ENV": "production"}).
		StartArgs([]string{"npm", "start"})
}

func TestCreateTemplateWithSpec(t *testing.T) {
	var gotReq *sandboxv1.CreateTemplateRequest
	handler := &templateHandler{
		createTemplateFn: func(req *connect.Request[sandboxv1.CreateTemplateRequest]) (*connect.Response[sandboxv1.CreateTemplateResponse], error) {
			gotReq = req.Msg
			return connect.NewResponse(&sandboxv1.CreateTemplateResponse{Template: &sandboxv1.Template{
				Id:          "01900000-0000-7000-8000-000000000001",
				Name:        "node-api",
				BuilderSpec: req.Msg.BuilderSpec,
				SpecHash:    strPtr("sha256:" + "ab12"[0:4] + "0000000000000000000000000000000000000000000000000000000000"),
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)

	template, err := client.CreateTemplate(context.Background(),
		WithWorkspaceID("01900000-0000-7000-8000-0000000000aa"),
		WithTemplateName("node-api"),
		WithTemplateSpec(typedTestSpec()),
	)
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if gotReq.BuilderSpec == nil {
		t.Fatal("builder_spec not sent")
	}
	if gotReq.BuilderSpec.Base.GetImage() != "sandbox-v2" {
		t.Fatalf("builder_spec base = %q", gotReq.BuilderSpec.Base.GetImage())
	}
	if gotReq.BaseImageId != "" || gotReq.SetupScript != "" || gotReq.StartCmd != nil {
		t.Fatalf("legacy fields must stay empty with builder_spec: %v", gotReq)
	}
	if template.Spec == nil {
		t.Fatal("template.Spec not mapped")
	}
	if template.SpecHash == "" {
		t.Fatal("template.SpecHash not mapped")
	}
	specJSON, err := template.Spec.ToJSON()
	if err != nil || len(specJSON) == 0 {
		t.Fatalf("template.Spec.ToJSON: %v", err)
	}
}

func strPtr(value string) *string { return &value }

func TestCreateTemplateWithSpecRejectsLegacyOptions(t *testing.T) {
	client := newTemplateTestClient(t, &templateHandler{})
	_, err := client.CreateTemplate(context.Background(),
		WithTemplateName("node-api"),
		WithTemplateSpec(typedTestSpec()),
		WithSetupScript("apt-get install -y jq"),
	)
	if !errors.Is(err, errTemplateSpecLegacyConflict) {
		t.Fatalf("expected legacy conflict error, got %v", err)
	}
}

func TestCreateTemplateWithInvalidSpecFailsBeforeRPC(t *testing.T) {
	rpcCalled := false
	handler := &templateHandler{
		createTemplateFn: func(*connect.Request[sandboxv1.CreateTemplateRequest]) (*connect.Response[sandboxv1.CreateTemplateResponse], error) {
			rpcCalled = true
			return nil, connect.NewError(connect.CodeInternal, errors.New("should not be called"))
		},
	}
	client := newTemplateTestClient(t, handler)

	invalid := NewTemplateSpec().Copy("../escape", "relative")
	_, err := client.CreateTemplate(context.Background(), WithTemplateName("x"), WithTemplateSpec(invalid))
	var validationErr *TemplateSpecValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected TemplateSpecValidationError, got %v", err)
	}
	if len(validationErr.Violations) < 2 {
		t.Fatalf("expected all violations, got %v", validationErr.Violations)
	}
	if rpcCalled {
		t.Fatal("RPC must not run for locally invalid specs")
	}
}

func TestCreateTemplateServerViolationsAreTyped(t *testing.T) {
	handler := &templateHandler{
		createTemplateFn: func(*connect.Request[sandboxv1.CreateTemplateRequest]) (*connect.Response[sandboxv1.CreateTemplateResponse], error) {
			rpcErr := connect.NewError(connect.CodeInvalidArgument, errors.New("validation failed"))
			detail, detailErr := connect.NewErrorDetail(&validate.Violations{Violations: []*validate.Violation{
				{
					Field: &validate.FieldPath{Elements: []*validate.FieldPathElement{
						{FieldName: strPtr("builder_spec")},
						{FieldName: strPtr("steps"), Subscript: &validate.FieldPathElement_Index{Index: 0}},
						{FieldName: strPtr("run")},
					}},
					RuleId:  strPtr("template_run_step.command_or_argv"),
					Message: strPtr("exactly one of command or argv is required"),
				},
				{
					Field:   &validate.FieldPath{Elements: []*validate.FieldPathElement{{FieldName: strPtr("workdir")}}},
					RuleId:  strPtr("string.pattern"),
					Message: strPtr("workdir must be absolute"),
				},
			}})
			if detailErr != nil {
				return nil, detailErr
			}
			rpcErr.AddDetail(detail)
			return nil, rpcErr
		},
	}
	client := newTemplateTestClient(t, handler)

	_, err := client.CreateTemplate(
		context.Background(),
		WithWorkspaceID("ws-1"),
		WithTemplateName("x"),
		WithTemplateSpec(typedTestSpec()),
	)
	var validationErr *TemplateSpecValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected TemplateSpecValidationError, got %v", err)
	}
	if len(validationErr.Violations) != 2 {
		t.Fatalf("expected both violations, got %v", validationErr.Violations)
	}
	if validationErr.Violations[0].Field != "builder_spec.steps[0].run" {
		t.Fatalf("field path = %q", validationErr.Violations[0].Field)
	}
	if validationErr.Violations[0].Rule != "template_run_step.command_or_argv" {
		t.Fatalf("rule = %q", validationErr.Violations[0].Rule)
	}
	if !errors.Is(err, ErrTemplateSpecInvalid) {
		t.Fatalf("expected ErrTemplateSpecInvalid sentinel, got %v", err)
	}
}

func TestUpdateTemplateWithSpecReplacesAtomically(t *testing.T) {
	var gotReq *sandboxv1.UpdateTemplateRequest
	handler := &templateHandler{
		updateTemplateFn: func(req *connect.Request[sandboxv1.UpdateTemplateRequest]) (*connect.Response[sandboxv1.UpdateTemplateResponse], error) {
			gotReq = req.Msg
			return connect.NewResponse(&sandboxv1.UpdateTemplateResponse{Template: &sandboxv1.Template{
				Id: req.Msg.TemplateId, BuilderSpec: req.Msg.BuilderSpec,
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)

	template := &Template{ID: "01900000-0000-7000-8000-000000000001"}
	updated, err := client.UpdateTemplate(context.Background(), template, WithTemplateSpec(typedTestSpec()))
	if err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}
	if gotReq.TemplateId != template.ID {
		t.Fatalf("template ID = %q", gotReq.TemplateId)
	}
	if gotReq.BuilderSpec == nil {
		t.Fatal("builder_spec not sent")
	}
	if updated.Spec == nil {
		t.Fatal("updated.Spec not mapped")
	}
}

func TestBuildTemplateAcceptsResourceObjectAndRawID(t *testing.T) {
	var gotIDs []string
	handler := &templateHandler{
		buildTemplateFn: func(req *connect.Request[sandboxv1.BuildTemplateRequest]) (*connect.Response[sandboxv1.BuildTemplateResponse], error) {
			gotIDs = append(gotIDs, req.Msg.TemplateId)
			return connect.NewResponse(&sandboxv1.BuildTemplateResponse{Build: &sandboxv1.TemplateBuild{
				Id: "01900000-0000-7000-8000-0000000000bb", TemplateId: req.Msg.TemplateId,
				State: sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_PENDING,
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)

	template := &Template{ID: "01900000-0000-7000-8000-000000000001"}
	if _, err := client.BuildTemplate(context.Background(), template); err != nil {
		t.Fatalf("BuildTemplate(resource): %v", err)
	}
	if _, err := client.BuildTemplate(context.Background(), template.ID); err != nil {
		t.Fatalf("BuildTemplate(raw ID): %v", err)
	}
	if len(gotIDs) != 2 || gotIDs[0] != template.ID || gotIDs[1] != template.ID {
		t.Fatalf("template IDs sent = %v", gotIDs)
	}

	if _, err := client.BuildTemplate(context.Background(), 42); err == nil {
		t.Fatal("expected typed error for unsupported reference type")
	}
	var nilTemplate *Template
	if _, err := client.BuildTemplate(context.Background(), nilTemplate); err == nil {
		t.Fatal("expected typed error for nil template")
	}
}

func TestBuildTemplateForwardsBuildEnvDefensively(t *testing.T) {
	var gotReq *sandboxv1.BuildTemplateRequest
	handler := &templateHandler{
		buildTemplateFn: func(req *connect.Request[sandboxv1.BuildTemplateRequest]) (*connect.Response[sandboxv1.BuildTemplateResponse], error) {
			gotReq = req.Msg
			return connect.NewResponse(&sandboxv1.BuildTemplateResponse{Build: &sandboxv1.TemplateBuild{
				Id: "01900000-0000-7000-8000-0000000000bb", TemplateId: req.Msg.TemplateId,
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)
	env := map[string]string{"NODE_ENV": "production"}
	option := WithBuildEnv(env)
	env["NODE_ENV"] = "mutated"

	_, err := client.BuildTemplate(context.Background(), "01900000-0000-7000-8000-000000000001", option)
	if err != nil {
		t.Fatalf("BuildTemplate: %v", err)
	}
	if gotReq == nil || gotReq.BuildEnv["NODE_ENV"] != "production" {
		t.Fatalf("build_env = %v", gotReq.GetBuildEnv())
	}
}

func TestCancelTemplateBuild(t *testing.T) {
	buildID := "01900000-0000-7000-8000-0000000000bb"
	var gotID string
	handler := &templateHandler{
		cancelTemplateBuildFn: func(req *connect.Request[sandboxv1.CancelTemplateBuildRequest]) (*connect.Response[sandboxv1.CancelTemplateBuildResponse], error) {
			gotID = req.Msg.BuildId
			return connect.NewResponse(&sandboxv1.CancelTemplateBuildResponse{Build: &sandboxv1.TemplateBuild{
				Id: req.Msg.BuildId, State: sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_FAILED,
				Error: strPtr("build canceled"),
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)

	build, err := client.CancelTemplateBuild(context.Background(), &TemplateBuild{ID: buildID})
	if err != nil {
		t.Fatalf("CancelTemplateBuild: %v", err)
	}
	if gotID != buildID {
		t.Fatalf("build ID = %q", gotID)
	}
	if !build.State.IsTerminal() {
		t.Fatalf("state = %v", build.State)
	}
}

func TestListActiveTemplateBuilds(t *testing.T) {
	templateID := "01900000-0000-7000-8000-000000000001"
	handler := &templateHandler{
		listActiveTemplateBuildsFn: func(req *connect.Request[sandboxv1.ListActiveTemplateBuildsRequest]) (*connect.Response[sandboxv1.ListActiveTemplateBuildsResponse], error) {
			if req.Msg.TemplateId != templateID {
				t.Fatalf("template ID = %q", req.Msg.TemplateId)
			}
			return connect.NewResponse(&sandboxv1.ListActiveTemplateBuildsResponse{Builds: []*sandboxv1.TemplateBuild{
				{Id: "01900000-0000-7000-8000-000000000003", TemplateId: templateID, Version: 3, State: sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_BUILDING},
				{Id: "01900000-0000-7000-8000-000000000002", TemplateId: templateID, Version: 2, State: sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_PENDING},
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)

	builds, err := client.ListActiveTemplateBuilds(context.Background(), &Template{ID: templateID})
	if err != nil {
		t.Fatalf("ListActiveTemplateBuilds: %v", err)
	}
	if len(builds) != 2 || builds[0].Version != 3 || builds[1].Version != 2 {
		t.Fatalf("builds = %#v", builds)
	}
}

func TestTemplateBuildExposesSpecHashAndImage(t *testing.T) {
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestRef := "acme/node-api@" + digest
	handler := &templateHandler{
		getTemplateBuildFn: func(req *connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
			return connect.NewResponse(&sandboxv1.GetTemplateBuildResponse{Build: &sandboxv1.TemplateBuild{
				Id:          req.Msg.BuildId,
				State:       sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_READY,
				BuilderSpec: typedTestSpec().toProto(),
				SpecHash:    strPtr(digest),
				Image: &sandboxv1.RegistryImage{
					Id: "01900000-0000-7000-8000-0000000000cc", Name: "node-api", WorkspaceSlug: "acme",
					Digest: strPtr(digest), DigestRef: strPtr(digestRef),
				},
				ImageDigest:    strPtr(digest),
				ImageDigestRef: strPtr(digestRef),
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)

	build, err := client.GetTemplateBuild(context.Background(), "01900000-0000-7000-8000-0000000000bb")
	if err != nil {
		t.Fatalf("GetTemplateBuild: %v", err)
	}
	if build.Spec == nil || build.SpecHash != digest {
		t.Fatalf("spec/hash = %v %q", build.Spec, build.SpecHash)
	}
	if build.ImageDigest != digest || build.ImageDigestRef != digestRef {
		t.Fatalf("digests = %q %q", build.ImageDigest, build.ImageDigestRef)
	}
	if build.Image == nil || build.Image.DigestRef != digestRef || build.Image.Ref() != "acme/node-api" {
		t.Fatalf("image = %+v", build.Image)
	}
}

func TestCreateWithImageFromBuildUsesDigestRef(t *testing.T) {
	digestRef := "acme/node-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var gotRef string
	var gotWorkspaceID string
	var gotProjectID string
	handler := &templateHandler{
		createSessionFn: func(req *connect.Request[sandboxv1.CreateSessionRequest]) (*connect.Response[sandboxv1.CreateSessionResponse], error) {
			gotRef = req.Msg.GetRegistryRef()
			gotWorkspaceID = req.Msg.GetWorkspaceId()
			gotProjectID = req.Msg.GetProjectId()
			return connect.NewResponse(&sandboxv1.CreateSessionResponse{Session: &sandboxv1.SandboxSession{
				Id: "01900000-0000-7000-8000-0000000000dd", State: sandboxv1.SessionState_SESSION_STATE_RUNNING,
			}}), nil
		},
	}
	client := newTemplateTestClient(t, handler)

	image := &RegistryImage{Name: "node-api", WorkspaceSlug: "acme", DigestRef: digestRef}
	if _, err := client.Create(context.Background(), WithWorkspaceID("ws-001"), WithImage(image), WithWaitReady(false)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotRef != digestRef {
		t.Fatalf("registry ref = %q", gotRef)
	}
	if gotWorkspaceID != "ws-001" || gotProjectID != "" {
		t.Fatalf("scope = workspace %q project %q", gotWorkspaceID, gotProjectID)
	}

	if _, err := client.Create(context.Background(), WithWorkspaceID("ws-001"), WithImage(&RegistryImage{Name: "node-api", WorkspaceSlug: "acme"}), WithWaitReady(false)); err != nil {
		t.Fatalf("Create fallback: %v", err)
	}
	if gotRef != "acme/node-api" {
		t.Fatalf("fallback registry ref = %q", gotRef)
	}
}

// Context cancellation must stop local observation without any remote cancel RPC.
func TestWaitForTemplateBuildContextCancelStopsLocallyOnly(t *testing.T) {
	cancelCalled := false
	handler := &templateHandler{
		getTemplateBuildFn: func(req *connect.Request[sandboxv1.GetTemplateBuildRequest]) (*connect.Response[sandboxv1.GetTemplateBuildResponse], error) {
			return connect.NewResponse(&sandboxv1.GetTemplateBuildResponse{Build: &sandboxv1.TemplateBuild{
				Id: req.Msg.BuildId, State: sandboxv1.TemplateBuildState_TEMPLATE_BUILD_STATE_BUILDING,
			}}), nil
		},
		cancelTemplateBuildFn: func(*connect.Request[sandboxv1.CancelTemplateBuildRequest]) (*connect.Response[sandboxv1.CancelTemplateBuildResponse], error) {
			cancelCalled = true
			return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected"))
		},
	}
	client := newTemplateTestClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.WaitForTemplateBuild(ctx, "01900000-0000-7000-8000-0000000000bb")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
	if cancelCalled {
		t.Fatal("local cancellation must not cancel the remote build")
	}
}
