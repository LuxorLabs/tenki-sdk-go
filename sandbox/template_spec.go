package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// TemplateSpecVersion is the canonical template build spec version emitted by this SDK.
const TemplateSpecVersion = "tenki.template.v1"

const defaultReadyWhenTimeout = 60 * time.Second

const maxTemplatePathLength = 4096

var templateUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ErrTemplateSpecInvalid is the sentinel wrapped by TemplateSpecValidationError.
var ErrTemplateSpecInvalid = errors.New("sandbox: template spec invalid")

// TemplateSpecViolation is one field-addressable template spec violation.
// Field uses canonical protobuf JSON names (for example "runtime.readyWhen").
type TemplateSpecViolation struct {
	Field   string
	Rule    string
	Message string
}

// TemplateSpecValidationError carries every violation found in a spec, from
// local Validate calls or from server-side submission failures.
type TemplateSpecValidationError struct {
	Violations []TemplateSpecViolation
}

func (e *TemplateSpecValidationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return ErrTemplateSpecInvalid.Error()
	}
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		if violation.Field != "" {
			parts = append(parts, violation.Field+": "+violation.Message)
			continue
		}
		parts = append(parts, violation.Message)
	}
	return ErrTemplateSpecInvalid.Error() + ": " + strings.Join(parts, "; ")
}

func (e *TemplateSpecValidationError) Unwrap() error { return ErrTemplateSpecInvalid }

// CheckoutMode selects how Git repository contents land in the guest.
type CheckoutMode string

const (
	CheckoutModeContents  CheckoutMode = "contents"
	CheckoutModeDirectory CheckoutMode = "directory"
)

// GitContext is the backend-fetched Git build context. Copy step sources are
// relative to this checkout; no local upload or archive transport exists.
type GitContext struct {
	Repo string
	Ref  string
	// CheckoutDest is the optional absolute checkout destination (defaults to the spec workdir).
	CheckoutDest string
	CheckoutMode CheckoutMode
	// CheckoutName names the child directory in directory mode.
	CheckoutName string
	Ignore       []string
}

// RunAt selects when the declared runtime starts.
type RunAt string

const (
	RunAtBoot   RunAt = "boot"
	RunAtBuild  RunAt = "build"
	RunAtManual RunAt = "manual"
)

// TemplateRestartPolicy selects boot/manual runtime restart behavior.
type TemplateRestartPolicy string

const (
	RestartNever     TemplateRestartPolicy = "never"
	RestartOnFailure TemplateRestartPolicy = "on-failure"
	RestartAlways    TemplateRestartPolicy = "always"
)

// SnapshotMode selects what a build-time runtime snapshot captures.
type SnapshotMode string

const (
	SnapshotModeFilesystem SnapshotMode = "filesystem"
	SnapshotModeMemory     SnapshotMode = "memory"
)

// RunStepOptions configures one run step.
type RunStepOptions struct {
	Name    string
	Workdir string
	Timeout time.Duration
}

// StepOptions configures steps that only support a display name.
type StepOptions struct {
	Name string
}

// WriteFileOptions configures one write-file step.
type WriteFileOptions struct {
	Name string
	Mode fs.FileMode
}

// MkdirOptions configures one mkdir step.
type MkdirOptions struct {
	Name    string
	Parents bool
	Mode    fs.FileMode
}

// RemoveOptions configures one remove step.
type RemoveOptions struct {
	Name      string
	Recursive bool
}

// StartOptions configures a single-command runtime entrypoint.
type StartOptions struct {
	Workdir       string
	RunAt         RunAt
	RestartPolicy TemplateRestartPolicy
	// ReadyWhen sets readiness checks with a default 60s timeout; use the
	// ReadyWhen builder method for full control.
	ReadyWhen []ReadyCheck
}

// ProcessComposeOptions configures a process-compose runtime entrypoint.
type ProcessComposeOptions struct {
	Workdir       string
	EnvFiles      []string
	RunAt         RunAt
	RestartPolicy TemplateRestartPolicy
}

// ExecCheckOptions configures one exec readiness check.
type ExecCheckOptions struct {
	Timeout time.Duration
}

// ReadyWhen is the runtime readiness contract shared by build, boot, manual
// start, and cold-fallback recovery.
type ReadyWhen struct {
	// Timeout bounds readiness polling; defaults to 60s when checks are set.
	Timeout time.Duration
	// PollInterval defaults server-side to 1s when zero.
	PollInterval time.Duration
	Checks       []ReadyCheck
}

// ReadyCheck is one readiness probe; build with ReadyPort, ReadyHTTP,
// ReadyExec, or ReadyExecArgs.
type ReadyCheck struct {
	port int
	http *sandboxv1.TemplateHTTPReadyCheck
	exec *sandboxv1.TemplateExecReadyCheck
}

// ReadyPort waits for a guest TCP port to accept connections.
func ReadyPort(port int) ReadyCheck {
	return ReadyCheck{port: port}
}

// ReadyHTTP waits for a localhost HTTP endpoint to return a success status
// (2xx by default).
func ReadyHTTP(url string, successStatusCodes ...int) ReadyCheck {
	check := &sandboxv1.TemplateHTTPReadyCheck{Url: url}
	for _, code := range successStatusCodes {
		check.SuccessStatusCodes = append(check.SuccessStatusCodes, uint32(code))
	}
	return ReadyCheck{http: check}
}

// ReadyExec waits for a shell command to exit zero.
func ReadyExec(command string, opts ...ExecCheckOptions) ReadyCheck {
	check := &sandboxv1.TemplateExecReadyCheck{Command: command}
	if len(opts) > 0 && opts[0].Timeout > 0 {
		check.TimeoutSeconds = uint32(opts[0].Timeout / time.Second)
	}
	return ReadyCheck{exec: check}
}

// ReadyExecArgs waits for an argv-form command to exit zero.
func ReadyExecArgs(argv []string, opts ...ExecCheckOptions) ReadyCheck {
	check := &sandboxv1.TemplateExecReadyCheck{Argv: append([]string(nil), argv...)}
	if len(opts) > 0 && opts[0].Timeout > 0 {
		check.TimeoutSeconds = uint32(opts[0].Timeout / time.Second)
	}
	return ReadyCheck{exec: check}
}

// TemplateSpec is an immutable typed template build recipe: every fluent method
// returns a new value, methods never panic, and Validate reports all violations.
type TemplateSpec struct {
	spec *sandboxv1.TemplateBuildSpec
}

// NewTemplateSpec returns a spec builder on the default Tenki base image "sandbox".
func NewTemplateSpec() TemplateSpec {
	return TemplateSpec{spec: defaultTemplateSpecProto()}
}

func defaultTemplateSpecProto() *sandboxv1.TemplateBuildSpec {
	return &sandboxv1.TemplateBuildSpec{
		SpecVersion: TemplateSpecVersion,
		Base:        &sandboxv1.TemplateBase{Source: &sandboxv1.TemplateBase_Image{Image: "sandbox"}},
	}
}

func (s TemplateSpec) clone() *sandboxv1.TemplateBuildSpec {
	if s.spec == nil {
		return defaultTemplateSpecProto()
	}
	return proto.Clone(s.spec).(*sandboxv1.TemplateBuildSpec)
}

func (s TemplateSpec) with(mutate func(*sandboxv1.TemplateBuildSpec)) TemplateSpec {
	next := s.clone()
	mutate(next)
	return TemplateSpec{spec: next}
}

// FromImage bases the template on a Tenki-managed image ID or registry image ref.
func (s TemplateSpec) FromImage(image string) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		spec.Base = &sandboxv1.TemplateBase{Source: &sandboxv1.TemplateBase_Image{Image: strings.TrimSpace(image)}}
	})
}

// FromTemplate bases the template on the current publication of a parent template.
func (s TemplateSpec) FromTemplate(templateID string) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		spec.Base = &sandboxv1.TemplateBase{Source: &sandboxv1.TemplateBase_TemplateId{TemplateId: strings.TrimSpace(templateID)}}
	})
}

// FromSnapshot bases the template on a parent snapshot.
func (s TemplateSpec) FromSnapshot(snapshotID string) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		spec.Base = &sandboxv1.TemplateBase{Source: &sandboxv1.TemplateBase_SnapshotId{SnapshotId: strings.TrimSpace(snapshotID)}}
	})
}

// WithGitContext sets the backend-fetched Git build context.
func (s TemplateSpec) WithGitContext(gitContext GitContext) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		context := &sandboxv1.TemplateContext{
			Source: &sandboxv1.TemplateContextSource{Source: &sandboxv1.TemplateContextSource_Git{
				Git: &sandboxv1.TemplateGitContext{Repo: strings.TrimSpace(gitContext.Repo), Ref: strings.TrimSpace(gitContext.Ref)},
			}},
			Ignore: append([]string(nil), gitContext.Ignore...),
		}
		if gitContext.CheckoutDest != "" || gitContext.CheckoutMode != "" || gitContext.CheckoutName != "" {
			checkout := &sandboxv1.TemplateCheckout{Dest: gitContext.CheckoutDest, Mode: checkoutModeToProto(gitContext.CheckoutMode)}
			if name := strings.TrimSpace(gitContext.CheckoutName); name != "" {
				checkout.Name = &name
			}
			context.Checkout = checkout
		}
		spec.Context = context
	})
}

// Workdir sets the default directory for checkout, build steps, and runtime.
func (s TemplateSpec) Workdir(dir string) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		spec.Workdir = strings.TrimSpace(dir)
	})
}

// BuildEnv merges persisted build-time environment variables.
func (s TemplateSpec) BuildEnv(env map[string]string) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		if len(env) == 0 {
			return
		}
		if spec.Build == nil {
			spec.Build = &sandboxv1.TemplateBuildConfig{}
		}
		if spec.Build.Env == nil {
			spec.Build.Env = map[string]string{}
		}
		for key, value := range env {
			spec.Build.Env[key] = value
		}
	})
}

// RuntimeEnv merges persisted runtime environment variables. A runtime
// entrypoint (Start, StartArgs, or ProcessCompose) is still required.
func (s TemplateSpec) RuntimeEnv(env map[string]string) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		if len(env) == 0 {
			return
		}
		runtime := ensureRuntime(spec)
		if runtime.Env == nil {
			runtime.Env = map[string]string{}
		}
		for key, value := range env {
			runtime.Env[key] = value
		}
	})
}

func (s TemplateSpec) appendStep(name string, step *sandboxv1.TemplateStep) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			step.Name = &trimmed
		}
		spec.Steps = append(spec.Steps, step)
	})
}

func firstOption[T any](opts []T) T {
	var zero T
	if len(opts) > 0 {
		return opts[0]
	}
	return zero
}

// Run appends a shell-form command step (executed with sh -lc).
func (s TemplateSpec) Run(command string, opts ...RunStepOptions) TemplateSpec {
	options := firstOption(opts)
	run := &sandboxv1.TemplateRunStep{Command: command, Workdir: options.Workdir}
	if options.Timeout > 0 {
		run.TimeoutSeconds = uint32(options.Timeout / time.Second)
	}
	return s.appendStep(options.Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Run{Run: run}})
}

// RunArgs appends an argv-form command step (no shell).
func (s TemplateSpec) RunArgs(argv []string, opts ...RunStepOptions) TemplateSpec {
	options := firstOption(opts)
	run := &sandboxv1.TemplateRunStep{Argv: append([]string(nil), argv...), Workdir: options.Workdir}
	if options.Timeout > 0 {
		run.TimeoutSeconds = uint32(options.Timeout / time.Second)
	}
	return s.appendStep(options.Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Run{Run: run}})
}

// Copy appends a copy step from the materialized Git context (src is
// context-relative) to an absolute guest path.
func (s TemplateSpec) Copy(src, dest string, opts ...StepOptions) TemplateSpec {
	return s.appendStep(firstOption(opts).Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Copy{
		Copy: &sandboxv1.TemplateCopyStep{Src: src, Dest: dest},
	}})
}

// WriteFile appends an inline small-file write step.
func (s TemplateSpec) WriteFile(path, content string, opts ...WriteFileOptions) TemplateSpec {
	options := firstOption(opts)
	step := &sandboxv1.TemplateWriteFileStep{Path: path, Content: []byte(content)}
	if options.Mode != 0 {
		mode := uint32(options.Mode.Perm())
		step.Mode = &mode
	}
	return s.appendStep(options.Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_WriteFile{WriteFile: step}})
}

// Mkdir appends a directory creation step.
func (s TemplateSpec) Mkdir(path string, opts ...MkdirOptions) TemplateSpec {
	options := firstOption(opts)
	step := &sandboxv1.TemplateMakeDirStep{Path: path, Parents: options.Parents}
	if options.Mode != 0 {
		mode := uint32(options.Mode.Perm())
		step.Mode = &mode
	}
	return s.appendStep(options.Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Mkdir{Mkdir: step}})
}

// Remove appends a path removal step.
func (s TemplateSpec) Remove(path string, opts ...RemoveOptions) TemplateSpec {
	options := firstOption(opts)
	return s.appendStep(options.Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Remove{
		Remove: &sandboxv1.TemplateRemoveStep{Path: path, Recursive: options.Recursive},
	}})
}

// Rename appends a rename step between absolute guest paths.
func (s TemplateSpec) Rename(src, dest string, opts ...StepOptions) TemplateSpec {
	return s.appendStep(firstOption(opts).Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Rename{
		Rename: &sandboxv1.TemplateRenameStep{Src: src, Dest: dest},
	}})
}

// Symlink appends a symlink step: path becomes a link pointing at target.
func (s TemplateSpec) Symlink(target, path string, opts ...StepOptions) TemplateSpec {
	return s.appendStep(firstOption(opts).Name, &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Symlink{
		Symlink: &sandboxv1.TemplateSymlinkStep{Target: target, Path: path},
	}})
}

// Apt appends an apt package install step.
func (s TemplateSpec) Apt(packages ...string) TemplateSpec {
	return s.appendStep("", &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Apt{Apt: &sandboxv1.TemplatePackageStep{Packages: append([]string(nil), packages...)}}})
}

// Pip appends a pip package install step.
func (s TemplateSpec) Pip(packages ...string) TemplateSpec {
	return s.appendStep("", &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Pip{Pip: &sandboxv1.TemplatePackageStep{Packages: append([]string(nil), packages...)}}})
}

// Npm appends an npm package install step.
func (s TemplateSpec) Npm(packages ...string) TemplateSpec {
	return s.appendStep("", &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Npm{Npm: &sandboxv1.TemplatePackageStep{Packages: append([]string(nil), packages...)}}})
}

// Bun appends a bun package install step.
func (s TemplateSpec) Bun(packages ...string) TemplateSpec {
	return s.appendStep("", &sandboxv1.TemplateStep{Operation: &sandboxv1.TemplateStep_Bun{Bun: &sandboxv1.TemplatePackageStep{Packages: append([]string(nil), packages...)}}})
}

func ensureRuntime(spec *sandboxv1.TemplateBuildSpec) *sandboxv1.TemplateRuntime {
	if spec.Runtime == nil {
		spec.Runtime = &sandboxv1.TemplateRuntime{}
	}
	return spec.Runtime
}

func applyRuntimeOptions(runtime *sandboxv1.TemplateRuntime, runAt RunAt, restartPolicy TemplateRestartPolicy) {
	if runAt != "" {
		runtime.RunAt = runAtToProto(runAt)
	}
	if restartPolicy != "" {
		runtime.RestartPolicy = restartPolicyToProto(restartPolicy)
	}
}

// Start declares a shell-form runtime start command.
func (s TemplateSpec) Start(command string, opts ...StartOptions) TemplateSpec {
	options := firstOption(opts)
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		runtime := ensureRuntime(spec)
		runtime.Entrypoint = &sandboxv1.TemplateRuntime_Start{Start: &sandboxv1.TemplateStartRuntime{
			Command: command,
			Workdir: options.Workdir,
		}}
		applyRuntimeOptions(runtime, options.RunAt, options.RestartPolicy)
		if len(options.ReadyWhen) > 0 {
			runtime.ReadyWhen = readyWhenToProto(ReadyWhen{Checks: options.ReadyWhen})
		}
	})
}

// StartArgs declares an argv-form runtime start command.
func (s TemplateSpec) StartArgs(argv []string, opts ...StartOptions) TemplateSpec {
	options := firstOption(opts)
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		runtime := ensureRuntime(spec)
		runtime.Entrypoint = &sandboxv1.TemplateRuntime_Start{Start: &sandboxv1.TemplateStartRuntime{
			Argv:    append([]string(nil), argv...),
			Workdir: options.Workdir,
		}}
		applyRuntimeOptions(runtime, options.RunAt, options.RestartPolicy)
		if len(options.ReadyWhen) > 0 {
			runtime.ReadyWhen = readyWhenToProto(ReadyWhen{Checks: options.ReadyWhen})
		}
	})
}

// ProcessCompose declares a process-compose runtime supervisor. The config
// path and env files are workdir-relative; Tenki owns supervisor execution.
func (s TemplateSpec) ProcessCompose(configPath string, opts ...ProcessComposeOptions) TemplateSpec {
	options := firstOption(opts)
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		runtime := ensureRuntime(spec)
		runtime.Entrypoint = &sandboxv1.TemplateRuntime_ProcessCompose{ProcessCompose: &sandboxv1.TemplateProcessComposeRuntime{
			ConfigPath: configPath,
			Workdir:    options.Workdir,
			EnvFiles:   append([]string(nil), options.EnvFiles...),
		}}
		applyRuntimeOptions(runtime, options.RunAt, options.RestartPolicy)
	})
}

// RuntimeRunAt sets when the declared runtime starts (boot is the server default).
func (s TemplateSpec) RuntimeRunAt(runAt RunAt) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		ensureRuntime(spec).RunAt = runAtToProto(runAt)
	})
}

// RestartPolicy sets the boot/manual runtime restart policy.
func (s TemplateSpec) RestartPolicy(policy TemplateRestartPolicy) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		ensureRuntime(spec).RestartPolicy = restartPolicyToProto(policy)
	})
}

// SnapshotMode selects filesystem or memory capture for build-time runtime.
func (s TemplateSpec) SnapshotMode(mode SnapshotMode) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		ensureRuntime(spec).SnapshotMode = snapshotModeToProto(mode)
	})
}

// ReadyWhen sets the runtime readiness contract.
func (s TemplateSpec) ReadyWhen(ready ReadyWhen) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		ensureRuntime(spec).ReadyWhen = readyWhenToProto(ready)
	})
}

// StopGrace sets the graceful stop window used before filesystem snapshots.
func (s TemplateSpec) StopGrace(grace time.Duration) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		ensureRuntime(spec).StopGraceSeconds = uint32(grace / time.Second)
	})
}

// Resources sets CPU/memory/disk defaults for builds and launched sandboxes.
func (s TemplateSpec) Resources(resources TemplateResources) TemplateSpec {
	return s.with(func(spec *sandboxv1.TemplateBuildSpec) {
		spec.Resources = &sandboxv1.TemplateResources{
			CpuCores:   resources.CPUCores,
			MemoryMb:   resources.MemoryMB,
			DiskSizeGb: resources.DiskSizeGB,
		}
	})
}

// ToJSON emits authored JSON with protobuf field encoding and short enum values.
// Serialization succeeds even for invalid specs; use Validate for violations.
func (s TemplateSpec) ToJSON() ([]byte, error) {
	data, err := protojson.Marshal(s.protoSpec())
	if err != nil {
		return nil, fmt.Errorf("sandbox: marshal template spec: %w", err)
	}
	normalized, err := normalizeTemplateSpecJSON(data, true)
	if err != nil {
		return nil, fmt.Errorf("sandbox: normalize template spec JSON: %w", err)
	}
	return normalized, nil
}

// TemplateSpecFromJSON accepts short or protobuf enum names and rejects unknown fields.
// The parsed spec still goes through Validate on submission.
func TemplateSpecFromJSON(data []byte) (TemplateSpec, error) {
	normalized, err := normalizeTemplateSpecJSON(data, false)
	if err != nil {
		return TemplateSpec{}, templateSpecJSONError(err)
	}
	spec := &sandboxv1.TemplateBuildSpec{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(normalized, spec); err != nil {
		return TemplateSpec{}, templateSpecJSONError(err)
	}
	return TemplateSpec{spec: spec}, nil
}

func templateSpecJSONError(err error) error {
	return &TemplateSpecValidationError{Violations: []TemplateSpecViolation{{
		Rule:    "json",
		Message: err.Error(),
	}}}
}

var authoredTemplateEnumFields = map[string]map[string]string{
	"context.checkout.mode": {
		"contents":  "TEMPLATE_CHECKOUT_MODE_CONTENTS",
		"directory": "TEMPLATE_CHECKOUT_MODE_DIRECTORY",
	},
	"runtime.runAt": {
		"boot":   "TEMPLATE_RUNTIME_RUN_AT_BOOT",
		"build":  "TEMPLATE_RUNTIME_RUN_AT_BUILD",
		"manual": "TEMPLATE_RUNTIME_RUN_AT_MANUAL",
	},
	"runtime.run_at": {
		"boot":   "TEMPLATE_RUNTIME_RUN_AT_BOOT",
		"build":  "TEMPLATE_RUNTIME_RUN_AT_BUILD",
		"manual": "TEMPLATE_RUNTIME_RUN_AT_MANUAL",
	},
	"runtime.restartPolicy": {
		"never":      "TEMPLATE_RESTART_POLICY_NEVER",
		"on-failure": "TEMPLATE_RESTART_POLICY_ON_FAILURE",
		"always":     "TEMPLATE_RESTART_POLICY_ALWAYS",
	},
	"runtime.restart_policy": {
		"never":      "TEMPLATE_RESTART_POLICY_NEVER",
		"on-failure": "TEMPLATE_RESTART_POLICY_ON_FAILURE",
		"always":     "TEMPLATE_RESTART_POLICY_ALWAYS",
	},
	"runtime.snapshotMode": {
		"filesystem": "TEMPLATE_SNAPSHOT_MODE_FILESYSTEM",
		"memory":     "TEMPLATE_SNAPSHOT_MODE_MEMORY",
	},
	"runtime.snapshot_mode": {
		"filesystem": "TEMPLATE_SNAPSHOT_MODE_FILESYSTEM",
		"memory":     "TEMPLATE_SNAPSHOT_MODE_MEMORY",
	},
}

// normalizeTemplateSpecJSON rewrites only authored enum fields while preserving object order.
func normalizeTemplateSpecJSON(data []byte, toAuthored bool) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var out bytes.Buffer
	if err := writeNormalizedTemplateJSONValue(decoder, &out, nil, toAuthored); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return out.Bytes(), nil
}

func writeNormalizedTemplateJSONValue(decoder *json.Decoder, out *bytes.Buffer, path []string, toAuthored bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			out.WriteByte('{')
			seen := map[string]struct{}{}
			first := true
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
				seen[key] = struct{}{}
				if !first {
					out.WriteByte(',')
				}
				first = false
				encodedKey, _ := json.Marshal(key)
				out.Write(encodedKey)
				out.WriteByte(':')
				if err := writeNormalizedTemplateJSONValue(decoder, out, append(path, key), toAuthored); err != nil {
					return err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return err
			}
			out.WriteByte('}')
			return nil
		case '[':
			out.WriteByte('[')
			first := true
			for decoder.More() {
				if !first {
					out.WriteByte(',')
				}
				first = false
				if err := writeNormalizedTemplateJSONValue(decoder, out, path, toAuthored); err != nil {
					return err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return err
			}
			out.WriteByte(']')
			return nil
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
	}

	if value, ok := token.(string); ok {
		if values := authoredTemplateEnumFields[strings.Join(path, ".")]; values != nil {
			if toAuthored {
				for short, protobufName := range values {
					if value == protobufName {
						value = short
						break
					}
				}
			} else if protobufName, exists := values[value]; exists {
				value = protobufName
			}
		}
		token = value
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return err
	}
	out.Write(encoded)
	return nil
}

func (s TemplateSpec) protoSpec() *sandboxv1.TemplateBuildSpec {
	if s.spec == nil {
		return defaultTemplateSpecProto()
	}
	return s.spec
}

func (s TemplateSpec) toProto() *sandboxv1.TemplateBuildSpec {
	return s.clone()
}

func templateSpecFromProto(spec *sandboxv1.TemplateBuildSpec) *TemplateSpec {
	if spec == nil {
		return nil
	}
	return &TemplateSpec{spec: proto.Clone(spec).(*sandboxv1.TemplateBuildSpec)}
}

func checkoutModeToProto(mode CheckoutMode) sandboxv1.TemplateCheckoutMode {
	switch mode {
	case CheckoutModeContents:
		return sandboxv1.TemplateCheckoutMode_TEMPLATE_CHECKOUT_MODE_CONTENTS
	case CheckoutModeDirectory:
		return sandboxv1.TemplateCheckoutMode_TEMPLATE_CHECKOUT_MODE_DIRECTORY
	default:
		return sandboxv1.TemplateCheckoutMode_TEMPLATE_CHECKOUT_MODE_UNSPECIFIED
	}
}

func runAtToProto(runAt RunAt) sandboxv1.TemplateRuntimeRunAt {
	switch runAt {
	case RunAtBoot:
		return sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_BOOT
	case RunAtBuild:
		return sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_BUILD
	case RunAtManual:
		return sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_MANUAL
	default:
		return sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_UNSPECIFIED
	}
}

func restartPolicyToProto(policy TemplateRestartPolicy) sandboxv1.TemplateRestartPolicy {
	switch policy {
	case RestartNever:
		return sandboxv1.TemplateRestartPolicy_TEMPLATE_RESTART_POLICY_NEVER
	case RestartOnFailure:
		return sandboxv1.TemplateRestartPolicy_TEMPLATE_RESTART_POLICY_ON_FAILURE
	case RestartAlways:
		return sandboxv1.TemplateRestartPolicy_TEMPLATE_RESTART_POLICY_ALWAYS
	default:
		return sandboxv1.TemplateRestartPolicy_TEMPLATE_RESTART_POLICY_UNSPECIFIED
	}
}

func snapshotModeToProto(mode SnapshotMode) sandboxv1.TemplateSnapshotMode {
	switch mode {
	case SnapshotModeFilesystem:
		return sandboxv1.TemplateSnapshotMode_TEMPLATE_SNAPSHOT_MODE_FILESYSTEM
	case SnapshotModeMemory:
		return sandboxv1.TemplateSnapshotMode_TEMPLATE_SNAPSHOT_MODE_MEMORY
	default:
		return sandboxv1.TemplateSnapshotMode_TEMPLATE_SNAPSHOT_MODE_UNSPECIFIED
	}
}

func readyWhenToProto(ready ReadyWhen) *sandboxv1.TemplateSnapshotWhen {
	out := &sandboxv1.TemplateSnapshotWhen{}
	timeout := ready.Timeout
	if timeout <= 0 {
		timeout = defaultReadyWhenTimeout
	}
	out.TimeoutSeconds = uint32(timeout / time.Second)
	if ready.PollInterval > 0 {
		out.PollIntervalSeconds = uint32(ready.PollInterval / time.Second)
	}
	for _, check := range ready.Checks {
		protoCheck := &sandboxv1.TemplateSnapshotCheck{}
		switch {
		case check.http != nil:
			protoCheck.Check = &sandboxv1.TemplateSnapshotCheck_Http{Http: proto.Clone(check.http).(*sandboxv1.TemplateHTTPReadyCheck)}
		case check.exec != nil:
			protoCheck.Check = &sandboxv1.TemplateSnapshotCheck_Exec{Exec: proto.Clone(check.exec).(*sandboxv1.TemplateExecReadyCheck)}
		default:
			protoCheck.Check = &sandboxv1.TemplateSnapshotCheck_Port{Port: uint32(max(check.port, 0))}
		}
		out.Checks = append(out.Checks, protoCheck)
	}
	return out
}

// Validate reports every violation in the spec as a *TemplateSpecValidationError.
// It mirrors the obvious server contract rules; the server remains authoritative.
func (s TemplateSpec) Validate() error {
	spec := s.protoSpec()
	var violations []TemplateSpecViolation
	add := func(field, rule, message string) {
		violations = append(violations, TemplateSpecViolation{Field: field, Rule: rule, Message: message})
	}

	if spec.SpecVersion != TemplateSpecVersion {
		add("specVersion", "const", fmt.Sprintf("specVersion must be %q", TemplateSpecVersion))
	}
	validateTemplateSpecBase(spec.Base, add)
	validateOptionalAbsolutePath("workdir", spec.Workdir, add)
	validateTemplateSpecContext(spec.Context, add)
	if spec.Build != nil && len(spec.Build.Env) > 256 {
		add("build.env", "max_pairs", "build env supports at most 256 variables")
	}
	validateTemplateSpecSteps(spec, add)
	validateTemplateSpecRuntime(spec.Runtime, add)
	validateTemplateSpecResources(spec.Resources, add)

	if len(violations) == 0 {
		return nil
	}
	return &TemplateSpecValidationError{Violations: violations}
}

func validateTemplateSpecBase(base *sandboxv1.TemplateBase, add func(field, rule, message string)) {
	if base == nil {
		add("base", "required", "exactly one base source is required")
		return
	}
	switch source := base.Source.(type) {
	case *sandboxv1.TemplateBase_Image:
		if source.Image == "" {
			add("base.image", "required", "base image cannot be empty")
		}
	case *sandboxv1.TemplateBase_TemplateId:
		if !templateUUIDPattern.MatchString(source.TemplateId) {
			add("base.templateId", "uuid", "base template ID must be a UUID")
		}
	case *sandboxv1.TemplateBase_SnapshotId:
		if !templateUUIDPattern.MatchString(source.SnapshotId) {
			add("base.snapshotId", "uuid", "base snapshot ID must be a UUID")
		}
	default:
		add("base", "required", "exactly one base source is required")
	}
}

func validateTemplateSpecContext(context *sandboxv1.TemplateContext, add func(field, rule, message string)) {
	if context == nil {
		return
	}
	git := context.GetSource().GetGit()
	if git == nil {
		add("context.source.git", "required", "a Git context source is required")
	} else {
		validateStringLength("context.source.git.repo", git.Repo, 1, 4096, add)
		validateStringLength("context.source.git.ref", git.Ref, 1, 1024, add)
	}
	checkout := context.Checkout
	if checkout != nil {
		validateOptionalAbsolutePath("context.checkout.dest", checkout.Dest, add)
		if checkout.Name != nil {
			name := checkout.GetName()
			if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 255 || name == "." || name == ".." || strings.Contains(name, "/") {
				add("context.checkout.name", "safe_name", "checkout name must be a safe 1-255 character name")
			}
			if checkout.Mode != sandboxv1.TemplateCheckoutMode_TEMPLATE_CHECKOUT_MODE_DIRECTORY {
				add("context.checkout.name", "directory_mode_only", "checkout name is only valid in directory mode")
			}
		}
	}
	if len(context.Ignore) > 256 {
		add("context.ignore", "max_items", "at most 256 ignore rules are supported")
	}
	for index, rule := range context.Ignore {
		validateStringLength(fmt.Sprintf("context.ignore[%d]", index), rule, 0, maxTemplatePathLength, add)
	}
}

func contextRelativePathViolation(path string) string {
	if path == "" {
		return "path is required"
	}
	if strings.HasPrefix(path, "/") {
		return "path must be relative to the Git context"
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return "path cannot traverse above the Git context"
		}
	}
	return ""
}

func validateCommandOrArgv(field, command string, argv []string, add func(field, rule, message string)) {
	hasCommand := command != ""
	hasArgv := len(argv) > 0
	if hasCommand == hasArgv {
		add(field, "command_or_argv", "exactly one of command or argv is required")
	}
	if utf8.RuneCountInString(command) > 65536 {
		add(field+".command", "max_len", "command cannot exceed 65536 characters")
	}
	if len(argv) > 1024 {
		add(field+".argv", "max_items", "argv supports at most 1024 items")
	}
	for index, arg := range argv {
		if arg == "" {
			add(fmt.Sprintf("%s.argv[%d]", field, index), "min_len", "argv entries cannot be empty")
		}
	}
}

func validateStringLength(field, value string, minLength, maxLength int, add func(field, rule, message string)) {
	length := utf8.RuneCountInString(value)
	if length < minLength || length > maxLength {
		add(field, "length", fmt.Sprintf("must be between %d and %d characters", minLength, maxLength))
	}
}

func validateOptionalAbsolutePath(field, path string, add func(field, rule, message string)) {
	if utf8.RuneCountInString(path) > maxTemplatePathLength {
		add(field, "max_len", fmt.Sprintf("path cannot exceed %d characters", maxTemplatePathLength))
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		add(field, "absolute_path", "path must be absolute")
	}
}

func validateAbsolutePath(field, path string, add func(field, rule, message string)) {
	if path == "" {
		add(field, "required", "path is required")
		return
	}
	validateOptionalAbsolutePath(field, path, add)
}

func validateTemplateSpecSteps(spec *sandboxv1.TemplateBuildSpec, add func(field, rule, message string)) {
	if len(spec.Steps) > 256 {
		add("steps", "max_items", "at most 256 steps are supported")
	}
	hasContext := spec.Context != nil
	for index, step := range spec.Steps {
		prefix := fmt.Sprintf("steps[%d]", index)
		if step.Name != nil {
			validateStringLength(prefix+".name", step.GetName(), 1, 128, add)
		}
		switch operation := step.Operation.(type) {
		case *sandboxv1.TemplateStep_Run:
			run := operation.Run
			validateCommandOrArgv(prefix+".run", run.Command, run.Argv, add)
			validateOptionalAbsolutePath(prefix+".run.workdir", run.Workdir, add)
			if run.TimeoutSeconds > 86400 {
				add(prefix+".run.timeoutSeconds", "lte", "run timeout cannot exceed 86400 seconds")
			}
		case *sandboxv1.TemplateStep_Copy:
			if !hasContext {
				add(prefix+".copy", "requires_context", "copy steps require a Git context")
			}
			if message := contextRelativePathViolation(operation.Copy.Src); message != "" {
				add(prefix+".copy.src", "context_relative", message)
			}
			if utf8.RuneCountInString(operation.Copy.Src) > maxTemplatePathLength {
				add(prefix+".copy.src", "max_len", fmt.Sprintf("path cannot exceed %d characters", maxTemplatePathLength))
			}
			validateAbsolutePath(prefix+".copy.dest", operation.Copy.Dest, add)
		case *sandboxv1.TemplateStep_WriteFile:
			validateAbsolutePath(prefix+".writeFile.path", operation.WriteFile.Path, add)
			if len(operation.WriteFile.Content) > 1048576 {
				add(prefix+".writeFile.content", "max_len", "inline file content cannot exceed 1 MiB")
			}
			if operation.WriteFile.Mode != nil && operation.WriteFile.GetMode() > 0o7777 {
				add(prefix+".writeFile.mode", "lte", "file mode cannot exceed 0o7777")
			}
		case *sandboxv1.TemplateStep_Mkdir:
			validateAbsolutePath(prefix+".mkdir.path", operation.Mkdir.Path, add)
			if operation.Mkdir.Mode != nil && operation.Mkdir.GetMode() > 0o7777 {
				add(prefix+".mkdir.mode", "lte", "directory mode cannot exceed 0o7777")
			}
		case *sandboxv1.TemplateStep_Remove:
			validateAbsolutePath(prefix+".remove.path", operation.Remove.Path, add)
		case *sandboxv1.TemplateStep_Rename:
			validateAbsolutePath(prefix+".rename.src", operation.Rename.Src, add)
			validateAbsolutePath(prefix+".rename.dest", operation.Rename.Dest, add)
		case *sandboxv1.TemplateStep_Symlink:
			validateStringLength(prefix+".symlink.target", operation.Symlink.Target, 1, maxTemplatePathLength, add)
			validateAbsolutePath(prefix+".symlink.path", operation.Symlink.Path, add)
		case *sandboxv1.TemplateStep_Apt:
			validatePackageStep(prefix+".apt", operation.Apt, add)
		case *sandboxv1.TemplateStep_Pip:
			validatePackageStep(prefix+".pip", operation.Pip, add)
		case *sandboxv1.TemplateStep_Npm:
			validatePackageStep(prefix+".npm", operation.Npm, add)
		case *sandboxv1.TemplateStep_Bun:
			validatePackageStep(prefix+".bun", operation.Bun, add)
		default:
			add(prefix, "operation_required", "exactly one step operation is required")
		}
	}
}

func validatePackageStep(field string, step *sandboxv1.TemplatePackageStep, add func(field, rule, message string)) {
	packages := step.GetPackages()
	if len(packages) == 0 {
		add(field+".packages", "min_items", "at least one package is required")
		return
	}
	if len(packages) > 1024 {
		add(field+".packages", "max_items", "at most 1024 packages are supported")
	}
	seen := make(map[string]struct{}, len(packages))
	duplicateReported := false
	for index, name := range packages {
		if _, exists := seen[name]; exists && !duplicateReported {
			add(field+".packages", "unique", "package names must be unique")
			duplicateReported = true
		} else {
			seen[name] = struct{}{}
		}
		if name == "" {
			add(fmt.Sprintf("%s.packages[%d]", field, index), "min_len", "package names cannot be empty")
			continue
		}
		if strings.HasPrefix(name, "-") || strings.ContainsRune(name, '\x00') {
			add(fmt.Sprintf("%s.packages[%d]", field, index), "safe_name", "package names cannot start with - or contain NUL")
		}
	}
}

var readyHTTPURLPattern = regexp.MustCompile(`^https?://(localhost|127\.0\.0\.1|\[::1\])(:[0-9]+)?(/.*)?$`)

func validateTemplateSpecRuntime(runtime *sandboxv1.TemplateRuntime, add func(field, rule, message string)) {
	if runtime == nil {
		return
	}
	if len(runtime.Env) > 256 {
		add("runtime.env", "max_pairs", "runtime env supports at most 256 variables")
	}
	switch entrypoint := runtime.Entrypoint.(type) {
	case *sandboxv1.TemplateRuntime_Start:
		start := entrypoint.Start
		validateCommandOrArgv("runtime.start", start.Command, start.Argv, add)
		validateOptionalAbsolutePath("runtime.start.workdir", start.Workdir, add)
	case *sandboxv1.TemplateRuntime_ProcessCompose:
		processCompose := entrypoint.ProcessCompose
		if message := contextRelativePathViolation(processCompose.ConfigPath); message != "" {
			add("runtime.processCompose.configPath", "workdir_relative", strings.ReplaceAll(message, "Git context", "workdir"))
		}
		if utf8.RuneCountInString(processCompose.ConfigPath) > maxTemplatePathLength {
			add("runtime.processCompose.configPath", "max_len", fmt.Sprintf("path cannot exceed %d characters", maxTemplatePathLength))
		}
		validateOptionalAbsolutePath("runtime.processCompose.workdir", processCompose.Workdir, add)
		if len(processCompose.EnvFiles) > 32 {
			add("runtime.processCompose.envFiles", "max_items", "at most 32 env files are supported")
		}
		seen := make(map[string]struct{}, len(processCompose.EnvFiles))
		duplicateReported := false
		for index, envFile := range processCompose.EnvFiles {
			if _, exists := seen[envFile]; exists && !duplicateReported {
				add("runtime.processCompose.envFiles", "unique", "env files must be unique")
				duplicateReported = true
			} else {
				seen[envFile] = struct{}{}
			}
			if message := contextRelativePathViolation(envFile); message != "" {
				add(fmt.Sprintf("runtime.processCompose.envFiles[%d]", index), "workdir_relative", strings.ReplaceAll(message, "Git context", "workdir"))
			}
			if utf8.RuneCountInString(envFile) > maxTemplatePathLength {
				add(fmt.Sprintf("runtime.processCompose.envFiles[%d]", index), "max_len", fmt.Sprintf("path cannot exceed %d characters", maxTemplatePathLength))
			}
		}
	default:
		add("runtime", "entrypoint_required", "a runtime requires a start command or process-compose config")
	}

	isBuildRuntime := runtime.RunAt == sandboxv1.TemplateRuntimeRunAt_TEMPLATE_RUNTIME_RUN_AT_BUILD
	if isBuildRuntime && runtime.ReadyWhen == nil {
		add("runtime.readyWhen", "build_requires_ready_when", "build runtime requires readyWhen")
	}
	if runtime.SnapshotMode != sandboxv1.TemplateSnapshotMode_TEMPLATE_SNAPSHOT_MODE_UNSPECIFIED && !isBuildRuntime {
		add("runtime.snapshotMode", "build_runtime_only", "snapshotMode is only valid when runAt is build")
	}
	if runtime.StopGraceSeconds > 300 {
		add("runtime.stopGraceSeconds", "lte", "stop grace cannot exceed 300 seconds")
	}
	validateTemplateSpecReadyWhen(runtime.ReadyWhen, add)
}

func validateTemplateSpecReadyWhen(readyWhen *sandboxv1.TemplateSnapshotWhen, add func(field, rule, message string)) {
	if readyWhen == nil {
		return
	}
	if readyWhen.TimeoutSeconds < 1 || readyWhen.TimeoutSeconds > 86400 {
		add("runtime.readyWhen.timeoutSeconds", "range", "readiness timeout must be between 1 and 86400 seconds")
	}
	if readyWhen.PollIntervalSeconds > 60 {
		add("runtime.readyWhen.pollIntervalSeconds", "lte", "poll interval cannot exceed 60 seconds")
	}
	if len(readyWhen.Checks) > 64 {
		add("runtime.readyWhen.checks", "max_items", "at most 64 readiness checks are supported")
	}
	for index, check := range readyWhen.Checks {
		prefix := fmt.Sprintf("runtime.readyWhen.checks[%d]", index)
		switch checkKind := check.Check.(type) {
		case *sandboxv1.TemplateSnapshotCheck_Port:
			if checkKind.Port < 1 || checkKind.Port > 65535 {
				add(prefix+".port", "range", "port must be between 1 and 65535")
			}
		case *sandboxv1.TemplateSnapshotCheck_Http:
			url := checkKind.Http.GetUrl()
			if utf8.RuneCountInString(url) > maxTemplatePathLength {
				add(prefix+".http.url", "max_len", fmt.Sprintf("URL cannot exceed %d characters", maxTemplatePathLength))
			}
			if !readyHTTPURLPattern.MatchString(url) {
				add(prefix+".http.url", "localhost_only", "http checks must target localhost, 127.0.0.1, or [::1]")
			}
			if len(checkKind.Http.GetSuccessStatusCodes()) > 100 {
				add(prefix+".http.successStatusCodes", "max_items", "at most 100 status codes are supported")
			}
			seen := make(map[uint32]struct{}, len(checkKind.Http.GetSuccessStatusCodes()))
			duplicateReported := false
			for codeIndex, code := range checkKind.Http.GetSuccessStatusCodes() {
				if _, exists := seen[code]; exists && !duplicateReported {
					add(prefix+".http.successStatusCodes", "unique", "status codes must be unique")
					duplicateReported = true
				} else {
					seen[code] = struct{}{}
				}
				if code < 100 || code > 599 {
					add(fmt.Sprintf("%s.http.successStatusCodes[%d]", prefix, codeIndex), "range", "status codes must be between 100 and 599")
				}
			}
		case *sandboxv1.TemplateSnapshotCheck_Exec:
			validateCommandOrArgv(prefix+".exec", checkKind.Exec.GetCommand(), checkKind.Exec.GetArgv(), add)
			if checkKind.Exec.GetTimeoutSeconds() > 300 {
				add(prefix+".exec.timeoutSeconds", "lte", "exec check timeout cannot exceed 300 seconds")
			}
		default:
			add(prefix, "check_required", "exactly one readiness check is required")
		}
	}
}

func validateTemplateSpecResources(resources *sandboxv1.TemplateResources, add func(field, rule, message string)) {
	if resources == nil {
		return
	}
	if resources.CpuCores < 0 || resources.CpuCores > 16 {
		add("resources.cpuCores", "range", "cpuCores must be between 0 and 16")
	}
	if resources.MemoryMb != 0 && (resources.MemoryMb < 512 || resources.MemoryMb > 65536 || resources.MemoryMb%2 != 0) {
		add("resources.memoryMb", "range", "memoryMb must be 0 or an even value between 512 and 65536")
	}
	if resources.DiskSizeGb != 0 && (resources.DiskSizeGb < 5 || resources.DiskSizeGb > 100) {
		add("resources.diskSizeGb", "range", "diskSizeGb must be 0 or between 5 and 100")
	}
}
