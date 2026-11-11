package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
)

func TestTemplateSpecAcceptsBothSpecVersions(t *testing.T) {
	t.Parallel()

	for _, version := range []string{TemplateSpecVersion, TemplateSpecVersionLuxor} {
		spec, err := TemplateSpecFromJSON([]byte(`{"specVersion":"` + version + `","base":{"image":"sandbox"}}`))
		if err != nil {
			t.Fatalf("import %s: %v", version, err)
		}
		if err := spec.Validate(); err != nil {
			t.Fatalf("validate %s: %v", version, err)
		}
	}
	if got := NewTemplateSpec().protoSpec().SpecVersion; got != TemplateSpecVersion {
		t.Fatalf("builder emits %q, want %q", got, TemplateSpecVersion)
	}
}

func TestTemplateSpecRejectsUnknownSpecVersion(t *testing.T) {
	t.Parallel()

	spec, err := TemplateSpecFromJSON([]byte(`{"specVersion":"acme.template.v9","base":{"image":"sandbox"}}`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var validationErr *TemplateSpecValidationError
	if !errors.As(spec.Validate(), &validationErr) {
		t.Fatalf("expected TemplateSpecValidationError")
	}
	if len(validationErr.Violations) != 1 || validationErr.Violations[0].Field != "specVersion" ||
		validationErr.Violations[0].Rule != "in" {
		t.Fatalf("violations = %#v", validationErr.Violations)
	}
}

type templateWarningHandler struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
}

var deprecatedSpecVersionWarning = []*sandboxv1.TemplateWarning{{
	Code:    sandboxv1.TemplateWarningCode_TEMPLATE_WARNING_CODE_SPEC_VERSION_DEPRECATED,
	Message: "specVersion is deprecated",
}}

func (templateWarningHandler) CreateTemplate(
	context.Context, *connect.Request[sandboxv1.CreateTemplateRequest],
) (*connect.Response[sandboxv1.CreateTemplateResponse], error) {
	return connect.NewResponse(&sandboxv1.CreateTemplateResponse{
		Template: &sandboxv1.Template{Id: "template-1"}, Warnings: deprecatedSpecVersionWarning,
	}), nil
}

func (templateWarningHandler) UpdateTemplate(
	context.Context, *connect.Request[sandboxv1.UpdateTemplateRequest],
) (*connect.Response[sandboxv1.UpdateTemplateResponse], error) {
	return connect.NewResponse(&sandboxv1.UpdateTemplateResponse{
		Template: &sandboxv1.Template{Id: "template-1"}, Warnings: deprecatedSpecVersionWarning,
	}), nil
}

func (templateWarningHandler) BuildTemplate(
	context.Context, *connect.Request[sandboxv1.BuildTemplateRequest],
) (*connect.Response[sandboxv1.BuildTemplateResponse], error) {
	return connect.NewResponse(&sandboxv1.BuildTemplateResponse{
		Build: &sandboxv1.TemplateBuild{Id: "build-1", TemplateId: "template-1"}, Warnings: deprecatedSpecVersionWarning,
	}), nil
}

func TestTemplateCallsRetainAndEmitWarnings(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.Handle(sandboxv1connect.NewSandboxServiceHandler(templateWarningHandler{}))
	server := httptest.NewUnstartedServer(mux)
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetHTTP1(true)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	t.Cleanup(server.Close)
	var emitted []SandboxWarning
	client, err := New(WithAuthToken("tk_test"), WithBaseURL(server.URL), WithWarningHandler(func(warning SandboxWarning) {
		emitted = append(emitted, warning)
	}))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	spec := NewTemplateSpec()

	created, err := client.CreateTemplate(ctx, WithTemplateName("app"), WithTemplateSpec(spec))
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	updated, err := client.UpdateTemplate(ctx, created, WithTemplateSpec(spec))
	if err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}
	build, err := client.BuildTemplate(ctx, created)
	if err != nil {
		t.Fatalf("BuildTemplate: %v", err)
	}

	for name, warnings := range map[string][]SandboxWarning{
		"create": created.Warnings, "update": updated.Warnings, "build": build.Warnings,
	} {
		if len(warnings) != 1 || warnings[0].Code != SandboxWarningCodeSpecVersionDeprecated {
			t.Fatalf("%s warnings = %#v", name, warnings)
		}
	}
	if len(emitted) != 3 {
		t.Fatalf("emitted %d warnings, want 3", len(emitted))
	}
}
