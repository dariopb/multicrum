# Long-Running Named Server and Connections Specification

## Current Status

This feature is implemented as a first-pass shared-TUI server model.

`multicrum --server NAME` now means:

1. Try to attach this process to an existing local owner for `NAME` over a Unix domain socket.
2. If no live owner exists, spawn a detached owner daemon for `NAME`.
3. Wait for the daemon's socket to become ready.
4. Attach the current terminal as a client.

The visible terminal is therefore always an attach client for named servers. Closing or detaching that terminal does not kill the server sessions; reconnect later with the same `--server NAME`.

A named server owns an ordered set of logical connections. Each connection owns an ordered set of sessions.

```text
server name / Unix socket
└── connection / workspace
    └── session / tab
```

Important first-pass design choice: attached local TUI clients share the owner-rendered TUI, active connection, active session, modal state, and terminal size policy. This is intentionally not yet a per-client independent UI model.

## CLI and Startup Flow

Entry point: `cmd/multicrum/main.go`.

Supported flags relevant to this feature:

| Flag | Default | Purpose |
|---|---:|---|
| `--server`, `--srv`, `-S` | `default` | Named local server/socket namespace to attach/create. First instance auto-starts a detached owner daemon. |
| `list`, `ls` | n/a | List local servers with status, PID/socket path, and startup settings. |
| `status` | n/a | Show one server's status and startup settings. |
| `stop` | n/a | Stop one server and disconnect its sessions/clients. |
| `--config` | `multicrum.yaml` | YAML config tree loaded by the owner and saved with `Ctrl+Alt+P`. |
| `--cmd` | `bash` | Default command for sessions when config has none or when creating a default session. |
| `--ws` | empty | Optional WebSocket/xterm.js browser UI served by the owner. |
| `--token` | empty | Optional web token. |

Startup algorithm implemented:

1. Parse `serverName`, defaulting to `default`.
2. Resolve socket path with `localserver.SocketPath(serverName)`.
3. Call `localserver.TryAttach(socketPath, serverName, os.Stdin, os.Stdout)`.
4. If attach succeeds, this process becomes an attach client and exits when detached/disconnected.
5. If attach does not succeed, re-exec the current binary with hidden `--owner`, detached from the controlling terminal.
6. The parent waits until `localserver.ServerStatus` succeeds, then attaches as the first client.
7. The detached owner:
   - parses default command with `ui.ParseCmdLine`, falling back to `[]string{"bash"}`,
   - constructs `ui.NewModelWithSSH`,
   - sets server name and config path,
   - loads config with `config.Load` and calls `model.SetConfigConnections(cfg)`,
   - creates `ui.InputMux(nil)` so only attached-client frames feed Bubble Tea,
   - starts `localserver.ListenWithSettings`, recording startup settings for lifecycle commands,
   - writes Bubble Tea output to the local server owner writer and `ui.NewKeyboardStripWriter`,
   - optionally starts browser UI via `ui.StartWSTransport`,
   - runs Bubble Tea.

Lifecycle behavior:

- `list` / `ls` enumerates local socket files and probes each server.
- `status --server NAME` reports PID, socket path, and startup settings.
- `stop --server NAME` sends a `FrameControl` stop request and falls back to killing the recorded PID if graceful shutdown times out.
- `status` and `list` expose command/config/ws/SSH startup settings; token values are redacted as `token=set`.

## Local Server Package

Package: `pkg/localserver`.

Current files:

```text
pkg/localserver/
├── fanout.go          # TTY-preserving output mirror to attached clients
├── frame.go           # length-delimited frame codec
├── frame_test.go      # frame and sanitizer tests
├── path.go            # server-name sanitizer helper
├── protocol.go        # hello/resize structs + protocol constants
├── server_unix.go     # Unix socket owner and attach client
└── server_windows.go  # unsupported stubs on Windows
```

### Socket path

`SocketPath(server)` sanitizes the server display name for filesystem use and returns:

```text
$XDG_RUNTIME_DIR/multicrum/<safe-server>.sock
```

Fallback when `XDG_RUNTIME_DIR` is empty:

```text
/tmp/multicrum-$UID/multicrum/<safe-server>.sock
```

The parent directory is created with `0700`. Connection names never affect socket paths.

### Frame protocol

Frame format in `frame.go`:

```text
uint32 big-endian payload length, including the type byte
byte frame type
payload bytes
```

`MaxFrameSize = 1 << 20`.

Frame constants:

| Constant | Value | Direction | Payload |
|---|---:|---|---|
| `FrameInput` | `0x00` | attach → owner | raw terminal input bytes |
| `FrameOutput` | `0x01` | owner → attach | rendered owner TUI output bytes |
| `FrameMeta` | `0x02` | reserved | currently unused |
| `FrameClientHello` | `0x03` | attach → owner | JSON `ClientHello` |
| `FrameServerHello` | `0x04` | owner → attach | JSON `ServerHello` |
| `FrameResize` | `0x05` | attach → owner | JSON `Resize` |
| `FrameControl` | `0x06` | reserved | currently unused |
| `FrameControlAck` | `0x07` | reserved | currently unused |

Protocol structs in `protocol.go`:

```go
const Protocol = "multicrum-local"
const Version = 1

type ClientHello struct {
    Protocol   string `json:"protocol"`
    Version    int    `json:"version"`
    Server     string `json:"server"`
    ClientName string `json:"clientName"`
    ClientKind string `json:"clientKind"`
    Cols       int    `json:"cols"`
    Rows       int    `json:"rows"`
}

type ServerHello struct {
    Protocol  string `json:"protocol"`
    Version   int    `json:"version"`
    Server    string `json:"server"`
    ServerPID int    `json:"serverPid"`
}

type Resize struct {
    Cols int `json:"cols"`
    Rows int `json:"rows"`
}
```

### Attach client behavior

Implemented in `server_unix.go:TryAttach`:

- connect to Unix socket with a short timeout,
- send `ClientHello`, require compatible `ServerHello`,
- put stdin into raw mode with `term.MakeRaw`, restoring on exit,
- forward stdin chunks as `FrameInput`,
- forward `SIGWINCH` as `FrameResize`,
- copy `FrameOutput` payloads to stdout,
- detach on `Ctrl+Q` (`0x11`) or `Alt+Ctrl+Q` (`ESC 0x11`), without killing server sessions.

### Owner behavior

Implemented in `server_unix.go:Listen` / `Owner`:

- removes stale socket only after verifying it is a socket owned by the current UID and not accepting connections,
- listens on the Unix socket,
- accepts clients and validates hello protocol/version,
- injects `FrameInput` into `ui.InputMux`,
- sends resize callbacks to Bubble Tea,
- tracks attached client count and sends `LocalClientCountMsg`,
- mirrors owner TUI output through `Owner.Write`.

`FanoutWriter` in `fanout.go` wraps owner stdout and mirror writer while preserving TTY-like methods (`Read`, `Write`, `Close`, `Fd`) so Bubble Tea detects terminal capabilities correctly.

## Config Tree

Package: `pkg/config`.

Current YAML shape:

```yaml
server: default
activeConnection: work
connections:
  - name: default
    sessions:
      - title: shell
        cmdline: bash
  - name: work
    sessions:
      - title: logs
        cmdline: tail -f /var/log/syslog
      - title: api
        cmd:
          - go
          - run
          - ./cmd/api
      - title: remote
        cmdline: bash -l
        ssh:
          target: user@example.com
          port: "22"
          key: ~/.ssh/id_ed25519
          useDefaultKeys: true
          useAgent: true
```

Structs:

```go
type SSHEntry struct {
    Target                string `yaml:"target,omitempty"`
    Port                  string `yaml:"port,omitempty"`
    Key                   string `yaml:"key,omitempty"`
    UseDefaultKeys        bool   `yaml:"useDefaultKeys,omitempty"`
    UseAgent              bool   `yaml:"useAgent,omitempty"`
    KnownHosts            string `yaml:"knownHosts,omitempty"`
    InsecureIgnoreHostKey bool   `yaml:"insecureIgnoreHostKey,omitempty"`
}

type SessionEntry struct {
    Title   string    `yaml:"title,omitempty"`
    CmdLine string    `yaml:"cmdline,omitempty"`
    Cmd     []string  `yaml:"cmd,omitempty"`
    SSH     *SSHEntry `yaml:"ssh,omitempty"`
}

type ConnectionEntry struct {
    Name     string         `yaml:"name"`
    Sessions []SessionEntry `yaml:"sessions"`
}

type Config struct {
    Server           string            `yaml:"server,omitempty"`
    ActiveConnection string            `yaml:"activeConnection,omitempty"`
    Connections      []ConnectionEntry `yaml:"connections,omitempty"`
    Sessions         []SessionEntry    `yaml:"sessions,omitempty"` // legacy
}
```

Loading behavior:

- Missing file returns `nil` config, not an error.
- Legacy top-level `sessions` migrates to `connections: [{name: default, sessions: ...}]`.
- Missing connection names become `connection-N`.
- Missing `activeConnection` becomes the first loaded connection.
- `cmdline` values are parsed into startup commands with `ui.ParseCmdLine` by `Model.SetConfigConnections`, while preserving the original `cmdline` for future saves.
- `ssh` blocks restore SSH-backed sessions with saved target, port, key, known-host settings, and remote command.

Saving behavior in `pkg/ui/layout_save.go`:

- saves all connections, not just active connection,
- saves server name and active connection name,
- saves every session title,
- prefers `Session.CmdLine()` as `cmdline`; otherwise writes argv-style `cmd`,
- writes SSH-backed session options in `ssh`, including target, port, key, default-key/agent flags, known-hosts override, and insecure host-key flag,
- omits legacy top-level `sessions` when connections exist.

## UI Connection Model

Connection state lives in `pkg/ui/model.go` and helpers in `pkg/ui/connections.go`.

```go
type connectionState struct {
    name           string
    manager        *session.SessionManager
    viewports      map[int]*viewport.Model
    altScreens     map[int]bool
    scrollbackMode map[int]bool
    initialCfg     []startupSession
}
```

`state` has:

- `connections []*connectionState`,
- `activeConn int`,
- `serverName string`,
- `localClients int`,
- active-connection aliases `manager`, `viewports`, `altScreens`, `scrollbackMode`.

`syncActiveConnectionFields()` points those aliases at the active connection. Most existing UI code still reads `s.manager` and `s.viewports`, so calling this helper after connection focus/removal/reorder is required.

Important helpers:

| Helper | Purpose |
|---|---|
| `activeConnection()` | Return current connection, creating default if needed. |
| `addConnection(name)` | Add connection shell state. |
| `focusConnection(index)` / `focusConnectionByName(name)` | Focus a connection and refresh focused session. |
| `renameConnection(index, name)` | Rename and broadcast metadata. |
| `moveConnection(from, to)` | Reorder while preserving active connection identity. |
| `removeConnection(index)` | Close all sessions in one connection and remove it, blocked when it is the final connection. |
| `createConnectionWithDefaultSession(name, model)` | Add connection and start a default session. |
| `initManagers(cols, rows)` | Build session managers for all connections. |
| `SetConfigConnections(cfg)` | Convert config tree into startup connections. |
| `rebindConnectionCallbacks()` | Rebind output/exit callbacks after connection reorder. |

Callback rebinding is important because output/exit message closures capture the connection index used in `connectionOutputMsg` and `connectionExitMsg`.

## TUI Modes, Dialogs, and Shortcuts

Current modes:

| Mode | Purpose |
|---|---|
| `modeNormal` | PTY forwarding + normal shortcuts. |
| `modeRenaming` | Legacy direct session rename mode; currently bypassed by `Ctrl+Alt+R`. |
| `modeSelecting` | Multi-function sessions dialog. |
| `modeHelp` | Help modal. |
| `modeExitPrompt` | Exited-session respawn/remove dialog. |
| `modeNewSession` | New-session modal. |
| `modeConnections` | Multi-function connections dialog. |
| `modeQuitConfirm` | Owner/server quit confirmation. |
| `modeDeleteConfirm` | Delete confirmation for sessions/connections/final-session cases. |

### Modal-safe global shortcuts

`state.handleGlobalShortcut` runs before all mode-specific key handlers.

Always-available shortcuts:

| Shortcut | Action |
|---|---|
| `Ctrl+Alt+T` | Open new-session modal in active connection, even from a faulted/exited-session prompt. |
| `Ctrl+Alt+Left` / `Ctrl+Alt+Right` | Previous/next session in active connection. |
| `Ctrl+Alt+[` / `Ctrl+Alt+]` | Previous/next connection. |
| `Ctrl+Alt+<` / `Ctrl+Alt+>` | Alternate previous/next connection forms. |
| `Ctrl+Alt+Q` | Open owner/server quit confirmation. |

Do not duplicate these bindings in individual modal handlers. Global switching closes stale modal state by returning to `modeNormal` before focusing the new target.

### Sessions dialog

Opened by `Ctrl+Alt+S` or `Ctrl+Alt+R`.

| Key | Action |
|---|---|
| `↑` / `↓` | Move cursor. |
| `Enter` | Focus selected session. |
| `N` | Open full new-session modal; add/cancel returns to sessions dialog. |
| `R` | Rename selected session inline. |
| `M` | Toggle move/reorder mode; then `↑` / `↓` reorders. |
| `F` | Edit filter. |
| `Delete` / `X` / `D` | Delete confirmation. |
| `Esc` | Close dialog, or leave submode when filtering/renaming/moving. |

Filter is only edited in filter mode and is displayed with the pink `scrollIndicatorStyle` badge. Backspace and Ctrl+U work while filtering. Enter/Esc exits filter mode with the filter still applied.

### Connections dialog

Opened by `Ctrl+Alt+O` or `Ctrl+Alt+E`.

| Key | Action |
|---|---|
| `↑` / `↓` | Move cursor. |
| `Enter` | Focus selected connection. |
| `N` | Quick-create connection and return to dialog. |
| `R` | Rename selected connection inline. |
| `M` | Toggle move/reorder mode; then `↑` / `↓` reorders. |
| `F` | Edit filter. |
| `Delete` / `X` / `D` | Delete confirmation. |
| `Esc` | Close dialog, or leave submode when filtering/renaming/moving. |

### Exited-session and final-session behavior

When a session exits, a respawn/remove dialog opens for the focused exited session.

Rules:

- Esc/N/Q do not dismiss this prompt, to avoid leaving focus on a dead, non-controllable session.
- The user must respawn/remove, or use a global shortcut to create a session, switch session/connection, or quit.
- Removing the final session in a connection always requires confirmation.
- If other connections exist, confirming final-session removal removes the entire now-empty connection and focuses another connection; the app does not exit.
- If it is the only session in the only connection, confirming may shut down the owner/server.

## Web UI and WebSocket

`pkg/transport/websocket.go` contains the WebSocket server and generated browser UI. Static fonts and xterm.js assets are embedded from `pkg/transport/static`.

Web UI features:

- Hamburger menu with sessions, connections, connection/session nav, new/kill/rename, save, mouse mode, and settings.
- Sessions dialog mirrors TUI behavior as much as browser controls allow.
- New-session Remote SSH fields include target, port (default `22`), password, key, and remote command.
- Connections dialog mirrors TUI behavior.
- Status bar shows server, connection pills, dimensions, and mouse mode.
- Clicking connection pills focuses a connection.
- Reconnect overlay grays out UI, shows spinner/message, and retries roughly every 10 seconds.
- Mouse mode persists in localStorage.

Web shortcuts:

| Shortcut | Action |
|---|---|
| `Alt+S` | Sessions dialog. |
| `Alt+N` | New session. |
| `Alt+K` | Kill session if safe. |
| `Alt+R` | Sessions dialog on active session. |
| `Alt+P` | Save layout. |
| `Alt+M` | Toggle web mouse mode. |
| `Alt+,` | Settings. |
| `Ctrl+Alt+Left` / `Ctrl+Alt+Right` | Previous/next session. |
| `Ctrl+Alt+[` / `Ctrl+Alt+]` | Previous/next connection. |
| `Ctrl+Alt+O` | Connections dialog. |
| `Ctrl+Alt+E` | Connections dialog on active connection. |
| `Ctrl+Alt+C` | New connection prompt. |

WebSocket binary tags:

| Direction | Tag | Payload |
|---|---:|---|
| server → client | `0x01` | raw PTY bytes |
| server → client | `0x02` | JSON `MetaMsg` |
| client → server | `0x00` | raw terminal input bytes |
| client → server | `0x01` | JSON `ControlMsg` |
| client → server | `0x02` | JSON `ResizeMsg` |

`MetaMsg` includes server name, active connection, connection list, focused session, and active-connection sessions. The browser still accepts legacy raw `[]SessionInfo` metadata.

Control actions include session actions (`focus`, `new`, `kill`, `rename`, `move`, `exit`, `save`) and connection actions (`focusConnection`, `prevConnection`, `nextConnection`, `newConnection`, `renameConnection`, `moveConnection`, `removeConnection`).

## Resize and Rendering

- TUI resize uses `tea.WindowSizeMsg` and resizes all active-connection sessions.
- Browser resize sends `ResizeMsg`; UI handles it as `wsResizeMsg` and resizes one session.
- Last active resizer wins for the session being viewed.
- When browser xterm size differs from TUI pane size, local viewport anchors around cursor instead of always bottoming.
- Browser receives raw PTY bytes and snapshots; TUI renders ANSI from vt10x cells.

## VTScreen and Terminal Quirks

`pkg/session/vtscreen.go` maintains:

- vt10x visible screen,
- raw replay history capped at 256 KiB,
- semantic TUI scrollback capped at 10000 rendered lines.

Notable workaround: `translateSCORC` rewrites bare `ESC[u` to `ESC 8` for vt emulator compatibility while preserving raw browser bytes.

`ui.NewKeyboardStripWriter` strips Bubble Tea keyboard-enhancement sequences (modifyOtherKeys and Kitty keyboard protocol setup/teardown) from stdout so they do not leak into child PTYs.

## Known Limitations

- Local attach server is Unix-only; Windows named-pipe support is not implemented.
- Attached local TUI clients share one owner-rendered UI state, not independent per-client focus/mode/connection state.
- No headless/daemon owner mode yet.
- No split-pane/tiling layout.
- No session persistence across owner process restarts.
- No background-session activity indicator.

## Validation

Run after changes:

```bash
go test ./...
go build -v ./cmd/multicrum/
```

Useful focused validation:

```bash
go test ./pkg/ui ./pkg/transport ./pkg/localserver ./cmd/multicrum
go build -v ./cmd/multicrum/
```

Known gopls-only diagnostic: `cmd/ptyrec/main.go` may show a Windows `syscall.SIGWINCH` error in mixed-platform diagnostics while Linux `go test ./...` and `go build ./...` pass.
