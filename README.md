# Tenki Sandbox Go SDK

Go client for `tenki.sandbox.v1.SandboxService`.

## Install

```bash
go get github.com/LuxorLabs/tenki-sdk-go/sandbox
```

## Quickstart

More runnable snippets live under `examples/`.

### Zero-config (env vars)

```bash
export TENKI_API_KEY=tk_your_api_key
# TENKI_API_URL defaults to https://api.tenki.cloud
```

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	tenkisandbox "github.com/LuxorLabs/tenki-sdk-go/sandbox"
)

func main() {
	ctx := context.Background()

	client, err := tenkisandbox.New()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// create waits by default: the server holds the response until the sandbox
	// is RUNNING with data-plane access primed, so it is exec-ready on return.
	session, err := client.Create(ctx, tenkisandbox.WithWaitTimeout(2*time.Minute))
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close(ctx)

	result, err := session.Exec(
		ctx,
		"echo",
		tenkisandbox.WithArgs("hello"),
		tenkisandbox.WithTimeout(5*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("status=%s exit=%d stdout=%s\n", result.Status, result.ExitCode, string(result.Stdout))
}
```

### Explicit config

```go
client, err := tenkisandbox.New(
	tenkisandbox.WithAuthToken("tk_your_api_key"),
	tenkisandbox.WithBaseURL("https://api.tenki.cloud"),
	tenkisandbox.WithHTTPTimeout(10*time.Second),
)
```

Creation warnings are retained on `session.Warnings` and emitted to stderr by default.
Replace or suppress emission with `WithWarningHandler`:

```go
client, err := tenkisandbox.New(
	tenkisandbox.WithWarningHandler(func(warning tenkisandbox.SandboxWarning) {
		log.Printf("sandbox warning [%s]: %s", warning.Code, warning.Message)
	}),
)
```

When `WithSticky()` and `WithMaxDuration(...)` are both supplied, sticky takes precedence and the requested duration is discarded.
The returned session contains `STICKY_OVERRIDES_MAX_DURATION`.
Sticky disables automatic idle pause and remains enabled across manual pause and resume.

## Configuration

Auth token resolution: `WithAuthToken()` > `TENKI_AUTH_TOKEN` env var > `TENKI_API_KEY` env var > error.
Base URL resolution: `WithBaseURL()` > `TENKI_API_URL` env var > `https://api.tenki.cloud`.

### Migrating to v1.0.0

Version 1.0.0 removes all deprecated Project fields, messages, and RPCs from
the generated v1 API. The protobuf package remains `tenki.sandbox.v1`; use
Workspace-scoped fields and RPCs instead.

### Migrating to v0.6.0

Version 0.6.0 removes `WithCookieName` and support for Ory session tokens and
browser cookie values. Use a `tk_` API key or service token with
`WithAuthToken`; the SDK sends it as an `Authorization: Bearer` credential.

| Env Var            | Description                                           |
| ------------------ | ----------------------------------------------------- |
| `TENKI_AUTH_TOKEN` | Auth token fallback (API key or service token), first |
| `TENKI_API_KEY`    | Auth token fallback (API key or service token)        |
| `TENKI_API_URL`    | Base URL fallback                                     |

Use an API key or service token (`tk_*`), sent as `Authorization: Bearer <token>`.
Workspace API keys determine Sandbox scope automatically; ordinary calls do not require a Workspace ID.

## API

### Client

- `New(opts ...Option) (*Client, error)`
- `(*Client).Create(ctx, opts ...CreateOption) (*Session, error)` — preferred: waits by default and returns a RUNNING, exec-ready session
- `(*Client).CreateAndWait(ctx, timeout, opts ...CreateOption) (*Session, error)` — compatibility wrapper around `Create`
- `(*Client).List(ctx, opts...) ([]*Session, error)`
- `(*Client).Get(ctx, sessionID string) (*Session, error)`
- `(*Client).WhoAmI(ctx) (*Identity, error)`
- `(*Client).Close() error`

### Session

- `(*Session).Exec(ctx, command string, opts ...ExecOption) (*Result, error)`
- `(*Session).WriteFile(ctx, path string, data []byte) error`
- `(*Session).ReadFile(ctx, path string) ([]byte, error)`
- `(*Session).WaitReady(ctx, timeout) error` — alternative wait for sessions obtained via `Get`/`List`; `Create` already returns ready sessions by default
- `(*Session).Pause(ctx) error`
- `(*Session).PauseAsync(ctx) error`
- `(*Session).WaitPaused(ctx, timeout) error` - waits for durable pause completion; pass a non-positive timeout to wait until `ctx` is canceled
- `(*Session).Resume(ctx) error`
- `(*Session).WaitResumed(ctx, timeout) error`
- `(*Session).Close(ctx) error`

### Durable pause

`PauseAsync` returns after the service durably accepts the request and moves the session to `PAUSING` on a node that supports asynchronous pause.
Snapshot capture and persistence continue in the background; acceptance does not mean the VM has stopped or the snapshot is durable.
`Pause` retains its existing completion behavior for compatibility.
Call `WaitPaused` before resuming or relying on a durable checkpoint.
It returns when the session reaches `PAUSED`, returns `ErrPauseFailed` if a verified rollback restores `RUNNING`, and respects context cancellation.

```go
if err := session.PauseAsync(ctx); err != nil {
	return err
}
if err := session.WaitPaused(ctx, 0); err != nil {
	return err
}
```

`DURABLE_CEPH` snapshots can resume on a capability-matched host in the same datacenter while R2 replication continues.
`DURABLE` snapshots are portable to eligible hosts.
`LOCAL_READY` is not resumable for pause operations.

### What survives a pause

Pause captures the VM's full memory, not only its disk: a resumed session is the
same running machine, not a reboot. Processes keep their PIDs and their in-memory
state, and the guest clock stops for the duration of the pause.

`/tmp` is cleared across a pause. Keep durable state under `/home/tenki`, which
is preserved.

While a session is paused it serves no traffic: preview requests return `404`
until it resumes. The client connection itself is not dropped — a held
keep-alive connection returns `200` again after resume — so treat a paused
session as unavailable rather than disconnected.

SSH is different: a held transport fails on use while paused and does not
recover after resume, so reconnect rather than retry on it. A fresh
connection succeeds as soon as the session is `RUNNING` again.

```go
sh := func(line string) (*tenkisandbox.Result, error) {
	return session.Exec(ctx, "bash", tenkisandbox.WithArgs("-lc", line))
}

sh("echo hello > ~/marker")
sh("setsid nohup sleep 3600 >/dev/null 2>&1 </dev/null &")
pid, _ := sh("pgrep -n sleep")

if err := session.Pause(ctx); err != nil {
	return err
}
if err := session.Resume(ctx); err != nil {
	return err
}

marker, _ := sh("cat ~/marker")
alive, _ := sh("kill -0 " + pid.StdoutString() + " && echo alive")

fmt.Println(marker.StdoutString()) // "hello" - the marker file survived
fmt.Println(alive.StdoutString())  // "alive" - same process, same memory
```

### Process control

- `(*Session).Command(argv []string, opts ...RunOptions) *Command`
- `(*Command).Exec(ctx) (*Result, error)` — buffered; blocks until the process exits
- `(*Command).Stream(ctx) (*RunHandle, error)` — live handle; blocks until the process has started
- `(*RunHandle).Signal(os.Signal) error` / `(*RunHandle).Kill() error` — enqueue a signal frame; both return once it is sent, **not** when the process has exited
- `(*RunHandle).Wait() (*Result, error)` — blocks until the process is actually dead

```go
proc, _ := sess.Command([]string{"sleep", "3600"}).Stream(ctx)
_ = proc.Signal(syscall.SIGTERM) // returns once the frame is sent
res, _ := proc.Wait()            // returns once the process has exited
// res.Status == CommandStatusFailed, res.ExitCode == -1
```

Unlike the Python and TypeScript SDKs, Go's `Result` does not expose which
signal terminated the process — the wire value is only used to derive `Status`.

`Kill()` always sends SIGKILL — use `Signal(syscall.SIGTERM)` for a graceful
stop. Only SIGKILL, SIGTERM, SIGINT, SIGHUP, SIGUSR1 and SIGUSR2 reach the
guest; **any other `os.Signal` is silently sent as SIGTERM**, so a
`Signal(syscall.SIGQUIT)` stops the process gracefully rather than failing.
Signalling after the process has exited is best-effort: it usually returns a
stream error, but a caller must not rely on getting one. `Wait()` delivers its
result once, so call it from a single goroutine only.

Process lifetime is bound to the Run stream, not to the handle. Once that stream
tears down — you cancel the `ctx` you passed to `Stream`, the connection breaks,
or the edge times the idle connection out (~30s) — the platform sends SIGTERM,
escalates to SIGKILL after 5s and reaps within 10s.

Dropping the handle does **not** tear the stream down on its own: `RunHandle` has
no `Close`, and the request side is closed only after an exit frame or a stream
error. An abandoned process can keep running, and one that keeps writing output
keeps its own stream alive indefinitely. To stop a process, cancel the context or
signal it and `Wait()`. There is no reattach.

### Session Git scope

- `session.Git.Clone(ctx, repo, GitCloneParams)`
- `session.Git.Checkout(ctx, ref, GitCheckoutParams)`
- `session.Git.Diff(ctx, GitDiffParams)`
- `session.Git.Log(ctx, GitLogParams)`
- `session.Git.FetchPR(ctx, prNum, GitFetchPRParams)`

### Disk space

The root disk defaults to 5 GB and holds the base image as well as your work, so a fresh
sandbox already reports around 56% used. It is fixed at create time and cannot be grown in
place — pass `WithDiskSizeGB` (5–100) to `Create` if you need more.

Every `Result` carries `Disk`. `result.Disk.Exhausted()` reports whether the sandbox ran
out of space at any point during the command — check it even when the command exited 0, since
npm reports `ENOSPC` as a warning and still exits successfully with a broken install.

### Volumes

- `(*Client).CreateVolume(ctx, opts ...CreateVolumeOption) (*Volume, error)`
- `(*Client).GetVolume(ctx, volumeID) (*Volume, error)`
- `(*Client).ListVolumes(ctx, opts...) ([]*Volume, error)`
- `(*Client).ResizeVolume(ctx, volumeID, newSizeBytes) (*Volume, error)`
- `(*Client).DeleteVolume(ctx, volumeID) error`
- `(*Session).AttachVolume(ctx, volumeID, mountPath, opts ...VolumeOption) error`
- `(*Session).DetachVolume(ctx, volumeID) error`

### Snapshots

- `(*Client).CreateSnapshot(ctx, sessionID, name, expiresAt) (*Snapshot, error)`
- `(*Client).GetSnapshot(ctx, snapshotID) (*Snapshot, error)`
- `(*Client).ListSnapshots(ctx, opts...) ([]*Snapshot, error)`
- `(*Client).DeleteSnapshot(ctx, snapshotID) (*Snapshot, error)`
- `(*Client).WaitSnapshotReady(ctx, snapshotID, timeout) (*Snapshot, error)`

### Templates

- `(*Client).CreateTemplate(ctx, opts ...TemplateOption) (*Template, error)`
- `(*Client).GetTemplate(ctx, templateID) (*Template, error)`
- `(*Client).ListTemplates(ctx, opts...) ([]*Template, error)`
- `(*Client).UpdateTemplate(ctx, templateID, opts ...TemplateOption) (*Template, error)`
- `(*Client).DeleteTemplate(ctx, templateID) (*Template, error)`
- `(*Client).BuildTemplate(ctx, templateID) (*TemplateBuild, error)`
- `(*Client).WaitForTemplateBuild(ctx, buildID) (*TemplateBuild, error)`

### SSH

Open an interactive SSH stream to a session over the gateway. `SSHConn`
implements `io.ReadWriteCloser`.

- `(*Session).SSH(ctx, opts ...SSHOption) (*SSHConn, error)`
- `(*Client).SSH(ctx, sessionID string, opts ...SSHOption) (*SSHConn, error)`
- `WithGatewayURL(string)` - pin the gateway URL (otherwise derived from base URL)

```go
conn, err := session.SSH(ctx)
if err != nil {
	log.Fatal(err)
}
defer conn.Close()
io.Copy(os.Stdout, conn) // read sandbox output; write to conn to send input
```

### Host-port tunnels & preview URLs

Expose a port from inside the sandbox to the caller.

- `(*Session).ExposeHostPort(ctx, hostAddr string, opts ...HostPortTunnelOptions) (*HostPortTunnel, error)`
- `(*Session).HostPortTunnel(ctx, host string, port int, opts ...HostPortTunnelOptions)`
- `(*Session).ExposeHostPortResilient(ctx, hostAddr, opts ...ResilientHostPortTunnelOptions)` - auto-reconnect
- `(*ResilientHostPortTunnel).Endpoint()` - read the current address and port safely during reconnects
- `(*HostPortTunnel).Terminated() <-chan HostPortTunnelTermination` / `.Close()`

Publish a stable, browser-openable URL bound to a session port:

- `(*Client).CreatePreviewURL(ctx, slug string, sessionID *string, port *int32) (*PreviewURL, error)`
- `(*Client).BindPreviewURL(ctx, previewURLID, sessionID string, port int32)` / `UnbindPreviewURL`
- `(*Client).ListPreviewURLs(ctx)` / `GetPreviewURL` / `DeletePreviewURL`

For resilient tunnels, replace reads of `SandboxAddress` and `SandboxPort` with
`address, port := tunnel.Endpoint()`. The method returns both values from the same
connection. During reconnects and after closure it retains the last opened endpoint;
use `State()` to check availability. Ordinary `HostPortTunnel` fields are unchanged.

### Registry

Publish and share sandbox images (templates/snapshots/images).

- `(*Client).PublishRegistryImage(ctx, opts ...RegistryPublishOption) (*RegistryPublishResult, error)`
- `(*Client).ListRegistryImages(ctx, opts ...RegistryListOption)` / `GetRegistryImage` / `ResolveRegistryRef`
- `(*Client).UnpublishRegistryImage` / `DeleteRegistryImage`
- `(*Client).DeleteRegistryImageVersion(ctx, imageID, snapshotID string) (*RegistryVersionDeleteResult, error)` — delete one untagged, non-latest, unshared version
- Sharing: `ShareImage`, `RevokeRegistryShareGrant`, `ListRegistryShareGrants`,
  `UnshareRegistryImage`

## Options

### Client options

- `WithAuthToken(string)` - API key or service token
- `WithBaseURL(string)` default: `https://api.tenki.cloud`
- `WithHTTPClient(*http.Client)`
- `WithHTTPTimeout(time.Duration)` default: `30s`
- `WithConnectClientOptions(...connect.ClientOption)`
- `WithWarningHandler(WarningHandler)`; pass `nil` to suppress warning emission

### Create options

- `WithName(string)`
- `WithAllowInbound(bool)` default: `true` (sent explicitly on every Create)
- `WithAllowOutbound(bool)` default: `true` (sent explicitly on every Create)
  - both are create-time settings and cannot be changed on an existing session;
    `session.InboundEnabled` / `session.OutboundEnabled` report what a session was created with
- `WithEnvs(map[string]string)` session-scoped env defaults
- `WithMaxDuration(time.Duration)`
- `WithSticky()`; overrides and discards `WithMaxDuration`
- `WithCPUCores(int32)` default: `2`
- `WithMemoryMB(int32)` default: `4096`
- `WithMetadata(map[string]string)`
- `WithSSHKeys([]string)`
- `WithVolume(volumeID, mountPath, ...VolumeOption)`
- `WithSnapshot(snapshotID)` / `WithImage(image)` (mutually exclusive)
- `WithCloneRepo(repoURL)` / `WithGitHubToken(token)`
- `WithAllowDomains(...string)` / `WithAllowCIDRs(...string)` restrict outbound access; no effect when `WithAllowOutbound(false)` is set

### Restricting outbound access

A session that may only reach PyPI:

```go
session, err := client.Create(ctx,
	tenkisandbox.WithAllowDomains("pypi.org", "*.pypi.org", "files.pythonhosted.org"),
)
```

`WithAllowOutbound(false)` still blocks everything, regardless of `WithAllowDomains`/`WithAllowCIDRs`.
Read the allowlist back with `session.Egress()`.

### Exec options

- `WithArgs(...string)`
- `WithTimeout(time.Duration)` — unbounded when unset
- `WithEnv(key, value string)`
- `WithEnvs(map[string]string)` command env overrides

### Timeouts

Commands are unbounded by default. `WithTimeout` sends the budget to the
guest-agent, which enforces it: on expiry it signals the process (`SIGTERM`,
escalating to `SIGKILL`) and reports the run as timed out.

A timeout is **not** a Go error — it comes back as an ordinary result with
`CommandStatusTimedOut`, carrying the output captured before the budget expired:

```go
result, err := session.Exec(ctx, "bash",
	tenkisandbox.WithArgs("-lc", "npm ci"),
	tenkisandbox.WithTimeout(2*time.Minute),
)
if err != nil {
	return err // transport or session failure, not a timeout
}
if result.Status.IsTimedOut() {
	// result.Reason is "timeout", or "grace_timeout" when the guest could not
	// reap the process. Partial output usually shows where it stalled.
	log.Printf("timed out (%s); partial output: %s", result.Reason, result.StdoutString())
}
```

The guest caps the request at its own configured maximum command timeout, and
older guest-agents accept `WithTimeout` but ignore it, leaving the run unbounded.

### Background processes and long-running services

A command returns when its **stdout and stderr reach EOF**, not when the shell
exits. A backgrounded process inherits both streams and holds them open, so this
waits for the server rather than the shell:

```go
// Hangs: the server inherits stdout/stderr.
session.Exec(ctx, "sh", tenkisandbox.WithArgs("-lc", "python3 -m http.server 3000 &"))
```

Redirect **both** streams to detach it:

```go
session.Exec(ctx, "sh",
	tenkisandbox.WithArgs("-lc", "python3 -m http.server 3000 >/home/tenki/http.log 2>&1 &"))
```

Redirecting only stdout is not enough — stderr still holds the stream open — and
`nohup` does not help, because it blocks `SIGHUP` rather than stream inheritance.
This is standard POSIX behavior, the same as Go's own `exec.Cmd` with piped output.

To keep hold of a service instead, use `session.Command(...).Stream(ctx)` and read
from the handle; for services you always want running, start them from a template
start command.

That last option is the durable one. Every exec child runs inside the
guest-agent's own systemd cgroup, so a guest-agent restart kills it, and `nohup`
does not change that. Pause and resume restore VM memory, so a backgrounded
process does survive a pause with the same PID — which makes an ad-hoc service
look more durable than it is. Anything load-bearing belongs in a template start
command with a readiness probe.

## Error handling

SDK maps Connect errors to typed errors. Use `errors.Is`:

```go
if errors.Is(err, tenkisandbox.ErrSessionNotFound) { ... }
if errors.Is(err, tenkisandbox.ErrPermissionDenied) { ... }
if errors.Is(err, tenkisandbox.ErrCommandTimeout) { ... }
if errors.Is(err, tenkisandbox.ErrMissingAuthToken) { ... }
```

## Size helpers

```go
tenkisandbox.GB   // 1,000,000,000
tenkisandbox.GiB  // 1,073,741,824
tenkisandbox.MB   // 1,000,000
tenkisandbox.MiB  // 1,048,576
```

## Timeout constants

- `DefaultSessionCreateTimeout` (3m)
- `DefaultSnapshotCreateTimeout` (5m)
- `DefaultRestoreTimeout` (5m)
- `DefaultExecTimeout` (30s) — a suggested value for `WithTimeout`, **not** applied
  automatically; commands are unbounded unless you set a timeout

## Constraints

- File ops are scoped to the sandbox home on server side. Use paths under `/home/tenki/...`.
- Process `cwd` values follow the guest contract: relative paths are normalized under the sandbox guest workdir
  (`/home/tenki` by default), absolute paths are used unchanged, and missing or non-directory targets fail before the
  process starts.
- `cwd` sets the spawned process's directory. A login shell (`bash -lc`) sources the guest's startup files first, so a
  `cd` in `~/.bashrc` or `~/.profile` runs before your command and wins over `cwd`; use `bash -c` when the directory
  must hold.
- Create/list ownership is derived from auth context.
- Volume size: 1 MiB - 100 GiB.
- Session CPU: 1-128 cores. Memory: 128-524288 MB, aligned to 2 MiB. Workspace limits may be lower.

## Workspace secrets

The workspace client exposes secret management through `client.Secrets` and does not require a sandbox Session:

```go
import "github.com/LuxorLabs/tenki-sdk-go/sandbox/workspace"

client, err := workspace.NewWorkspaceClient(workspace.WorkspaceOptions{WorkspaceID: workspaceID})
if err != nil { return err }
defer client.Close()
secret, err := client.Secrets.Create(ctx, "TOKEN", valueBytes,
    workspace.SecretPolicy{
        DeliveryMode: workspace.SecretGuestAndInjection,
        DestinationMode: workspace.SecretDestinationUnset,
    }, requestID)
```

The client uses `TENKI_AUTH_TOKEN` or `TENKI_API_KEY`. Set `BaseURL` or
`TENKI_CLOUD_API_URL` to override the cloud API endpoint independently of sandbox
configuration. Set `WorkspaceID` once in the client options. Leave it empty to use the authenticated workspace key's
scope; other callers must specify one.

`Create`, `Update`, `Get`, `List`, `ListVersions`, `Revoke`, and `Delete` return
metadata only. Secret values are `[]byte`; on update, `nil` retains the value while
`[]byte{}` creates an empty value. Updates, revocations, and deletions require
`ExpectedRevision`. `ActiveVersion` selects an existing version; omit the value
when selecting one. A nil revoke version revokes the entire secret irreversibly.

Mutations generate a request ID when none is supplied. For retries after an
uncertain result, supply and reuse the same request ID and identical arguments.
The SDK does not retry mutations automatically. `WorkspaceSecretError.Code`
preserves the RPC status, including revision conflicts.

Managed runtimes reference workspace secret names, without accepting their values:

```go
runtime := sandbox.NewTemplateSpec().Start("npm start", sandbox.StartOptions{
    SecretEnv: map[string]string{"API_TOKEN": "app-token"},
})
session, err := client.Create(ctx,
    sandbox.WithImage("team/base:v1"),
    sandbox.WithDirectRuntime(runtime),
    sandbox.WithSecretOverrides(map[string]string{"app-token": "development-token"}),
)
```

`StartArgs` and `ProcessCompose` also support `SecretEnv` in stored templates.
Names resolve in the launching workspace. `WithDirectRuntime` accepts a
runtime-only spec and starts at boot; use Create options for image and resources.
Secret targets cannot also appear in ordinary runtime/session env. Session and
snapshot metadata expose the read-only `HasRuntimeSecrets` marker.

## Runtime secret files

Put `secrets://API_TOKEN` in any text file, regardless of filename or extension. Built templates capture unresolved source text from the guest image; direct launches supply content read on the caller. Values resolve from the launching workspace before managed startup.

```go
// Built template: source is already inside the image.
spec := sandbox.NewTemplateSpec().Start("python3 /app/server.py", sandbox.StartOptions{
    SecretFiles: []*sandbox.RuntimeSecretFile{{
        Path: "/app/.env",
        Format: &sandbox.RuntimeSecretFileSource{Source: "/app/env.tpl-sandbox"},
    }},
})

// Direct launch: read on the caller and upload unresolved text.
content, err := os.ReadFile("env.tpl-sandbox")
if err != nil { return err }
session, err := client.Create(ctx,
    sandbox.WithImage("my-template:latest"),
    sandbox.WithSecretFiles(&sandbox.RuntimeSecretFile{
        Path: "/app/.env",
        Format: &sandbox.RuntimeSecretFileContent{Content: string(content)},
    }),
    sandbox.WithSecretOverrides(map[string]string{"API_TOKEN": "STAGING_API_TOKEN"}),
)
```

Rendering performs one literal pass, without YAML/JSON/dotenv escaping, environment expansion, or recursive substitution. A backslash before a marker escapes it. Authors must ensure the actual consumer accepts the result; do not shell-source rendered secret text. Use a raw reference for exact-byte credentials, including binary values.

Allowed destinations are under `/home/tenki/`, `/workspace/`, or `/app/`. Files are private, owned by tenki, and replaced atomically. Duplicate destinations, unsafe paths, missing references, and injection-only plaintext delivery fail startup. Limits: 64 KiB per secret, 256 KiB per source/output file, 1 MiB total source/output, 32 files, and 64 references across environment and files.

Guest values remain frozen across retries, restart, and ordinary resume; replacement Sessions adopt updates. Escape a file marker as `\secrets://NAME` to preserve `secrets://NAME` for separately authorized outbound injection; the marker grants no authority by itself. See [the file delivery contract](../../../docs/sandbox-secret-files.md) for lifecycle and path details.
