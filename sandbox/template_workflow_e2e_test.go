//go:build sdk_e2e

package sandbox_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	sandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
)

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func runPSQL(t *testing.T, sql string, variables map[string]string) string {
	t.Helper()
	dsn := envOr(
		"TENKI_SANDBOX_E2E_DATABASE_URL",
		"postgresql://postgres@127.0.0.1:6444/tenki",
	)
	args := []string{dsn, "-Atq", "-v", "ON_ERROR_STOP=1"}
	for key, value := range variables {
		args = append(args, "-v", key+"="+value)
	}
	cmd := exec.Command("psql", args...)
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("psql: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func withIncompatibleCPU(t *testing.T, snapshotID string, fn func()) {
	t.Helper()
	original := strings.SplitN(runPSQL(t, `
		SELECT coalesce(cpu_profile, '') || '|' || coalesce(origin_cpu_signature, '')
		FROM sandbox_snapshots
		WHERE id = :'snapshot_id'::uuid;
	`, map[string]string{"snapshot_id": snapshotID}), "|", 2)
	if len(original) != 2 {
		t.Fatal("snapshot CPU metadata was not found")
	}
	runPSQL(t, `
		UPDATE sandbox_snapshots
		SET cpu_profile = 'legacy',
			origin_cpu_signature = 'sdk-e2e-forced-mismatch'
		WHERE id = :'snapshot_id'::uuid;
	`, map[string]string{"snapshot_id": snapshotID})
	defer runPSQL(t, `
		UPDATE sandbox_snapshots
		SET cpu_profile = nullif(:'cpu_profile', ''),
			origin_cpu_signature = nullif(:'origin_cpu_signature', '')
		WHERE id = :'snapshot_id'::uuid;
	`, map[string]string{
		"snapshot_id":          snapshotID,
		"cpu_profile":          original[0],
		"origin_cpu_signature": original[1],
	})
	fn()
}

func templateWorkflowSpec(label string, memory bool) sandbox.TemplateSpec {
	runAt := sandbox.RunAtBoot
	if memory {
		runAt = sandbox.RunAtBuild
	}
	spec := sandbox.NewTemplateSpec().
		WithGitContext(sandbox.GitContext{
			Repo: envOr(
				"TENKI_SANDBOX_E2E_GIT_REPO",
				"https://github.com/octocat/Hello-World.git",
			),
			Ref: envOr("TENKI_SANDBOX_E2E_GIT_REF", "master"),
		}).
		BuildEnv(map[string]string{"SDK_E2E_BUILD": label}).
		Run("test -f README", sandbox.RunStepOptions{Name: "verify git checkout"}).
		Run(`test "$SDK_E2E_BUILD" = "`+label+`"`, sandbox.RunStepOptions{Name: "verify build env"}).
		WriteFile("/home/tenki/app/sdk-e2e.txt", label+"\n").
		RuntimeEnv(map[string]string{"SDK_E2E_RUNTIME": label + "-template"}).
		StartArgs(
			[]string{
				"python3",
				"-m",
				"http.server",
				"3000",
				"--directory",
				"/home/tenki/app",
			},
			sandbox.StartOptions{RunAt: runAt},
		).
		ReadyWhen(sandbox.ReadyWhen{
			Timeout: 60 * time.Second,
			Checks:  []sandbox.ReadyCheck{sandbox.ReadyPort(3000)},
		}).
		Resources(sandbox.TemplateResources{
			CPUCores:   2,
			MemoryMB:   2048,
			DiskSizeGB: 10,
		})
	if memory {
		spec = spec.SnapshotMode(sandbox.SnapshotModeMemory)
	}
	return spec
}

func verifyTemplateSession(
	t *testing.T,
	ctx context.Context,
	client *sandbox.Client,
	build *sandbox.TemplateBuild,
	workspaceID string,
	label string,
	expectedMode string,
) *sandbox.Session {
	t.Helper()
	session, err := client.Create(
		ctx,
		sandbox.WithWorkspaceID(workspaceID),
		sandbox.WithImage(build.Image),
		sandbox.WithEnvs(map[string]string{
			"SDK_E2E_RUNTIME": label + "-create",
		}),
		sandbox.WithWaitForRuntime(true),
		sandbox.WithWaitTimeout(5*time.Minute),
	)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.RuntimeState != sandbox.RuntimeStateReady {
		t.Fatalf("runtime state = %s, want READY", session.RuntimeState)
	}
	marker, err := session.Exec(
		ctx,
		"cat",
		sandbox.WithArgs("/home/tenki/app/sdk-e2e.txt"),
	)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if got := strings.TrimSpace(marker.StdoutString()); got != label {
		t.Fatalf("marker = %q, want %q", got, label)
	}
	runtimeEnv, err := session.Exec(
		ctx,
		"printenv",
		sandbox.WithArgs("SDK_E2E_RUNTIME"),
	)
	if err != nil {
		t.Fatalf("read runtime env: %v", err)
	}
	if got := strings.TrimSpace(runtimeEnv.StdoutString()); got != label+"-create" {
		t.Fatalf("runtime env = %q, want %q", got, label+"-create")
	}
	if err := session.Refresh(ctx); err != nil {
		t.Fatalf("refresh session: %v", err)
	}
	if expectedMode != "" && session.Metadata["restore_mode"] != expectedMode {
		t.Fatalf("restore metadata = %#v, want mode %q", session.Metadata, expectedMode)
	}
	return session
}

func TestTemplateWorkflowFilesystemMemoryAndForcedFallback(t *testing.T) {
	if os.Getenv("SANDBOX_E2E") != "1" {
		t.Skip("set SANDBOX_E2E=1 to run sandbox SDK E2E tests")
	}
	workspaceID := requiredEnv(t, "TENKI_SANDBOX_WORKSPACE_ID")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	client, err := sandbox.New(sandbox.WithCookieName(envOr("TENKI_COOKIE_NAME", "tenki_session")))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	var templates []*sandbox.Template
	var sessions []*sandbox.Session
	var imageIDs []string
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		for i := len(sessions) - 1; i >= 0; i-- {
			_ = sessions[i].CloseIfOpen(cleanupCtx)
		}
		for i := len(templates) - 1; i >= 0; i-- {
			_, _ = client.DeleteTemplate(cleanupCtx, templates[i])
		}
		for i := len(imageIDs) - 1; i >= 0; i-- {
			_, _ = client.DeleteRegistryImage(cleanupCtx, imageIDs[i], "SDK E2E cleanup")
		}
	})

	runID := fmt.Sprint(time.Now().UnixNano())
	for _, mode := range []string{"filesystem", "memory"} {
		memory := mode == "memory"
		label := fmt.Sprintf("go-%s-%s", mode, runID)
		spec := templateWorkflowSpec(label, memory)
		if err := spec.Validate(); err != nil {
			t.Fatalf("validate %s spec: %v", mode, err)
		}
		template, err := client.CreateTemplate(
			ctx,
			sandbox.WithWorkspaceID(workspaceID),
			sandbox.WithTemplateName(label),
			sandbox.WithTemplateSpec(spec),
			sandbox.WithTags("sdk-e2e", "go"),
		)
		if err != nil {
			t.Fatalf("create %s template: %v", mode, err)
		}
		templates = append(templates, template)
		build, err := client.BuildTemplate(
			ctx,
			template,
			sandbox.WithWaitForCompletion(true),
		)
		if err != nil {
			t.Fatalf("build %s template: %v", mode, err)
		}
		if build.State != sandbox.TemplateBuildStateReady ||
			build.Image == nil ||
			build.SnapshotID == "" ||
			build.ImageDigestRef == "" {
			t.Fatalf("incomplete %s build: %#v", mode, build)
		}
		if !strings.HasPrefix(build.ImageDigest, "sha256:") || len(build.ImageDigest) != len("sha256:")+64 {
			t.Fatalf("image digest = %q, want SHA-256 manifest digest", build.ImageDigest)
		}
		if build.ImageDigestRef != build.Image.DigestRef ||
			build.ImageDigestRef != build.Image.Ref()+"@"+build.ImageDigest ||
			strings.Contains(build.ImageDigestRef, build.SnapshotID) {
			t.Fatalf("image identity = %#v, build digest ref = %q", build.Image, build.ImageDigestRef)
		}
		imageIDs = append(imageIDs, build.Image.ID)

		expectedMode := ""
		if memory {
			expectedMode = "memory"
		}
		session := verifyTemplateSession(
			t,
			ctx,
			client,
			build,
			workspaceID,
			label,
			expectedMode,
		)
		sessions = append(sessions, session)
		if memory && session.Metadata["restore_degraded"] != "false" {
			t.Fatalf("memory restore metadata = %#v", session.Metadata)
		}
		if err := session.CloseIfOpen(ctx); err != nil {
			t.Fatalf("close %s session: %v", mode, err)
		}

		if memory {
			withIncompatibleCPU(t, build.SnapshotID, func() {
				cold := verifyTemplateSession(
					t,
					ctx,
					client,
					build,
					workspaceID,
					label,
					"cold_boot",
				)
				sessions = append(sessions, cold)
				if cold.Metadata["restore_degraded"] != "true" ||
					cold.Metadata["restore_reason"] != "incompatible_cpu" {
					t.Fatalf("cold fallback metadata = %#v", cold.Metadata)
				}
				if err := cold.CloseIfOpen(ctx); err != nil {
					t.Fatalf("close cold fallback session: %v", err)
				}
			})
		}
	}
}
