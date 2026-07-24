package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func mustSpecProto(t *testing.T, spec TemplateSpec) *sandboxv1.TemplateBuildSpec {
	t.Helper()
	data, err := spec.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	parsed, err := TemplateSpecFromJSON(data)
	if err != nil {
		t.Fatalf("TemplateSpecFromJSON: %v", err)
	}
	return parsed.toProto()
}

func specFromAuthoredJSON(t *testing.T, authored string) *sandboxv1.TemplateBuildSpec {
	t.Helper()
	spec, err := TemplateSpecFromJSON([]byte(authored))
	if err != nil {
		t.Fatalf("unmarshal authored JSON: %v", err)
	}
	return spec.toProto()
}

func TestTemplateSpecDefaultBase(t *testing.T) {
	got := mustSpecProto(t, NewTemplateSpec())
	if got.SpecVersion != "tenki.template.v1" {
		t.Fatalf("spec version = %q", got.SpecVersion)
	}
	if got.Base.GetImage() != "sandbox" {
		t.Fatalf("default base image = %q", got.Base.GetImage())
	}
}

// The canonical filesystem example from the template build spec. The builder
// must serialize equivalently to the shared cross-SDK contract.
func TestTemplateSpecFilesystemCanonicalExample(t *testing.T) {
	spec := NewTemplateSpec().
		Workdir("/home/tenki/app").
		WithGitContext(GitContext{
			Repo:         "https://github.com/acme/app",
			Ref:          "main",
			CheckoutDest: "/home/tenki/app",
			CheckoutMode: CheckoutModeContents,
			Ignore:       []string{".git/cache"},
		}).
		BuildEnv(map[string]string{"NODE_ENV": "production"}).
		Run("npm ci", RunStepOptions{Timeout: 1800 * time.Second}).
		Copy("ops/nginx.conf", "/etc/nginx/nginx.conf").
		WriteFile("/home/tenki/app/.template-built", "ready\n").
		RuntimeEnv(map[string]string{"PORT": "3000"}).
		Start("npm run dev", StartOptions{RunAt: RunAtBuild, Workdir: "/home/tenki/app"}).
		SnapshotMode(SnapshotModeFilesystem).
		ReadyWhen(ReadyWhen{
			Timeout:      60 * time.Second,
			PollInterval: time.Second,
			Checks:       []ReadyCheck{ReadyHTTP("http://127.0.0.1:3000/health", 200, 204)},
		}).
		StopGrace(30 * time.Second).
		Resources(TemplateResources{CPUCores: 2, MemoryMB: 4096, DiskSizeGB: 10})

	want := specFromAuthoredJSON(t, `{
		"specVersion": "tenki.template.v1",
		"base": {"image": "sandbox"},
		"workdir": "/home/tenki/app",
		"context": {
			"source": {"git": {"repo": "https://github.com/acme/app", "ref": "main"}},
			"checkout": {"dest": "/home/tenki/app", "mode": "contents"},
			"ignore": [".git/cache"]
		},
		"build": {"env": {"NODE_ENV": "production"}},
		"steps": [
			{"run": {"command": "npm ci", "timeoutSeconds": 1800}},
			{"copy": {"src": "ops/nginx.conf", "dest": "/etc/nginx/nginx.conf"}},
			{"writeFile": {"path": "/home/tenki/app/.template-built", "content": "cmVhZHkK"}}
		],
		"runtime": {
			"env": {"PORT": "3000"},
			"runAt": "build",
			"start": {"command": "npm run dev", "workdir": "/home/tenki/app"},
			"snapshotMode": "filesystem",
			"readyWhen": {
				"timeoutSeconds": 60,
				"pollIntervalSeconds": 1,
				"checks": [{"http": {"url": "http://127.0.0.1:3000/health", "successStatusCodes": [200, 204]}}]
			},
			"stopGraceSeconds": 30
		},
		"resources": {"cpuCores": 2, "memoryMb": 4096, "diskSizeGb": 10}
	}`)

	got := mustSpecProto(t, spec)
	if !proto.Equal(want, got) {
		t.Fatalf("spec mismatch:\nwant: %s\ngot:  %s", protojson.Format(want), protojson.Format(got))
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// The canonical memory template runtime example, including all three readiness
// check kinds and cold-fallback-relevant snapshot mode.
func TestTemplateSpecMemoryCanonicalExample(t *testing.T) {
	spec := NewTemplateSpec().
		Workdir("/home/tenki/app").
		StartArgs([]string{"npm", "run", "dev"}, StartOptions{RunAt: RunAtBuild, Workdir: "/home/tenki/app"}).
		SnapshotMode(SnapshotModeMemory).
		ReadyWhen(ReadyWhen{
			Timeout:      60 * time.Second,
			PollInterval: time.Second,
			Checks: []ReadyCheck{
				ReadyPort(3000),
				ReadyHTTP("http://127.0.0.1:3000/health"),
				ReadyExec("test -f /home/tenki/app/.ready", ExecCheckOptions{Timeout: 5 * time.Second}),
			},
		})

	want := specFromAuthoredJSON(t, `{
		"specVersion": "tenki.template.v1",
		"base": {"image": "sandbox"},
		"workdir": "/home/tenki/app",
		"runtime": {
			"runAt": "build",
			"start": {"argv": ["npm", "run", "dev"], "workdir": "/home/tenki/app"},
			"snapshotMode": "memory",
			"readyWhen": {
				"timeoutSeconds": 60,
				"pollIntervalSeconds": 1,
				"checks": [
					{"port": 3000},
					{"http": {"url": "http://127.0.0.1:3000/health"}},
					{"exec": {"command": "test -f /home/tenki/app/.ready", "timeoutSeconds": 5}}
				]
			}
		}
	}`)

	got := mustSpecProto(t, spec)
	if !proto.Equal(want, got) {
		t.Fatalf("spec mismatch:\nwant: %s\ngot:  %s", protojson.Format(want), protojson.Format(got))
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTemplateSpecProcessComposeRuntime(t *testing.T) {
	spec := NewTemplateSpec().
		RuntimeEnv(map[string]string{"APP_ENV": "development"}).
		ProcessCompose("process-compose.yaml", ProcessComposeOptions{
			Workdir:  "/home/tenki/app",
			EnvFiles: []string{".env.template"},
			RunAt:    RunAtBoot,
		}).
		StopGrace(30 * time.Second)

	got := mustSpecProto(t, spec)
	runtime := got.Runtime
	if runtime.GetProcessCompose().GetConfigPath() != "process-compose.yaml" {
		t.Fatalf("config path = %q", runtime.GetProcessCompose().GetConfigPath())
	}
	if runtime.GetProcessCompose().GetWorkdir() != "/home/tenki/app" {
		t.Fatalf("workdir = %q", runtime.GetProcessCompose().GetWorkdir())
	}
	if got := runtime.GetProcessCompose().GetEnvFiles(); len(got) != 1 || got[0] != ".env.template" {
		t.Fatalf("env files = %v", got)
	}
	if runtime.GetRunAt() != sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_BOOT {
		t.Fatalf("run at = %v", runtime.GetRunAt())
	}
	if runtime.GetEnv()["APP_ENV"] != "development" {
		t.Fatalf("runtime env = %v", runtime.GetEnv())
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTemplateSpecBaseSelectors(t *testing.T) {
	if got := mustSpecProto(t, NewTemplateSpec().FromImage("sandbox-v2")); got.Base.GetImage() != "sandbox-v2" {
		t.Fatalf("image base = %q", got.Base.GetImage())
	}
	templateID := "01900000-0000-7000-8000-000000000001"
	if got := mustSpecProto(t, NewTemplateSpec().FromTemplate(templateID)); got.Base.GetTemplateId() != templateID {
		t.Fatalf("template base = %q", got.Base.GetTemplateId())
	}
	snapshotID := "01900000-0000-7000-8000-000000000002"
	if got := mustSpecProto(t, NewTemplateSpec().FromSnapshot(snapshotID)); got.Base.GetSnapshotId() != snapshotID {
		t.Fatalf("snapshot base = %q", got.Base.GetSnapshotId())
	}
}

func TestTemplateSpecStepOperations(t *testing.T) {
	spec := NewTemplateSpec().
		WithGitContext(GitContext{Repo: "https://github.com/acme/app", Ref: "main"}).
		Run("make", RunStepOptions{Name: "Build", Workdir: "/home/tenki/app"}).
		RunArgs([]string{"go", "test", "./..."}).
		Copy("conf/app.conf", "/etc/app.conf", StepOptions{Name: "Install config"}).
		WriteFile("/etc/motd", "hi", WriteFileOptions{Mode: 0o644}).
		Mkdir("/var/app", MkdirOptions{Parents: true, Mode: 0o755}).
		Remove("/tmp/scratch", RemoveOptions{Recursive: true}).
		Rename("/tmp/a", "/tmp/b").
		Symlink("/usr/bin/python3", "/usr/local/bin/python").
		Apt("curl", "jq").
		Pip("requests").
		Npm("typescript").
		Bun("esbuild")

	got := mustSpecProto(t, spec)
	if len(got.Steps) != 12 {
		t.Fatalf("steps = %d", len(got.Steps))
	}
	if got.Steps[0].GetName() != "Build" || got.Steps[0].GetRun().GetWorkdir() != "/home/tenki/app" {
		t.Fatalf("run step = %v", got.Steps[0])
	}
	if argv := got.Steps[1].GetRun().GetArgv(); len(argv) != 3 || argv[0] != "go" {
		t.Fatalf("run argv step = %v", got.Steps[1])
	}
	if got.Steps[2].GetName() != "Install config" || got.Steps[2].GetCopy().GetSrc() != "conf/app.conf" {
		t.Fatalf("copy step = %v", got.Steps[2])
	}
	if got.Steps[3].GetWriteFile().GetMode() != 0o644 {
		t.Fatalf("write file mode = %v", got.Steps[3])
	}
	if !got.Steps[4].GetMkdir().GetParents() || got.Steps[4].GetMkdir().GetMode() != 0o755 {
		t.Fatalf("mkdir step = %v", got.Steps[4])
	}
	if !got.Steps[5].GetRemove().GetRecursive() {
		t.Fatalf("remove step = %v", got.Steps[5])
	}
	if got.Steps[6].GetRename().GetDest() != "/tmp/b" {
		t.Fatalf("rename step = %v", got.Steps[6])
	}
	if got.Steps[7].GetSymlink().GetTarget() != "/usr/bin/python3" {
		t.Fatalf("symlink step = %v", got.Steps[7])
	}
	if pkgs := got.Steps[8].GetApt().GetPackages(); len(pkgs) != 2 || pkgs[0] != "curl" {
		t.Fatalf("apt step = %v", got.Steps[8])
	}
	if got.Steps[9].GetPip() == nil || got.Steps[10].GetNpm() == nil || got.Steps[11].GetBun() == nil {
		t.Fatalf("package steps = %v", got.Steps[9:])
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// Fluent methods return new values; a shared base spec is never mutated.
func TestTemplateSpecImmutability(t *testing.T) {
	base := NewTemplateSpec().FromImage("sandbox-v2").BuildEnv(map[string]string{"A": "1"})
	left := base.Run("left").BuildEnv(map[string]string{"B": "2"})
	right := base.Run("right")

	baseProto := mustSpecProto(t, base)
	if len(baseProto.Steps) != 0 {
		t.Fatalf("base steps mutated: %v", baseProto.Steps)
	}
	if len(baseProto.Build.GetEnv()) != 1 {
		t.Fatalf("base env mutated: %v", baseProto.Build.GetEnv())
	}
	leftProto := mustSpecProto(t, left)
	rightProto := mustSpecProto(t, right)
	if leftProto.Steps[0].GetRun().GetCommand() != "left" || rightProto.Steps[0].GetRun().GetCommand() != "right" {
		t.Fatalf("branch steps: left=%v right=%v", leftProto.Steps, rightProto.Steps)
	}
}

// The zero value and hostile inputs must never panic; problems surface as
// typed validation errors instead.
func TestTemplateSpecNeverPanics(t *testing.T) {
	var zero TemplateSpec
	spec := zero.
		FromImage("").
		WithGitContext(GitContext{}).
		Workdir("relative/path").
		Run("").
		RunArgs(nil).
		Copy("", "").
		WriteFile("", "").
		Mkdir("").
		Remove("").
		Rename("", "").
		Symlink("", "").
		Apt().
		BuildEnv(nil).
		RuntimeEnv(nil).
		Start("").
		SnapshotMode(SnapshotMode("bogus")).
		ReadyWhen(ReadyWhen{}).
		Resources(TemplateResources{CPUCores: -1})

	err := spec.Validate()
	var validationErr *TemplateSpecValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected TemplateSpecValidationError, got %v", err)
	}
	if len(validationErr.Violations) < 10 {
		t.Fatalf("expected many violations, got %d: %v", len(validationErr.Violations), validationErr)
	}
	if _, jsonErr := spec.ToJSON(); jsonErr != nil {
		t.Fatalf("ToJSON on invalid spec: %v", jsonErr)
	}
}

func TestTemplateSpecValidateCollectsAllViolations(t *testing.T) {
	spec := NewTemplateSpec().
		Copy("../escape", "relative/dest").
		Start("npm start", StartOptions{RunAt: RunAtBuild}).
		SnapshotMode(SnapshotModeMemory)

	err := spec.Validate()
	var validationErr *TemplateSpecValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected TemplateSpecValidationError, got %v", err)
	}

	wantFields := []string{
		"steps[0].copy",      // copy requires a Git context
		"steps[0].copy.src",  // src traverses above the context
		"steps[0].copy.dest", // dest must be absolute
		"runtime.readyWhen",  // build runtime requires readiness
	}
	for _, field := range wantFields {
		found := false
		for _, violation := range validationErr.Violations {
			if violation.Field == field {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing violation for %q in %v", field, validationErr.Violations)
		}
	}
	if !strings.Contains(validationErr.Error(), "copy") {
		t.Fatalf("error text should mention violations: %v", validationErr)
	}
	if !errors.Is(err, ErrTemplateSpecInvalid) {
		t.Fatalf("expected ErrTemplateSpecInvalid sentinel, got %v", err)
	}
}

func TestTemplateSpecValidateMirrorsProtoConstraints(t *testing.T) {
	longPath := "/" + strings.Repeat("x", 4096)
	ignore := make([]string, 257)
	for index := range ignore {
		ignore[index] = "ignored"
	}
	ignore[0] = strings.Repeat("x", 4097)

	spec := NewTemplateSpec().
		FromTemplate("not-a-uuid").
		Workdir(longPath).
		WithGitContext(GitContext{
			Repo:         strings.Repeat("r", 4097),
			Ref:          strings.Repeat("r", 1025),
			CheckoutDest: longPath,
			CheckoutMode: CheckoutModeDirectory,
			CheckoutName: strings.Repeat("n", 256),
			Ignore:       ignore,
		}).
		Run(strings.Repeat("c", 65537), RunStepOptions{Name: strings.Repeat("n", 129)}).
		Copy(strings.Repeat("s", 4097), "/tmp/dest").
		Apt("curl", "curl", "--unsafe").
		ProcessCompose(strings.Repeat("p", 4097), ProcessComposeOptions{EnvFiles: []string{".env", ".env"}}).
		ReadyWhen(ReadyWhen{
			Timeout: time.Minute,
			Checks:  []ReadyCheck{ReadyHTTP("http://localhost/ready", 99, 200, 200)},
		})

	var validationErr *TemplateSpecValidationError
	if err := spec.Validate(); !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}

	wantFields := []string{
		"base.templateId",
		"workdir",
		"context.source.git.repo",
		"context.source.git.ref",
		"context.checkout.dest",
		"context.checkout.name",
		"context.ignore",
		"context.ignore[0]",
		"steps[0].name",
		"steps[0].run.command",
		"steps[1].copy.src",
		"steps[2].apt.packages",
		"steps[2].apt.packages[2]",
		"runtime.processCompose.configPath",
		"runtime.processCompose.envFiles",
		"runtime.readyWhen.checks[0].http.successStatusCodes",
		"runtime.readyWhen.checks[0].http.successStatusCodes[0]",
	}
	for _, field := range wantFields {
		found := false
		for _, violation := range validationErr.Violations {
			if violation.Field == field {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing violation for %q in %v", field, validationErr.Violations)
		}
	}
}

func TestTemplateSpecValidateSnapshotUUID(t *testing.T) {
	var validationErr *TemplateSpecValidationError
	if err := NewTemplateSpec().FromSnapshot("not-a-uuid").Validate(); !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if len(validationErr.Violations) != 1 || validationErr.Violations[0].Field != "base.snapshotId" {
		t.Fatalf("violations = %v", validationErr.Violations)
	}
}

func TestTemplateSpecSnapshotModeRequiresBuildRuntime(t *testing.T) {
	spec := NewTemplateSpec().
		Start("npm start", StartOptions{RunAt: RunAtBoot}).
		SnapshotMode(SnapshotModeMemory)

	var validationErr *TemplateSpecValidationError
	if err := spec.Validate(); !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	found := false
	for _, violation := range validationErr.Violations {
		if violation.Field == "runtime.snapshotMode" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected runtime.snapshotMode violation: %v", validationErr.Violations)
	}
}

func TestTemplateSpecRuntimeEntrypointRequired(t *testing.T) {
	spec := NewTemplateSpec().RuntimeEnv(map[string]string{"PORT": "3000"})
	var validationErr *TemplateSpecValidationError
	if err := spec.Validate(); !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestTemplateSpecJSONRoundTrip(t *testing.T) {
	spec := NewTemplateSpec().
		FromImage("sandbox-v2").
		WithGitContext(GitContext{Repo: "https://github.com/acme/app", Ref: "main"}).
		Run("npm ci").
		Start("npm start")

	data, err := spec.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	parsed, err := TemplateSpecFromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	roundTripped, err := parsed.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON roundtrip: %v", err)
	}
	if !proto.Equal(parsed.toProto(), spec.protoSpec()) {
		t.Fatalf("parsed proto mismatch:\n%s", data)
	}
	roundTripSpec, err := TemplateSpecFromJSON(roundTripped)
	if err != nil {
		t.Fatalf("FromJSON roundtrip: %v", err)
	}
	if !proto.Equal(roundTripSpec.toProto(), spec.protoSpec()) {
		t.Fatalf("roundtrip mismatch:\n%s\n%s", data, roundTripped)
	}
}

func TestTemplateSpecAuthoredJSONUsesShortEnumValues(t *testing.T) {
	spec := NewTemplateSpec().
		WithGitContext(GitContext{
			Repo:         "https://github.com/acme/app",
			Ref:          "main",
			CheckoutMode: CheckoutModeContents,
		}).
		BuildEnv(map[string]string{"KEEP": "TEMPLATE_SNAPSHOT_MODE_MEMORY"}).
		Start("npm start", StartOptions{
			RunAt:         RunAtBuild,
			RestartPolicy: RestartOnFailure,
		}).
		SnapshotMode(SnapshotModeMemory).
		ReadyWhen(ReadyWhen{
			Timeout:      60 * time.Second,
			PollInterval: 2 * time.Second,
			Checks: []ReadyCheck{
				ReadyPort(3000),
				ReadyHTTP("http://localhost:3000/health", 200, 204),
			},
		}).
		Resources(TemplateResources{CPUCores: 2, MemoryMB: 4096, DiskSizeGB: 10})

	data, err := spec.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var doc struct {
		Context struct {
			Checkout struct {
				Mode string `json:"mode"`
			} `json:"checkout"`
		} `json:"context"`
		Build struct {
			Env map[string]string `json:"env"`
		} `json:"build"`
		Runtime struct {
			RunAt         string `json:"runAt"`
			RestartPolicy string `json:"restartPolicy"`
			SnapshotMode  string `json:"snapshotMode"`
			ReadyWhen     struct {
				TimeoutSeconds      uint32 `json:"timeoutSeconds"`
				PollIntervalSeconds uint32 `json:"pollIntervalSeconds"`
				Checks              []struct {
					Port uint32 `json:"port"`
					HTTP struct {
						SuccessStatusCodes []uint32 `json:"successStatusCodes"`
					} `json:"http"`
				} `json:"checks"`
			} `json:"readyWhen"`
		} `json:"runtime"`
		Resources struct {
			CPUCores   int32 `json:"cpuCores"`
			MemoryMB   int32 `json:"memoryMb"`
			DiskSizeGB int32 `json:"diskSizeGb"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal authored JSON: %v", err)
	}
	if doc.Context.Checkout.Mode != "contents" || doc.Runtime.RunAt != "build" ||
		doc.Runtime.RestartPolicy != "on-failure" || doc.Runtime.SnapshotMode != "memory" {
		t.Fatalf("unexpected authored enum values: %+v", doc)
	}
	if doc.Build.Env["KEEP"] != "TEMPLATE_SNAPSHOT_MODE_MEMORY" {
		t.Fatalf("unrelated string was normalized: %+v", doc.Build.Env)
	}
	if doc.Runtime.ReadyWhen.TimeoutSeconds != 60 || doc.Runtime.ReadyWhen.PollIntervalSeconds != 2 ||
		len(doc.Runtime.ReadyWhen.Checks) != 2 || doc.Runtime.ReadyWhen.Checks[0].Port != 3000 ||
		!reflect.DeepEqual(doc.Runtime.ReadyWhen.Checks[1].HTTP.SuccessStatusCodes, []uint32{200, 204}) {
		t.Fatalf("unexpected authored numeric runtime values: %+v", doc.Runtime.ReadyWhen)
	}
	if doc.Resources.CPUCores != 2 || doc.Resources.MemoryMB != 4096 || doc.Resources.DiskSizeGB != 10 {
		t.Fatalf("unexpected authored numeric resource values: %+v", doc.Resources)
	}
}

func TestTemplateSpecFromJSONAcceptsShortAndProtobufEnumValues(t *testing.T) {
	for _, test := range []struct {
		name          string
		checkoutMode  string
		runAt         string
		restartPolicy string
		snapshotMode  string
		wantCheckout  sandboxv1.TemplateCheckoutMode
		wantRunAt     sandboxv1.TemplateRuntimeRunAt
		wantRestart   sandboxv1.TemplateRestartPolicy
		wantSnapshot  sandboxv1.TemplateSnapshotMode
	}{
		{
			name:          "short authored values",
			checkoutMode:  "contents",
			runAt:         "build",
			restartPolicy: "on-failure",
			snapshotMode:  "memory",
			wantCheckout:  sandboxv1.TemplateCheckoutMode_TEMPLATE_CHECKOUT_MODE_CONTENTS,
			wantRunAt:     sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_BUILD,
			wantRestart:   sandboxv1.TemplateRestartPolicy_TEMPLATE_RESTART_POLICY_ON_FAILURE,
			wantSnapshot:  sandboxv1.TemplateSnapshotMode_TEMPLATE_SNAPSHOT_MODE_MEMORY,
		},
		{
			name:          "protobuf enum values",
			checkoutMode:  "TEMPLATE_CHECKOUT_MODE_DIRECTORY",
			runAt:         "TEMPLATE_RUNTIME_RUN_AT_MANUAL",
			restartPolicy: "TEMPLATE_RESTART_POLICY_ALWAYS",
			snapshotMode:  "TEMPLATE_SNAPSHOT_MODE_FILESYSTEM",
			wantCheckout:  sandboxv1.TemplateCheckoutMode_TEMPLATE_CHECKOUT_MODE_DIRECTORY,
			wantRunAt:     sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_MANUAL,
			wantRestart:   sandboxv1.TemplateRestartPolicy_TEMPLATE_RESTART_POLICY_ALWAYS,
			wantSnapshot:  sandboxv1.TemplateSnapshotMode_TEMPLATE_SNAPSHOT_MODE_FILESYSTEM,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(`{
				"specVersion": "tenki.template.v1",
				"base": {"image": "sandbox"},
				"context": {
					"source": {"git": {"repo": "https://github.com/acme/app", "ref": "main"}},
					"checkout": {"mode": %q}
				},
				"runtime": {
					"runAt": %q,
					"restartPolicy": %q,
					"snapshotMode": %q,
					"start": {"command": "npm start"}
				}
			}`, test.checkoutMode, test.runAt, test.restartPolicy, test.snapshotMode)
			spec, err := TemplateSpecFromJSON([]byte(data))
			if err != nil {
				t.Fatalf("TemplateSpecFromJSON: %v", err)
			}
			got := spec.toProto()
			if got.GetContext().GetCheckout().GetMode() != test.wantCheckout ||
				got.GetRuntime().GetRunAt() != test.wantRunAt ||
				got.GetRuntime().GetRestartPolicy() != test.wantRestart ||
				got.GetRuntime().GetSnapshotMode() != test.wantSnapshot {
				t.Fatalf("unexpected protobuf enums: %v %v %v %v", got.GetContext().GetCheckout().GetMode(), got.GetRuntime().GetRunAt(), got.GetRuntime().GetRestartPolicy(), got.GetRuntime().GetSnapshotMode())
			}
			authored, err := spec.ToJSON()
			if err != nil {
				t.Fatalf("ToJSON: %v", err)
			}
			if strings.Contains(string(authored), "TEMPLATE_") {
				t.Fatalf("ToJSON retained protobuf enum name: %s", authored)
			}
		})
	}
}

func TestTemplateSpecFromJSONRejectsUnknownShortEnumValue(t *testing.T) {
	_, err := TemplateSpecFromJSON([]byte(`{
		"specVersion": "tenki.template.v1",
		"base": {"image": "sandbox"},
		"context": {
			"source": {"git": {"repo": "https://github.com/acme/app", "ref": "main"}},
			"checkout": {"mode": "content"}
		}
	}`))
	if err == nil {
		t.Fatal("expected unknown enum rejection")
	}
}

func TestTemplateSpecFromJSONRejectsUnknownFields(t *testing.T) {
	_, err := TemplateSpecFromJSON([]byte(`{"specVersion": "tenki.template.v1", "base": {"image": "sandbox"}, "bogusField": true}`))
	if err == nil {
		t.Fatal("expected unknown-field rejection")
	}
}

func TestTemplateSpecFromJSONAcceptsCanonicalFieldNames(t *testing.T) {
	parsed, err := TemplateSpecFromJSON([]byte(`{
		"specVersion": "tenki.template.v1",
		"base": {"image": "sandbox"},
		"workdir": "/home/tenki/app",
		"steps": [{"run": {"command": "npm ci", "timeoutSeconds": 1800}}]
	}`))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	got := mustSpecProto(t, parsed)
	if got.Steps[0].GetRun().GetTimeoutSeconds() != 1800 {
		t.Fatalf("timeout = %d", got.Steps[0].GetRun().GetTimeoutSeconds())
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTemplateSpecStartOptionsReadyWhenDefaultsTimeout(t *testing.T) {
	spec := NewTemplateSpec().StartArgs([]string{"npm", "start"}, StartOptions{
		ReadyWhen: []ReadyCheck{ReadyHTTP("http://localhost:3000/health")},
	})
	got := mustSpecProto(t, spec)
	if got.Runtime.GetReadyWhen().GetTimeoutSeconds() != 60 {
		t.Fatalf("default ready timeout = %d", got.Runtime.GetReadyWhen().GetTimeoutSeconds())
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTemplateSpecReadyChecksValidation(t *testing.T) {
	spec := NewTemplateSpec().Start("npm start", StartOptions{RunAt: RunAtBuild}).ReadyWhen(ReadyWhen{
		Timeout: time.Minute,
		Checks: []ReadyCheck{
			ReadyPort(0),
			ReadyHTTP("http://example.com/health"),
			ReadyExec(""),
		},
	})
	var validationErr *TemplateSpecValidationError
	if err := spec.Validate(); !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if len(validationErr.Violations) < 3 {
		t.Fatalf("expected one violation per bad check: %v", validationErr.Violations)
	}
}

func TestTemplateSpecJSONUsesCanonicalNames(t *testing.T) {
	data, err := NewTemplateSpec().Run("npm ci").ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["specVersion"] != "tenki.template.v1" {
		t.Fatalf("specVersion = %v", doc["specVersion"])
	}
	if _, ok := doc["base"].(map[string]any); !ok {
		t.Fatalf("base = %v", doc["base"])
	}
}
