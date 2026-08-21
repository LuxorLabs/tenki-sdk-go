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
- `(*Session).Close(ctx) error`

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
- `(*HostPortTunnel).Terminated() <-chan HostPortTunnelTermination` / `.Close()`

Publish a stable, browser-openable URL bound to a session port:

- `(*Client).CreatePreviewURL(ctx, slug string, sessionID *string, port *int32) (*PreviewURL, error)`
- `(*Client).BindPreviewURL(ctx, previewURLID, sessionID string, port int32)` / `UnbindPreviewURL`
- `(*Client).ListPreviewURLs(ctx)` / `GetPreviewURL` / `DeletePreviewURL`

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

### Create options

- `WithName(string)`
- `WithAllowInbound(bool)` default: `true` (sent explicitly on every Create)
- `WithAllowOutbound(bool)` default: `true` (sent explicitly on every Create)
  - both are create-time settings and cannot be changed on an existing session;
    `session.InboundEnabled` / `session.OutboundEnabled` report what a session was created with
- `WithEnvs(map[string]string)` session-scoped env defaults
- `WithMaxDuration(time.Duration)`
- `WithCPUCores(int32)` default: `2`
- `WithMemoryMB(int32)` default: `4096`
- `WithMetadata(map[string]string)`
- `WithSSHKeys([]string)`
- `WithVolume(volumeID, mountPath, ...VolumeOption)`
- `WithSnapshot(snapshotID)` / `WithImage(image)` (mutually exclusive)
- `WithCloneRepo(repoURL)` / `WithGitHubToken(token)`

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
	tenkisandbox.WithArgs("-lc", "python3 -m http.server 3000 >/tmp/http.log 2>&1 &"))
```

Redirecting only stdout is not enough — stderr still holds the stream open — and
`nohup` does not help, because it blocks `SIGHUP` rather than stream inheritance.
This is standard POSIX behavior, the same as Go's own `exec.Cmd` with piped output.

To keep hold of a service instead, use `session.Command(...).Stream(ctx)` and read
from the handle; for services you always want running, start them from a template
start command.

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
- Create/list ownership is derived from auth context.
- Volume size: 1 MiB - 100 GiB.
- Session CPU: 1-16 cores. Memory: 128-65536 MB, aligned to 2 MiB.
