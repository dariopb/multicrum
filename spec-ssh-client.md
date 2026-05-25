# SSH Client Feature Specification

## Goal

Add a reusable SSH client package under top-level `ssh_client/` and expose it through the existing multiplexer as a future `--ssh` command option. The feature should let users open a session backed by a remote SSH connection instead of a local PTY command.

The implementation must not reimplement SSH protocol handling from scratch. It should compose established Go SSH libraries and only add project-specific parsing, option resolution, and session integration.

## Target User Experience

Initial CLI shape:

```bash
multicrum --ssh user@host
multicrum --ssh host
multicrum --ssh user@host:2222
multicrum --ssh user@host --ssh-key ~/.ssh/id_ed25519
multicrum --ssh user@host --ssh-passwd 'secret'
multicrum --ssh user@host --ssh-use-default-keys
multicrum --ssh user@host --cmd bash
```

Implemented new-session behavior reuses the same SSH client object/options by default, and the `Ctrl+Alt+T` modal can also create a one-off typed local command or one-off remote SSH session with its own target, password/key, and optional remote command.

## Requirements

### Address parsing

Accept common Linux/OpenSSH target forms:

- `host`
- `user@host`
- `host:port`
- `user@host:port`
- IPv6 host forms should be supported if practical through `net.SplitHostPort`, e.g. `[2001:db8::1]:2222`.

Resolution rules:

- If user is omitted, use the current local username or `User` from SSH config.
- If port is omitted, use port `22` or `Port` from SSH config.
- Host aliases from `~/.ssh/config` should resolve `HostName`, `User`, `Port`, and `IdentityFile`.

### Authentication options

Support these authentication sources:

1. Explicit private key file, equivalent to OpenSSH `-i`.
2. Explicit password, via a flag such as `--ssh-passwd`.
3. Default keys in `~/.ssh` when `--ssh-use-default-keys` is set.
4. SSH agent when available, especially through `SSH_AUTH_SOCK`.
5. Identity files from `~/.ssh/config`.

Authentication precedence is explicit:

- An explicit key (`-i` / `--ssh-key` / `Options.IdentityFile`) uses only that key and ignores config/default identities.
- An explicit password (`--ssh-passwd` / `Options.Password`) uses only password and keyboard-interactive password auth and ignores config/default identities.
- SSH config identities, default keys, and agent auth are only considered when no explicit key/password is supplied.

Default key discovery should try standard OpenSSH names in a sensible order:

```text
~/.ssh/id_ed25519
~/.ssh/id_ecdsa
~/.ssh/id_rsa
~/.ssh/id_dsa
```

Passphrase-protected keys should be supported later if needed, but the first implementation may return a clear error when a key requires a passphrase and no passphrase option exists.

### Host key verification

Do not use `ssh.InsecureIgnoreHostKey()` except behind an explicit developer/testing option.

Use standard known-hosts behavior:

- Read `~/.ssh/known_hosts` by default.
- Support `/etc/ssh/ssh_known_hosts` where feasible.
- Fail safely on changed host keys.
- Return a clear error for unknown hosts, with a later option to trust-on-first-use if desired.

### Session behavior

The SSH client must be usable as a session backend, similar to the existing local PTY/ConPTY backends.

Needed operations:

- Open an interactive SSH session.
- Request a PTY on the remote side.
- Forward stdin bytes from the TUI/web UI to the SSH session.
- Forward remote stdout/stderr bytes into `session.VTScreen`.
- Propagate terminal resize events with SSH `WindowChange`.
- Close the SSH session cleanly.
- Surface remote command exit as the same exit modal flow used by local sessions.

The initial remote command should default to the remote login shell if no command is supplied, or use `--cmd` when provided.

SSH remote sessions support OpenSSH-style escapes at line start: `~.` disconnects, and `~~` sends a literal `~`.

## Current Implementation Status

Implemented in `ssh_client/`:

- `config.go`, `parse.go`, `auth.go`, `client.go`, and `session.go`.
- Parser and escape-sequence tests in `ssh_client/*_test.go`.
- `session.Session` can start local PTY/ConPTY or SSH remote PTY depending on whether a `*ssh_client.Client` is attached.
- `SessionManager.NewWithSSH` supports one-off local/remote sessions and rolls back half-created sessions on start failure.
- `main.go` exposes `--ssh`, `-i/--ssh-key`, `--ssh-passwd`, `--ssh-use-default-keys`, `--ssh-agent`, `--ssh-known-hosts`, and `--ssh-insecure-ignore-host-key`.
- `ui.NewModelWithSSH` passes the default SSH client factory into `SessionManager`.
- `Ctrl+Alt+T` opens `modeNewSession`, where Enter keeps the old/default behavior and the user can choose a typed local command or a remote SSH target/password/key/command. Errors remain inline in the modal and wrap across at least four lines.
- Mouse selection/copy is implemented through `ui/selection.go` and `ui/clipboard.go`; `VTScreen.BufferLines()` is used to preserve soft-wrap semantics.

## Proposed Package Layout

```text
ssh_client/
├── config.go      # Options, resolved config, defaults
├── parse.go       # user@host[:port] parsing
├── auth.go        # auth method construction: key, password, agent, defaults
├── knownhosts.go  # known_hosts loading and verification policy
├── client.go      # high-level Client wrapper
└── session.go     # interactive PTY session adapter implementing Read/Write/Resize/Close/Done
```

Public API sketch:

```go
package ssh_client

type Options struct {
    Target         string
    User           string
    Host           string
    Port           string
    IdentityFile   string
    Password       string
    UseDefaultKeys bool
    UseAgent       bool
    Command        []string
}

type ResolvedConfig struct {
    User          string
    Host          string
    Port          string
    IdentityFiles []string
    AuthMethods   []ssh.AuthMethod
    HostKey       ssh.HostKeyCallback
}

type Client struct {
    cfg ResolvedConfig
}

func Resolve(opts Options) (ResolvedConfig, error)
func New(opts Options) (*Client, error)
func (c *Client) Start(cols, rows int) (*RemoteSession, error)
```

`RemoteSession` should expose the same shape as the existing console implementations:

```go
type RemoteSession struct {}

func (s *RemoteSession) Read(p []byte) (int, error)
func (s *RemoteSession) Write(p []byte) (int, error)
func (s *RemoteSession) Resize(cols, rows int) error
func (s *RemoteSession) Close() error
func (s *RemoteSession) Done() <-chan error
```

This keeps integration with `session.Session` straightforward: add an SSH-backed start path that creates a `RemoteSession` instead of a local `console.UnixConsole` / `console.WinConsole`.

## Library Research

### `golang.org/x/crypto/ssh`

Official Go SSH implementation.

Capabilities:

- SSH client protocol implementation.
- Password auth.
- Public key auth.
- Keyboard-interactive auth.
- Agent integration through `golang.org/x/crypto/ssh/agent`.
- Interactive sessions, PTY requests, shell/command execution, and window-change requests.

Limitations:

- Low-level API.
- Does not parse `~/.ssh/config`.
- Does not discover default keys automatically.
- Does not parse OpenSSH-style targets.
- Requires explicit host key callback setup.

Use as the protocol foundation.

### `github.com/melbahja/goph`

Higher-level SSH client wrapper around `x/crypto/ssh`.

Capabilities:

- Simple client creation.
- Password authentication.
- Private key authentication.
- Private key with passphrase.
- SSH agent support.
- Known-hosts helpers.
- Command execution and SFTP helpers.

Limitations:

- Does not fully emulate OpenSSH CLI parsing.
- Does not automatically resolve `~/.ssh/config` host aliases.
- Does not automatically discover default identity files unless wrapped.

Recommendation: consider for command/SFTP helpers, but evaluate whether its interactive PTY API is flexible enough for this app's byte-stream session backend. If not, use `x/crypto/ssh` directly for the interactive session and borrow only design ideas.

### `github.com/kevinburke/ssh_config`

Parser for OpenSSH config files.

Capabilities:

- Reads `~/.ssh/config` and `/etc/ssh/ssh_config` style files.
- Resolves `Host` patterns.
- Reads `HostName`, `User`, `Port`, `IdentityFile`, and other directives.

Limitations:

- Config parser only.
- No connection/auth/session handling.

Recommended for config resolution.

### `github.com/skeema/knownhosts`

Known-hosts helper around `x/crypto/ssh/knownhosts`.

Capabilities:

- Reads known-hosts files.
- Host key verification.
- Handles multiple host key algorithms better than the raw callback.
- Can identify unknown hosts vs changed host keys.

Limitations:

- Host verification only.
- No authentication/session logic.

Recommended for secure known-hosts handling.

### `golang.org/x/crypto/ssh/agent`

SSH agent protocol package.

Capabilities:

- Connect to `SSH_AUTH_SOCK`.
- Provide `ssh.PublicKeysCallback(agentClient.Signers)` auth.

Recommended for agent authentication.

## Recommended Dependency Strategy

Use `x/crypto/ssh` directly for the actual interactive PTY session. It exposes the needed primitives clearly:

- `ssh.Dial`
- `client.NewSession`
- `session.RequestPty`
- `session.Shell` or `session.Start`
- `session.StdinPipe`
- `session.StdoutPipe`
- `session.StderrPipe`
- `session.WindowChange`
- `session.Wait`

Add focused helper libraries instead of a large abstraction:

```text
golang.org/x/crypto/ssh
x/crypto/ssh/agent
github.com/kevinburke/ssh_config
github.com/skeema/knownhosts
```

`github.com/melbahja/goph` remains a useful reference and possible wrapper, but the first implementation should not depend on it unless it cleanly supports long-lived interactive PTY byte streams with resize and lifecycle hooks.

## CLI Integration Plan

Add flags in `main.go`:

| Flag | Purpose |
|---|---|
| `--ssh` | SSH target, e.g. `user@host`, enables SSH-backed sessions |
| `--ssh-key`, `-i` | Explicit identity file |
| `--ssh-passwd` | Explicit password authentication |
| `--ssh-use-default-keys` | Try standard keys in `~/.ssh` |
| `--ssh-agent` | Use SSH agent, default true when `SSH_AUTH_SOCK` exists |
| `--ssh-known-hosts` | Optional known_hosts path override |
| `--ssh-insecure-ignore-host-key` | Developer-only escape hatch, default false |

When `--ssh` is empty, keep current local `--cmd` behavior unchanged.

When `--ssh` is present:

1. Resolve the SSH config.
2. Build an SSH client factory.
3. Pass that factory into `ui.NewModel` / `session.SessionManager`.
4. New sessions use the SSH factory instead of local PTY startup.
5. `--cmd` becomes the remote command/shell override.

## Error Handling

Return actionable errors:

- Invalid target syntax.
- Missing username after config/default resolution.
- No auth methods available.
- Identity file not found or unreadable.
- Private key requires passphrase.
- Unknown host key.
- Changed host key.
- Authentication failed.
- Remote PTY request failed.
- Remote command/session exited.

Do not log passwords or private key material.

## Security Requirements

- Never default to insecure host key verification.
- Never echo or persist `--ssh-passwd`.
- Prefer agent/key auth over password when available.
- Avoid storing passwords in long-lived structs beyond connection setup if possible.
- Keep host key errors explicit and visible.

## Implementation Phases

### Phase 1: Standalone package

- Create `ssh_client/` package.
- Implement target parsing.
- Implement config resolution with explicit options and default keys.
- Implement auth methods for password, explicit key, default keys, and agent.
- Implement known_hosts verification.
- Add unit tests for parser/config/auth path selection.

### Phase 2: Interactive session backend

- Implement `RemoteSession` with `Read`, `Write`, `Resize`, `Close`, `Done`.
- Verify with a small smoke test that remote shell output passes through vt10x.
- Ensure resize propagation works.

### Phase 3: CLI and multiplexer integration

- Add `--ssh` flags.
- Thread SSH options into `ui.NewModel` and `session.SessionManager`.
- Start SSH-backed sessions when enabled.
- Keep local sessions as default.

### Phase 4: New-session UX

- Reuse SSH factory when pressing new-session shortcut.
- Later add UI support for opening a different SSH target per new tab.
- Later add saved connection profiles.

## Acceptance Criteria

- `multicrum --ssh user@host --ssh-use-default-keys` opens an interactive remote shell.
- `multicrum --ssh user@host -i ~/.ssh/id_ed25519` authenticates with the explicit key.
- `multicrum --ssh user@host --ssh-passwd ...` authenticates with password where allowed by the server.
- `~/.ssh/config` aliases resolve user, host, port, and identity file.
- Unknown/changed host keys fail safely with clear errors.
- Remote terminal resizes when the local TUI is resized.
- Creating a new session opens another SSH-backed remote session with the same resolved options.
