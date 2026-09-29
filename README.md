# multicrum

`multicrum` is a Go terminal multiplexer and reusable session library for running multiple persistent local or SSH-backed terminal sessions.

It provides:

- A local Bubble Tea TUI.
- An optional browser UI served over WebSocket and rendered with xterm.js.
- Local PTY/ConPTY sessions.
- Remote SSH PTY sessions through the reusable `ssh_client` package.
- A session manager that can be embedded by other Go applications.

The child program or remote shell is treated as a black-box terminal: the app forwards terminal bytes and renders output through a virtual terminal screen model.

Applications embedding multicrum should use `pkg/app.Start`. It owns terminal
size discovery, layout-before-command ordering, Bubble Tea initialization,
local attach, control service startup, and cleanup:

```go
owner, err := app.Start(ctx, app.Options{
    Server:           "my-app",
    Command:          []string{"copilot", "--no-mouse"},
    ConnectionLayout: "left",
    Terminal:         os.Stdout,
})
if err != nil {
    return err
}
defer owner.Close()
return owner.Attach(os.Stdin, os.Stdout)
```

## Features

- Long-running named local servers: the first `multicrum --server NAME` auto-starts a detached owner daemon, and later processes attach to it over a Unix socket.
- Multiple connections/workspaces per server, each with its own tabs / sessions.
- Configurable local connection switcher: the default bottom pills or a persisted, drag-resizable left rail.
- Local commands via PTY on Unix and ConPTY on Windows.
- Named server attach/daemon lifecycle on Unix and Windows.
- SSH sessions with `user@host[:port]`, explicit port, password, explicit key, SSH agent, `~/.ssh/config`, and known-host verification.
- `Ctrl+Alt+T` new-session modal:
  - default: same current/default session behavior,
  - typed local command,
  - one-off remote SSH session with target/port/password/key/remote command.
- Session rename, kill, respawn/remove on exit, and filtered session picker.
- Layout save/load for local sessions and SSH sessions, including remote commands and the live working directory of interactive local shells.
- Optional xterm.js browser UI over WebSocket.
- Authenticated `multicrum-control` automation over a separate per-user socket,
  with stable IDs, raw PTY subscriptions, snapshots, waits, and scoped delegation.
- Mouse selection/copy mode that preserves logical lines across scrollback wraps, terminal-grid wraps, and narrower live viewport wraps. Right-click copies the current selection, including through tmux/byobu attach clients.
- Right-click a session or connection tab for a modal-styled context menu with focus, rename, move, and remove actions.
- In the left layout, clicking the `Multicrum` title opens global actions including Help, session/connection controls, mouse mode, save, client-only Detach, and server-wide Quit.
- Centered dialogs support direct button/row clicks; drag session tabs/rows or connection pills/rail entries/rows to reorder them.
- Exited-session prompts cannot be accidentally dismissed; mouse and keyboard can still switch away, and a focused faulted tab remains visibly selected with its `✗`.
- Terminal-accurate resize: output reflows (re-wraps) when the pane is made narrower or wider — characters cropped by a shrink are restored on a later widen — and scrollback stays consistent while scrolling with the mouse wheel or `Ctrl+PgUp`/`Ctrl+PgDn`. Prompt redraws and progress bars overwrite in place instead of leaving duplicate lines.

## Build

Go 1.26 or newer is required (including by the current goreverselb dependency)
and is expected to be available in `PATH`.

```bash
go build -v ./cmd/multicrum/
```

Run tests:

```bash
go test ./...
```

## Run as an app

Local shell on the default named server:

```bash
./multicrum --cmd bash
```

Attach to or create a separate named server:

```bash
./multicrum --server work --cmd bash
./multicrum --server work
```

If no live `work` server exists, the first command starts a detached owner daemon, waits for its local endpoint, then attaches the current terminal as a client. Closing or detaching that client does not stop the sessions; run `./multicrum --server work` later to reconnect.

New attach clients receive the current frame, cursor position/style, alternate-screen state, and mouse mode immediately; no keypress is needed to trigger the first render.

Lifecycle commands:

```bash
./multicrum list
./multicrum status --server work
./multicrum stop --server work
```

`list` and `status` show the server PID, local endpoint path, and startup settings such as command, config path, WebSocket address, token presence, and SSH options. Token values are never printed; only `token=set` is shown. Unix uses Unix sockets; Windows stores a per-user loopback TCP address file under the multicrum runtime directory.

On Linux, saving a layout records the live `cwd` for local interactive shells; configured `cwd` values also round-trip on other platforms. Restored shells try to start there and silently fall back to the owner working directory if the saved path is missing or inaccessible. SSH sessions, non-shell commands, and shell one-shots such as `bash -c` do not save `cwd`.

Local command:

```bash
./multicrum --cmd "python3"
```

SSH with password:

```bash
./multicrum --ssh dario@localhost --ssh-passwd 'secret'
```

SSH with explicit key:

```bash
./multicrum --ssh user@example.com -i ~/.ssh/id_ed25519
```

SSH using standard keys from `~/.ssh`:

```bash
./multicrum --ssh user@example.com --ssh-use-default-keys
```

SSH using `~/.ssh/config` host aliases:

```bash
./multicrum --ssh my-server
```

Run a remote command instead of the remote login shell:

```bash
./multicrum --ssh user@example.com --cmd "bash -l"
```

Start local TUI plus browser UI:

```bash
./multicrum --cmd bash --ws :9999 --token mytoken
```

Then open:

```text
http://localhost:9999/?token=mytoken
```

On Unix, owner diagnostics live under `${TMPDIR:-/tmp}/multicrum-$UID/`:

| File | Contents |
|---|---|
| `<server>.log` | Lifecycle messages, panic stacks, and automatic recent-event dumps when the owner UI fails or a session reader panics. |
| `<server>.trace.log` | Latest 256 metadata events, atomically checkpointed every five seconds and at shutdown/failure. |
| `<server>.trace.log.previous` | Previous run's checkpoint, retained when a new owner starts. |

The trace includes build/runtime identifiers, attach counts, resize sources,
old/new screen dimensions, session IDs/generations/PIDs, alternate-screen
transitions, replay byte counts/timing, session output ending, respawns, and
shutdown signals. It never records terminal contents, keystrokes, command
arguments, environment variables, or authentication tokens. Individual events
are capped at 768 bytes, keeping the checkpoint bounded.

After an unexpected failure, preserve all three files. SIGKILL (including an
OOM kill) cannot produce a final dump, so its checkpoint may be missing the
last five seconds; check the system journal when the log ends without an
`owner stopped` entry. These diagnostics require a newly started owner using
the updated binary.

## Reverse load balancer

The browser endpoint can be published through
[goreverselb](https://github.com/dariopb/goreverselb), using its
`NewMuxTunnelClient` directly, as in pinger. The API endpoint is a **TLS tunnel
address (`host:port`), not an HTTP URL**. HTTP and WebSocket traffic share the
outbound tunnel to multicrum's local web listener.

Set `MULTICRUM_LB_TOKEN` to the LB registration token and
`MULTICRUM_WEB_TOKEN` to a separate browser authentication token, then start a
new owner:

```bash
./multicrum --server work --config work.yaml \
  --ws 127.0.0.1:9998 \
  --lb-api ingress.example.com:9999 \
  --lb-service multicrum-myhost-work
```

The public frontend port defaults to automatic allocation (`--lb-port auto`
or `0`). The owner uses goreverselb's `WaitReady` to wait for an accepted
registration and obtain the confirmed frontend port/address. Only then does
multicrum save the requested LB settings and web listen address to YAML,
preserving existing layout and session settings. The configured `auto`/fixed
port remains unchanged; the assigned port is reported separately.
Startup reloads settings; explicit flags override environment
variables, which override saved values.
An empty API endpoint disables LB integration (`--lb-api ""` overrides YAML).
The LB is only started when the web endpoint is enabled; `--ws ""` disables
both. Restart an existing owner to apply startup changes.

When starting a new owner, the launching terminal waits for registration,
prints the assigned frontend address/port, and prompts for a key before
attaching the TUI. **Only that terminal pauses**: the owner, sessions, and web
UI run once registration succeeds. The keypress is consumed, not sent to the
session. Existing-owner attaches do not pause; noninteractive stdin also skips
the keypress without consuming input. Ctrl+C cancels this attach attempt,
not the owner; use `multicrum stop --server NAME` to stop the owner as well.
Bindings-only publication is reported explicitly as having no dedicated port.
`status`/`list` show the startup-confirmed port (not continuous tunnel health).

Example saved settings:

```yaml
logLevel: info
web:
  address: 127.0.0.1:9998
  tokenRequired: true
  reverseLB:
    apiEndpoint: ingress.example.com:9999
    frontendPort: 0
    serviceName: multicrum-myhost-work
```

Tokens are **never saved to YAML**. Restart with the same environment variables
or `--token` / `--lb-token`. A saved authenticated web endpoint requires its
token on restart. The LB token authenticates registration, not browser users.
Keep service names unique to avoid load balancing between separate owners.

`--lb-instance-name` (alias `--lb-instance`, environment
`MULTICRUM_LB_INSTANCE_NAME`) is persisted as `web.reverseLB.instanceName`.
Multicrum passes `serviceName:instanceName` to the upstream client, just as
goreverselb's CLI does. Leave it empty to register without an instance.

`--lb-wrap-tls` enables public HTTPS termination at the LB and is persisted as
`web.reverseLB.wrapTLS: true`. It defaults to false; `--lb-wrap-tls=false`
overrides a saved true value. For DNS-based HTTPS, goreverselb selects the
backend by SNI: use `--lb-service multicrum-myhost-work
--lb-instance-name ingress.example.com`, matching the browser hostname.
Use a bare service name when specifying the instance separately. Existing
`service:instance` names remain supported when the separate instance is empty.
The public certificate is supplied by the LB.

`--lb-wrap-ssh` passes SSH wrapping directly to the LB and is persisted as
`web.reverseLB.wrapSSH: true`. It defaults to false; `--lb-wrap-ssh=false`
overrides a saved true value. An SSH-wrapped frontend requires SSH access
rather than a direct browser HTTP connection.

Use the application-wide `--log-level debug` (alias `--loglevel`, environment
`MULTICRUM_LOG_LEVEL=debug`) for verbose diagnostics. Logging is initialized
before command execution and owner startup, including when web/LB is disabled.
It also sets the upstream client's shared Logrus level. The default is `info`;
the setting is saved as top-level `logLevel` and retained by layout saves.
Use `--log-level info` to turn debug logging back off.
The old `--lb-log-level` alias, `MULTICRUM_LB_LOG_LEVEL` fallback environment
variable, and `web.reverseLB.logLevel` YAML fallback remain accepted.
The top-level setting takes precedence over the legacy YAML setting.

Owner debug output goes to `<server>.log`, including an
`application debug logging enabled` message at startup.
**Attaching does not reconfigure an existing owner.** Restart that owner to
change its logging; changing flags on an attach client only affects that client.
Existing lifecycle/diagnostic messages remain available at all log levels.

The library constructor remains asynchronous; multicrum now waits on its
readiness API rather than treating constructor success as registration.
Initial network/authentication failures are logged and retried, with startup
waiting until registration succeeds or the owner is stopped. Stopping an
owner during this wait cancels it and does not save unconfirmed settings.
After startup, reconnection remains the library's responsibility.
Owner shutdown closes the library client. The legacy constructor still skips
tunnel certificate verification; use a trusted LB.

## CLI flags

Every public configuration option has an environment equivalent, shown in
`--help` (or `call --help`). Names use `MULTICRUM_` plus the canonical flag
name in uppercase with hyphens replaced by underscores, such as
`MULTICRUM_CONFIG`, `MULTICRUM_WS`, `MULTICRUM_LB_API_ENDPOINT`,
`MULTICRUM_LB_FRONTEND_PORT`, `MULTICRUM_LB_SERVICE_NAME`, and
`MULTICRUM_LB_WRAP_TLS`. The existing `--token` variable remains
`MULTICRUM_WEB_TOKEN`. The `call` options use `MULTICRUM_CALL_METHOD`,
`MULTICRUM_CALL_PARAMS`, and `MULTICRUM_CALL_TIMEOUT`.
Boolean variables accept `true`/`false`; timeouts accept durations such as
`30s`. Explicit CLI values take precedence over environment values, then
YAML where supported, then defaults. Internal daemon flags have no environment
equivalents.

| Flag | Purpose |
|---|---|
| `--cmd` | Local command, or remote command when `--ssh` is set. Default: `bash`. |
| `--log-level`, `--loglevel` | Application and library logging level, e.g. `info`, `debug`, or `trace`. Default: `info`; persisted as top-level `logLevel`. |
| `--server`, `--srv`, `-S` | Named local long-running server to attach/create. Default: `default`. |
| `list`, `ls` | List local servers, status, PID, socket path, and startup settings. |
| `status` | Show status and startup settings for one server. |
| `stop` | Stop one server and disconnect its sessions/clients. |
| `--ssh` | SSH target: `host`, `user@host`, `host:port`, `user@host:port`. |
| `-i`, `--ssh-key` | Explicit identity file. Overrides config/default identities. |
| `--ssh-passwd` | Explicit password / keyboard-interactive password. Overrides config/default identities. |
| `--ssh-use-default-keys` | Try `~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`, `id_dsa`. |
| `--ssh-agent` | Use `SSH_AUTH_SOCK` when no explicit password/key is supplied. Default: true. |
| `--ssh-known-hosts` | Override known_hosts path. |
| `--ssh-insecure-ignore-host-key` | Disable host key verification. Unsafe; testing only. |
| `--ws` | WebSocket/xterm.js listen address, overriding YAML; e.g. `:9999`, or `127.0.0.1:0` for an automatic local port. Empty disables web. |
| `--token` | Optional token for `/ws?token=...`; also reads `MULTICRUM_WEB_TOKEN`. Never saved. |
| `--lb-api-endpoint`, `--lb-api` | goreverselb TLS tunnel address (`host:port`). Empty disables the integration. |
| `--lb-frontend-port`, `--lb-port` | Public port; default `auto` (`0`). The requested setting is saved. |
| `--lb-service-name`, `--lb-service` | Unique service name; default `multicrum-HOST-SERVER`. |
| `--lb-instance-name`, `--lb-instance` | Optional instance name; persisted as `instanceName`. |
| `--lb-wrap-tls` | Enable TLS termination on the LB frontend. Default: false; persisted as `wrapTLS`. |
| `--lb-wrap-ssh` | Enable SSH wrapping on the LB frontend. Default: false; persisted as `wrapSSH`. |
| `--lb-token` | Registration token, also read from `MULTICRUM_LB_TOKEN`; never saved to YAML. |
| `--control-token` | Full-control automation token. When omitted, the owner creates a random mode-0600 token file beside the control socket. |

## Automation control

Every owner exposes a separate `multicrum-control` v1 endpoint at:

```text
/tmp/multicrum-$UID/<server>.control.sock
```

Full-control clients owned by the same user discover the random credential in
the adjacent mode-0600 token file. Child agents receive short-lived scoped
credentials through `capability.delegate`; tokens are never placed in process
arguments, saved layouts, or UI metadata. The Go SDK is in `pkg/control`.

The sample controller starts a visible `copilot --no-mouse`, delegates a
restricted capability, points it at the bundled multicrum-control skill, and
attaches the terminal to the ordinary multicrum TUI:

```bash
go run ./samples/agents --server default --cwd "$PWD"
```

For a directory Copilot has not previously trusted, review it and explicitly
approve one-session folder trust:

```bash
go run ./samples/agents --server default --connection agent \
  --cwd /tmp/temp --trust-cwd
```

If the named server is not running, the sample hosts an owner directly through
the multicrum Go packages. No separate `multicrum` executable is required.
Other terminals can attach while the sample is running; an owner embedded by
the sample and its sessions are closed when the sample exits. The embedded TUI
starts with the connection switcher on the left.

The controlling Copilot can create additional named agent or command sessions
that immediately appear as ordinary tabs. Type requests directly into the
focused Copilot tab. Another terminal can attach at any time:

The sample capability is intentionally scoped to the selected connection.
Child Copilot sessions use `copilot --no-mouse` in that connection; they cannot
create another connection or recursively start the `agents` host application.

```bash
multicrum --server default
```

Inside a delegated session, the multicrum CLI uses the injected endpoint and
token automatically:

```bash
multicrum call \
  --method connection.list \
  --params '{}'
```

See `spec-control-protocol.md` for the wire protocol and
`skills/multicrum-control/SKILL.md` for the agent workflow.

## TUI shortcuts

| Shortcut | Action |
|---|---|
| `Alt+`` | Show / close help. |
| `Ctrl+Alt+T` | Open new-session modal. |
| `Ctrl+Alt+W` | Kill focused session, except final session. |
| `Ctrl+Alt+R` | Open the sessions dialog on the active session; press `R` to rename. |
| `Ctrl+Alt+S` | Open sessions dialog (focus, create, rename, move/reorder, filter, remove). |
| `Ctrl+Alt+O` | Open connections modal (focus, create, rename, move/reorder, filter, remove; `L` toggles bottom/left switcher placement). |
| `Ctrl+Alt+E` | Open the connections modal on the active connection; press `R` to rename. |
| `Ctrl+Alt+C` | Quick-create a new connection/workspace. |
| `Ctrl+Alt+[` / `Ctrl+Alt+]` | Switch connections/workspaces. |
| `Ctrl+Alt+Left` / `Ctrl+Alt+Right` | Switch sessions inside the active connection. |
| `Alt+1..9` | Jump to session N in the active connection. |
| `Ctrl+Alt+M` | Toggle mouse mode: selection/copy vs app forwarding. |
| `Ctrl+Alt+Q` | Quit the owner TUI/server; in an attached client, detach that client. |

In the left layout, click the `Multicrum` title for the global action menu. **Detach** disconnects only the client that opened the menu; **Quit** opens the server-wide shutdown confirmation.

In both the TUI and browser, scrollback navigation supports `Ctrl+PgUp` / `Ctrl+PgDown`, `Ctrl+Up` / `Ctrl+Down`, and `Ctrl+Home` / `Ctrl+End`. While scrolled up, `/` searches, `:` jumps to a line, and `n` / `N` moves through matches. The search prompt appears in the **top bar above the terminal pane**, immediately left of the current-line/total-lines counter. Enter commits and Esc cancels, restoring the normal top bar.

In select mouse mode, dragging to or beyond the terminal edge scrolls and extends the selection; the mouse wheel also extends an active drag. Hold `Ctrl+Alt` when starting a drag for rectangular selection. Block copies trim trailing spaces on each row but retain line breaks and blank rows; ordinary selection copying is unchanged. Copy-on-release follows the shared setting, with right-click copying available for retained selections.

In SSH sessions, OpenSSH-style escapes are supported at line start:

- `~.` disconnects the SSH session.
- `~~` sends a literal `~`.

## SSH behavior

The SSH client lives in `pkg/ssh_client/` and is usable independently from the TUI.

Resolution behavior:

- Targets accept OpenSSH-like forms: `host`, `user@host`, `host:port`, `user@host:port`, and bracketed IPv6.
- `~/.ssh/config` and `/etc/ssh/ssh_config` can provide `HostName`, `User`, `Port`, and `IdentityFile`.
- Known hosts are verified through `github.com/skeema/knownhosts` using `~/.ssh/known_hosts` and `/etc/ssh/ssh_known_hosts` by default.
- Host key verification is secure by default; `ssh.InsecureIgnoreHostKey()` is only used when explicitly requested.

Authentication precedence:

1. Explicit key (`-i`, `--ssh-key`, `Options.IdentityFile`) uses only that key.
2. Explicit password (`--ssh-passwd`, `Options.Password`) uses only password and keyboard-interactive password auth.
3. SSH config identities, default keys, and SSH agent are used only when no explicit key/password is supplied.

## Use as a Go library

The project exposes two useful layers:

1. `ssh_client`: reusable SSH target resolution and remote PTY sessions.
2. `session`: a multi-session manager that can create local sessions or SSH-backed sessions and feed output into your app.

### Bootstrap an app into a managed local session

The intended embedding shape is a **parent/child split**:

- The **parent process** owns `session.SessionManager` and creates sessions.
- The **child process** is your real application running inside a PTY session.
- The child handles terminal input/output normally through stdin/stdout/stderr.
- The parent does **not** manually read/write the child's business data.
- If the child needs the parent to create another session, it must use an **out-of-band control channel**.

Do not use the PTY stdout stream for control messages: stdout belongs to the terminal UI. Use a Unix-domain socket, named pipe, localhost socket, inherited file descriptor, or another IPC mechanism. The parent creates the control endpoint, passes its address to the child (usually via an environment variable), and the child sends structured control requests such as `new-local` or `new-ssh`.

> Current status: `session` and `ssh_client` provide the session backends. A reusable child→parent control protocol package is not yet implemented in this repository; embedders should provide this side-channel until one is added.

Example control message shape:

```json
{"action":"new-local","cmd":["bash"]}
{"action":"new-ssh","target":"user@example.com","port":"22","password":"secret","cmd":["bash","-l"]}
```

Single `urfave/cli` app with parent and `--child` modes:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "net"
    "os"

    "github.com/urfave/cli/v3"
    "multicrum/pkg/session"
    "multicrum/pkg/ssh_client"
)

type ControlMsg struct {
    Action   string   `json:"action"`
    Cmd      []string `json:"cmd,omitempty"`
    Target   string   `json:"target,omitempty"`
    Port     string   `json:"port,omitempty"`
    Password string   `json:"password,omitempty"`
    KeyFile  string   `json:"keyFile,omitempty"`
}

func main() {
    cmd := &cli.Command{
        Name:  "myapp",
        Usage: "example app that can bootstrap itself into managed sessions",
        Flags: []cli.Flag{
            &cli.BoolFlag{
                Name:  "child",
                Usage: "run as the real terminal app inside a managed PTY session",
            },
            &cli.StringFlag{
                Name:  "mux-control",
                Usage: "parent multiplexer control socket path",
            },
        },
        Action: run,
    }

    if err := cmd.Run(context.Background(), os.Args); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func run(ctx context.Context, c *cli.Command) error {
    if c.Bool("child") {
        return runChildApp(c.String("mux-control"))
    }
    return runParentMux(ctx)
}

func runParentMux(ctx context.Context) error {
    cols, rows := 120, 40

    manager := session.NewManager(
        cols,
        rows,
        func(msg session.OutputMsg) {
            // The library has already read child output from the PTY/SSH stream.
            // A real parent UI would append msg.Data to the pane for msg.Index.
        },
        func(msg session.ExitMsg) {
            // Mark the session as exited in the parent UI.
        },
    )

    sockPath := os.TempDir() + "/myapp-mux.sock"
    _ = os.Remove(sockPath)
    listener, err := net.Listen("unix", sockPath)
    if err != nil {
        return err
    }
    defer listener.Close()

    go serveControl(listener, manager)

    // Bootstrap the real app into a local PTY by running the same binary in
    // child mode. From this point on, the child handles stdin/stdout normally.
    childCmd := []string{os.Args[0], "--child", "--mux-control", sockPath}
    mainSession, err := manager.New(childCmd)
    if err != nil {
        return err
    }
    mainSession.SetTitle("main")

    // Parent now runs its own event loop/UI. It creates more sessions only when
    // child control messages arrive; it does not inject app data by hand.
    <-ctx.Done()
    return ctx.Err()
}

func runChildApp(controlSock string) error {
    // This is your real terminal app: Bubble Tea, readline, REPL, agent, etc.
    // It owns stdin/stdout/stderr normally because it is running inside the PTY.

    // Whenever the app needs another session, it sends a control message over
    // the side-channel instead of printing control data to stdout.
    requestNewSSHSession(controlSock)

    fmt.Println("real app running inside managed PTY")
    select {}
}

func serveControl(listener net.Listener, manager *session.SessionManager) {
    for {
        conn, err := listener.Accept()
        if err != nil {
            return
        }
        go func() {
            defer conn.Close()
            var msg ControlMsg
            if err := json.NewDecoder(conn).Decode(&msg); err != nil {
                return
            }
            switch msg.Action {
            case "new-local":
                if len(msg.Cmd) == 0 {
                    msg.Cmd = []string{"bash"}
                }
                sess, err := manager.NewWithSSH(msg.Cmd, nil)
                if err == nil {
                    sess.SetTitle("secondary")
                }
            case "new-ssh":
                sshClient, err := ssh_client.New(ssh_client.Options{
                    Target:       msg.Target,
                    Port:         msg.Port,
                    Password:     msg.Password,
                    IdentityFile: msg.KeyFile,
                    Command:      msg.Cmd,
                })
                if err != nil {
                    return
                }
                sess, err := manager.NewWithSSH(msg.Cmd, sshClient)
                if err == nil {
                    sess.SetTitle("secondary")
                }
            }
        }()
    }
}

func requestNewSSHSession(sockPath string) {
    if sockPath == "" {
        return
    }
    conn, err := net.Dial("unix", sockPath)
    if err != nil {
        return
    }
    defer conn.Close()

    _ = json.NewEncoder(conn).Encode(ControlMsg{
        Action: "new-ssh",
        Target: "user@example.com",
        Port:   "22",
        Cmd:    []string{"bash", "-l"},
    })
}
```

### Use only the SSH client layer

If you do not need the multiplexer manager, use `ssh_client.Client` directly:

```go
client, err := ssh_client.New(ssh_client.Options{
    Target:         "user@example.com",
    Port:           "22",
    Password:       "secret",
    UseAgent:       false,
    UseDefaultKeys: false,
})
if err != nil {
    panic(err)
}

remote, err := client.Start(120, 40)
if err != nil {
    panic(err)
}
defer remote.Close()

_, _ = remote.Write([]byte("uname -a\r"))
```

`RemoteSession` implements `io.ReadWriteCloser` and also exposes:

```go
Resize(cols, rows int) error
Done() <-chan struct{}
```

## Discovering the library shape

For Go-native discovery:

```bash
go doc multicrum/pkg/session
go doc multicrum/pkg/ssh_client
go doc -all multicrum/pkg/session
go doc -all multicrum/pkg/ssh_client
```

Compileable examples live in:

```text
examples/parent_child/  # parent/child app bootstrap with side-channel control
examples/ssh_client/    # direct ssh_client.RemoteSession usage
```

Run them with:

```bash
go run ./examples/parent_child
go run ./examples/ssh_client user@host
```

## Documentation

- `spec.md` records the current app architecture and behavior.
- `spec-ssh-client.md` records the SSH client design, library research, and implementation details.
- `pkg/session/doc.go` and `pkg/ssh_client/doc.go` provide GoDoc package overviews.
- `examples/` contains compileable copy-paste starting points for embedders and coding agents.
- `AGENTS.md` contains development notes and project-specific gotchas.
