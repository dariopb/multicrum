# multicrum — Current Implementation Specification

## Overview

`multicrum` is a Go terminal multiplexer for multiple persistent terminal sessions. It has two synchronized frontends:

- A local Bubble Tea TUI.
- An optional browser UI served over WebSocket and rendered with embedded xterm.js assets.

Runtime state is now hierarchical:

```text
server
└── connection/workspace
    └── session/tab
```

A session is backed by a real PTY/ConPTY or SSH PTY. Child programs are treated as black-box terminal applications: multicrum forwards terminal input/output bytes and renders output through virtual terminal screen state.

## Commands

```bash
# Build the runnable app (preferred build command)
go build -v ./cmd/multicrum/

# Run tests
go test ./...

# Local TUI on default named server
./multicrum --cmd bash

# Attach to or create a named server
# If no server exists, this starts a detached owner daemon and attaches.
./multicrum --server work --cmd bash
./multicrum --srv work
./multicrum -S work

# Lifecycle commands
./multicrum list
./multicrum status --server work
./multicrum stop --server work

# TUI plus browser UI
./multicrum --server work --cmd bash --ws :9999 --token mytoken
# http://localhost:9999/?token=mytoken
```

CLI entry point: `cmd/multicrum/main.go`.

| Flag | Default | Purpose |
|---|---:|---|
| `--cmd` | `bash` | Default command for sessions; parsed with `ui.ParseCmdLine`. Also used as default remote command when `--ssh` is set. |
| `--server`, `--srv`, `-S` | `default` | Named local long-running server to attach/create. First instance auto-starts a detached owner daemon. |
| `list`, `ls` | n/a | List local servers, PID/socket path, and startup settings. |
| `status` | n/a | Show one server's status and startup settings. |
| `stop` | n/a | Stop one server and disconnect its sessions/clients. |
| `--config` | `multicrum.yaml` | Layout/config YAML path loaded on owner startup and saved with `Ctrl+Alt+P`. |
| `--ssh` | empty | SSH target for default SSH-backed sessions. |
| `-i`, `--ssh-key` | empty | Explicit SSH identity file. |
| `--ssh-passwd` | empty | Explicit password / keyboard-interactive password. |
| `--ssh-use-default-keys` | false | Try standard `~/.ssh/id_*` identity files. |
| `--ssh-agent` | true | Use `SSH_AUTH_SOCK` when no explicit password/key is supplied. |
| `--ssh-known-hosts` | empty | Override known_hosts path. |
| `--ssh-insecure-ignore-host-key` | false | Disable host key verification; testing only. |
| `--ws` | empty | Start WebSocket/xterm.js UI on this address. |
| `--token` | empty | Optional token for `/ws?token=...`. |

## Source Layout

```text
cmd/
  multicrum/
    main.go              # CLI flags, attach-or-own flow, Bubble Tea startup, WS startup
    termsize_unix.go     # Unix terminal size helper
    termsize_windows.go  # Windows terminal size helper
  ptyrec/
    main.go              # PTY recorder/replay diagnostic tool

pkg/
  config/
    config.go            # YAML config tree + legacy migration
    config_test.go
  console/
    console_unix.go      # Unix PTY wrapper
    console_windows.go   # Windows ConPTY wrapper
  localserver/
    path.go              # server-name sanitization helper
    frame.go             # length-prefixed frame codec
    frame_test.go
    protocol.go          # hello/resize protocol structs
    server_unix.go       # Unix socket owner + attach client
    server_windows.go    # Windows loopback-TCP owner + attach client
    fanout.go            # TTY-preserving output fan-out writer
  session/
    doc.go
    manager.go           # SessionManager create/focus/move/kill/resize/respawn
    session.go           # Session lifecycle, read loop, title/cmdline state
    start_unix.go        # Unix PTY startup
    start_windows.go     # Windows ConPTY startup
    current_dir_linux.go # live child cwd lookup through /proc
    current_dir_other.go # unsupported-platform cwd lookup fallback
    vtscreen.go          # Charm vt screen, ANSI rendering, scrollback, replay bytes
  ssh_client/
    auth.go
    client.go
    config.go
    doc.go
    parse.go
    parse_test.go
    session.go
    session_test.go
  transport/
    transport.go         # generic transport interface
    local.go             # no-op local transport
    websocket.go         # Echo/Gorilla WS server + embedded browser UI
  ui/
    model.go             # Bubble Tea model, modes, Update/View, global shortcuts
    connections.go       # connection state and connection management helpers
    connections_modal.go # TUI connection dialog
    new_session.go       # TUI new-session modal
    delete_confirm.go    # shared delete confirmation modal
    quit.go              # owner quit confirmation
    web_control.go       # browser control handlers
    inputmux.go          # local stdin + attached-client input multiplexer
    layout_save.go       # save all connections/sessions to YAML
    keyboardstrip.go     # strip Bubble Tea keyboard-enhancement escapes
    keys.go              # key-to-PTY-byte conversion
    mouse.go             # mouse event encoding/selection routing
    selection.go         # TUI selection/copy logic
    status_connections.go# status-bar connection pills
    styles.go            # lipgloss styles
    textedit.go          # modal text-edit helpers
    clipboard.go         # clipboard transports
    cmdline.go           # command-line parser
    debug.go             # debug helpers
```

Top-level docs/specs:

- `README.md`: user-facing overview.
- `AGENTS.md`: maintainer memory and operational gotchas.
- `spec.md`: this current implementation spec.
- `spec-vertical-layout.md`: detailed bottom/left connection-layout behavior.
- `spec-ssh-client.md`: SSH package details.

## Dependencies

Current direct dependencies include:

| Concern | Library |
|---|---|
| CLI parsing | `github.com/urfave/cli/v3` |
| TUI framework | `charm.land/bubbletea/v2` |
| TUI viewport | `charm.land/bubbles/v2/viewport` |
| TUI styling | `charm.land/lipgloss/v2` |
| Terminal ANSI helpers | `github.com/charmbracelet/x/ansi`, `github.com/charmbracelet/x/vt` |
| VT screen model | `github.com/charmbracelet/x/vt` |
| Unix PTY | `github.com/creack/pty` |
| Windows syscalls / ConPTY | `golang.org/x/sys/windows` |
| SSH | `golang.org/x/crypto/ssh`, `github.com/kevinburke/ssh_config`, `github.com/skeema/knownhosts` |
| Terminal raw mode | `golang.org/x/term` |
| Web server | `github.com/labstack/echo/v4` |
| WebSocket | `github.com/gorilla/websocket` |
| Config YAML | `gopkg.in/yaml.v3` |

Module Go version: `go 1.25.0`.

## Startup and Long-Running Server Flow

`cmd/multicrum/main.go` implements attach-or-daemon semantics:

1. Parse `--server` (`default` when empty).
2. Resolve the Unix socket path with `localserver.SocketPath(serverName)`.
3. Try `localserver.TryAttach(...)`.
   - If it attaches, the process becomes an attach client and returns when detached/disconnected.
   - Attached clients use raw terminal mode, forward stdin frames, receive owner-rendered TUI output frames, map bare LF output to CRLF for raw terminals, and forward resize frames where supported.
   - A newly attached terminal receives an explicit full-frame snapshot with the current alternate-screen state, mouse mode, and exact cursor position/style/visibility. This bypasses the owner's already-painted renderer cache, so the client is never blank until its first input.
4. If attach fails, spawn a detached hidden owner process by re-execing the current binary with hidden `--owner`.
5. The parent waits for the server socket to become ready, then attaches the current terminal as the first client.
6. The detached owner:
   - constructs `ui.Model`, sets server name and config path,
   - loads YAML config and calls `model.SetConfigConnections(cfg)`,
   - creates `ui.InputMux(nil)` so only attached-client frames feed Bubble Tea,
   - starts `localserver.ListenWithSettings(...)`, including startup settings for status/list,
   - writes Bubble Tea output to the local server owner writer,
   - wraps output with `ui.NewKeyboardStripWriter`,
   - starts optional WebSocket UI with `ui.StartWSTransport`.

Lifecycle commands:

- `multicrum list` / `multicrum ls` enumerates socket files and probes live servers.
- `multicrum status --server NAME` reports PID, socket path, and startup settings.
- `multicrum stop --server NAME` sends a local control request to stop a server; it falls back to killing the recorded PID if graceful shutdown times out.

Startup settings exposed in status/list include command, config path, WebSocket bind address, token presence (`token=set`, never the token value), and SSH options.

Unix local servers use Unix sockets. Windows local servers use a per-user loopback TCP listener and store its address in `%LOCALAPPDATA%\multicrum\<server>.addr` (falling back through the user cache directory/temp dir). PTY sessions support Windows through ConPTY.

## Local Server Protocol

Package: `pkg/localserver`.

Socket path:

```text
# Unix:
/tmp/multicrum-$UID/<safe-server>.sock

# Windows:
%LOCALAPPDATA%\multicrum\<safe-server>.addr
# contains a 127.0.0.1:<port> loopback TCP address
```

`SocketPath` sanitizes server names to filesystem-safe names using `path.go:sanitize`. The Unix parent directory is owned by the current user with mode `0700`; this persistent `/tmp` location keeps the socket reachable after the final SSH login ends. On Windows, the path is an address file rather than a socket file.

Frame format:

```text
uint32 big-endian payload length, including type byte
byte frame type
payload bytes
```

`MaxFrameSize` is `1 << 20`.

Frame types in `pkg/localserver/frame.go`:

| Type | Name | Direction | Payload |
|---:|---|---|---|
| `0x00` | `FrameInput` | attach → owner | raw terminal input bytes |
| `0x01` | `FrameOutput` | owner → attach | rendered TUI output bytes |
| `0x02` | `FrameMeta` | reserved | currently unused |
| `0x03` | `FrameClientHello` | attach → owner | JSON `ClientHello` |
| `0x04` | `FrameServerHello` | owner → attach | JSON `ServerHello` |
| `0x05` | `FrameResize` | attach → owner | JSON `Resize` |
| `0x06` | `FrameControl` | control client → owner | JSON `ControlRequest` (`stop`) |
| `0x07` | `FrameControlAck` | owner → control client | JSON `ControlAck` |
| `0x08` | `FrameClipboard` | owner → attach | selected UTF-8 text for client-side clipboard handling |

Protocol structs in `pkg/localserver/protocol.go`:

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
    Protocol  string         `json:"protocol"`
    Version   int            `json:"version"`
    Server    string         `json:"server"`
    ServerPID int            `json:"serverPid"`
    Settings  ServerSettings `json:"settings"`
}

type ServerSettings struct {
    Command                  string `json:"command,omitempty"`
    SSH                      string `json:"ssh,omitempty"`
    SSHKey                   string `json:"sshKey,omitempty"`
    SSHUseDefaultKeys        bool   `json:"sshUseDefaultKeys,omitempty"`
    SSHAgent                 bool   `json:"sshAgent,omitempty"`
    SSHKnownHosts            string `json:"sshKnownHosts,omitempty"`
    SSHInsecureIgnoreHostKey bool   `json:"sshInsecureIgnoreHostKey,omitempty"`
    WS                       string `json:"ws,omitempty"`
    TokenSet                 bool   `json:"tokenSet,omitempty"`
    Config                   string `json:"config,omitempty"`
}

type Resize struct {
    Cols int `json:"cols"`
    Rows int `json:"rows"`
}

type ControlRequest struct {
    Action string `json:"action"`
}

type ControlAck struct {
    OK    bool   `json:"ok"`
    Error string `json:"error,omitempty"`
}
```

Attach-client detach:

- `Alt+Ctrl+Q` (`ESC 0x11`) detaches the attached client only. Plain `Ctrl+Q`
  is forwarded to the TUI/child.
- The left-rail `Multicrum` menu also exposes **Detach**. The owner tracks the
  attach client that supplied the menu click and closes only that connection;
  sessions, the daemon, and other clients continue running.
- Owner `Ctrl+Alt+Q` opens shutdown confirmation and closes the server if confirmed.

Clipboard delivery:

- The owner sends selected text in `FrameClipboard` so the attach process can
  copy in the environment of the receiving terminal.
- Attach clients inside tmux/byobu use `tmux set-buffer -w`; other terminals
  use OSC 52 output.

`localserver.FanoutWriter` preserves TTY-like `Read`, `Write`, `Close`, and `Fd` behavior by delegating to the primary file when possible. Bubble Tea depends on this to detect output capabilities.

## Config Model

Package: `pkg/config`.

Current YAML tree:

```yaml
server: default
activeConnection: work
connectionLayout: left
connectionRailWidth: 15
connections:
  - name: default
    sessions:
      - title: shell
        cmdline: bash
        cwd: /home/user/project
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
    Cwd     string    `yaml:"cwd,omitempty"`
    SSH     *SSHEntry `yaml:"ssh,omitempty"`
}

type ConnectionEntry struct {
    Name     string         `yaml:"name"`
    Sessions []SessionEntry `yaml:"sessions"`
}

type Config struct {
    Server           string            `yaml:"server,omitempty"`
    ActiveConnection string            `yaml:"activeConnection,omitempty"`
    ConnectionLayout string            `yaml:"connectionLayout,omitempty" json:"connectionLayout,omitempty"`
    ConnectionRailWidth int            `yaml:"connectionRailWidth,omitempty" json:"connectionRailWidth,omitempty"`
    Connections      []ConnectionEntry `yaml:"connections,omitempty"`
    Sessions         []SessionEntry    `yaml:"sessions,omitempty"` // legacy
}
```

Loading rules:

- Missing config returns `nil` without error.
- Legacy top-level `sessions` is migrated into one `default` connection by `Config.Normalize()`.
- Empty connection names become `connection-N`.
- Missing active connection becomes the first loaded connection.
- `connectionLayout` accepts `bottom` (default) and `left`; empty or unknown
  values normalize to `bottom`.
- `connectionRailWidth` persists the requested left-rail width. Missing or
  non-positive values use the UI default, and rendering clamps it without
  overwriting the saved preference when the terminal is narrow.
- `cmdline` is parsed into startup `Cmd` with `ui.ParseCmdLine` when loaded through `Model.SetConfigConnections`; this is required so config-defined commands do not fall back to the process default `--cmd` (`bash`).
- On Linux, saving a local interactive shell records its live working
  directory as `cwd` through `/proc/<pid>/cwd`. Other platforms preserve a
  configured `cwd` but do not discover a changed live directory. Startup uses
  the saved directory when usable and otherwise silently falls back to the
  owner's normal working directory.
- `cwd` is not recorded for SSH sessions, non-shell commands, or shell
  one-shots such as `bash -c`.
- `ssh` entries recreate SSH-backed sessions with their saved target, port, key, known-host settings, and remote command.
- `cmdline` is preserved in session state for round-trip saves.

Saving rules:

- `pkg/ui/layout_save.go` saves every connection, not just the active connection.
- It writes `server`, `activeConnection`, `connectionLayout`,
  `connectionRailWidth`, and `connections[].sessions[]`.
- For each session, it prefers `Session.CmdLine()` when present; otherwise it writes argv-style `cmd`.
- SSH-backed sessions also write an `ssh` block with target, port, key, default-key/agent flags, known-hosts override, and insecure host-key flag.
- Top-level legacy `sessions` is omitted when connections exist.

## Command-Line Parsing

`pkg/ui/cmdline.go:ParseCmdLine` parses user/config command lines.

- Empty string → `nil`.
- Plain whitespace-separated commands → `strings.Fields`.
- Lines containing shell metacharacters (`|`, `&`, `;`, `<`, `>`, `(`, `)`, `$`, backticks, glob chars, quotes, backslashes, `~`, etc.) become `[]string{"bash", "-c", line}`.

This preserves shell features such as pipes, redirection, environment expansion, process substitution, and quoted strings.

## Runtime State Model

`ui.Model` contains a pointer to mutable `state` because Bubble Tea models are value-copied.

Connections are represented by `connectionState` in `pkg/ui/model.go` / `pkg/ui/connections.go`:

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

`state` keeps:

- `connections []*connectionState`
- `activeConn int`
- `serverName string`
- `localClients int`
- legacy aliases for active connection state:
  - `manager *session.SessionManager`
  - `viewports map[int]*viewport.Model`
  - `altScreens map[int]bool`
  - `scrollbackMode map[int]bool`

`syncActiveConnectionFields()` points those legacy aliases at the active connection. This lets older session/viewport paths keep working while connection state is per-connection.

Important helpers:

| Helper | Purpose |
|---|---|
| `activeConnection()` | Return active connection, creating default if needed. |
| `syncActiveConnectionFields()` | Rebind active manager/viewports/alt/scrollback aliases. |
| `addConnection(name)` | Add connection state. |
| `focusConnection(index)` / `focusConnectionByName(name)` | Change active connection and refresh. |
| `renameConnection(index, name)` | Rename and broadcast metadata. |
| `moveConnection(from, to)` | Reorder connections and preserve active connection identity. |
| `removeConnection(index)` | Close sessions in one connection, remove it, and focus a remaining connection. |
| `createConnectionWithDefaultSession(name, model)` | Add connection with default session. |
| `initManagers(cols, rows)` | Create a `SessionManager` for each connection. |
| `rebindConnectionCallbacks()` | Rebind output/exit callbacks after connection reordering. |

When reordering connections, manager callbacks must be rebound because closures capture the connection index used in `connectionOutputMsg` / `connectionExitMsg`.

## Session Manager

Package: `pkg/session`.

`SessionManager` owns one connection's sessions and focused index.

Implemented operations:

| Method | Purpose |
|---|---|
| `New(cmd []string)` | Create local/default-backend session. |
| `NewInDir(cmd []string, cwd string)` | Create a local session in a saved directory, silently falling back when unusable. |
| `NewWithSSH(cmd []string, sshClient *ssh_client.Client)` | Create local or SSH-backed session; rolls back if start fails. |
| `Focus(index)` | Focus session. |
| `Rename(index, title)` | Set title override. |
| `Move(from, to)` | Reorder sessions and reindex. |
| `Kill(index)` | Close/remove session; refuses to remove the final remaining session. |
| `Respawn(index)` | Relaunch an exited session. |
| `ResizeOne` / `ResizeAll` | Resize session(s). |
| `SetSendOutput` / `SetSendExit` | Rebind callbacks for sessions already created. |

Session indexes are zero-based and are reindexed after kill/move. UI viewport maps are keyed by session index, so callers must delete/reset viewports when session indexes are removed or reused.

## UI Modes and Dialogs

Current modes in `pkg/ui/model.go`:

| Mode | Purpose |
|---|---|
| `modeNormal` | PTY forwarding + normal shortcuts. |
| `modeRenaming` | Legacy direct session rename mode; currently bypassed by `Ctrl+Alt+R` in favor of sessions dialog. |
| `modeSelecting` | Multi-function sessions dialog. |
| `modeHelp` | Help modal. |
| `modeExitPrompt` | Exited-session respawn/remove modal. |
| `modeNewSession` | New-session modal. |
| `modeConnections` | Multi-function connections dialog. |
| `modeQuitConfirm` | Owner/server quit confirmation. |
| `modeDeleteConfirm` | Delete confirmation for sessions/connections/final-session cases. |
| `modeScrollSearch` | Scrollback search or line-jump input. |
| `modeContextMenu` | Session, connection, or global application context menu. |

### Global shortcuts

Global shortcuts are handled before mode-specific modal handlers by `state.handleGlobalShortcut`. Do not duplicate these in individual modal handlers.

Always-available TUI shortcuts:

| Shortcut | Action |
|---|---|
| `Ctrl+Alt+T` | Open new-session modal in active connection, even from exited-session dialogs. |
| `Ctrl+Alt+Left` / `Ctrl+Alt+Right` | Previous/next session in active connection. |
| `Ctrl+Alt+[` / `Ctrl+Alt+]` | Previous/next connection. |
| `Ctrl+Alt+<` / `Ctrl+Alt+>` | Alternate previous/next connection forms where terminals report shifted comma/period. |
| `Ctrl+Alt+Q` | Open owner/server quit confirmation. |

Global connection/session switching closes stale modal state by returning to `modeNormal` before focusing the new target.

### Normal TUI shortcuts

| Shortcut | Action |
|---|---|
| `Alt+`` | Help modal. |
| `Ctrl+Alt+T` | New-session modal. |
| `Ctrl+Alt+W` | Kill focused session if not final session. |
| `Ctrl+Alt+R` | Open sessions dialog on active session; press `R` to rename. |
| `Ctrl+Alt+S` | Open sessions dialog. |
| `Ctrl+Alt+O` | Open connections dialog. |
| `Ctrl+Alt+E` | Open connections dialog on active connection; press `R` to rename. |
| `Ctrl+Alt+C` | Quick-create a new connection and focus it. |
| `Ctrl+Alt+P` | Save layout to `--config` path. |
| `Ctrl+Alt+M` | Toggle mouse mode. |
| `Alt+1..9` / `Ctrl+Alt+1..9` | Jump to session N in active connection. |
| Scrollback shortcuts | `Ctrl+Alt+Up/Down`, `Ctrl+Alt+PgUp/PgDown`, `Ctrl+Alt+Home/End`. |

### Sessions dialog

`modeSelecting` is a multi-function sessions dialog.

| Key | Action |
|---|---|
| `↑` / `↓` | Move cursor. |
| `Enter` | Focus selected session. |
| `N` | Open full new-session modal; after add/cancel, return to sessions dialog. |
| `R` | Rename selected session inline in dialog. |
| `M` | Toggle move/reorder mode; while active, `↑` / `↓` moves the selected session. |
| `F` | Enter filter-edit mode. |
| `Delete` / `X` / `D` | Open delete confirmation. |
| `Esc` | Close dialog, except while editing filter/rename/move where it exits that submode. |

Filter behavior:

- Active filter is shown with the pink `scrollIndicatorStyle` badge.
- Typing edits the filter only in filter mode.
- Backspace and Ctrl+U work in filter mode.
- Enter/Esc leaves filter mode with the filter still applied.

### Connections dialog

`modeConnections` mirrors sessions dialog behavior for connections.

| Key | Action |
|---|---|
| `↑` / `↓` | Move cursor. |
| `Enter` | Focus selected connection. |
| `N` | Create a new connection and return to the connections dialog. |
| `R` | Rename selected connection inline in dialog. |
| `M` | Toggle move/reorder mode; while active, `↑` / `↓` moves the selected connection. |
| `F` | Enter filter-edit mode. |
| `Delete` / `X` / `D` | Open delete confirmation. |
| `Esc` | Close dialog, except while editing filter/rename/move where it exits that submode. |

### New-session modal

`modeNewSession` supports three creation modes:

1. Same as current/default.
2. Local command.
3. Remote SSH.

The modal stores typed fields in `newSessionState` and supports cursor editing, Tab field navigation, and 1/2/3 mode selection. Remote SSH fields include target, port (default `22`), password, key file, and remote command. If opened from the sessions dialog, add/cancel returns to the sessions dialog so users can keep adding.

### Exited-session prompt and final-session behavior

When a session exits, `modeExitPrompt` offers respawn/remove.

Rules:

- The dialog is intentionally non-dismissible with Esc/N/Q, because closing it leaves focus on an exited, non-controllable session.
- Mouse clicks may focus another session or connection while the prompt is
  open; doing so closes the stale prompt just like keyboard focus switching.
- A focused exited session keeps the normal active-tab background and adds the
  `✗` marker, so focus remains visible.
- User must respawn/remove, or use global shortcuts to create another session, switch session/connection, or quit.
- Removing the final session in a connection always requires an additional confirmation.
- If other connections exist, confirming final-session removal removes the whole now-empty connection and focuses another connection; the app does not exit.
- If it is the final session in the only connection, confirming may shut down the owner/server.

## Web UI

The browser UI is embedded in `pkg/transport/websocket.go:indexHTML()` and uses embedded assets in `pkg/transport/static/`.

UI features:

- Hamburger menu with sessions, connections, prev/next connection, new/rename connection, prev/next session, new/kill/rename session, save layout, mouse mode, and settings.
- Session tab bar.
- Connection pills in status bar; clicking a pill focuses that connection.
- xterm.js terminal area.
- Settings modal for fonts/theme/palette/scrollback/viewport.
- Special keys popover.
- Reconnect overlay: on WebSocket disconnect, gray out UI, show spinner/message, and retry roughly every 10 seconds.
- Mouse modes:
  - `app`: xterm/app mouse behavior.
  - `select`: local scrollback wheel and forced text selection.

Web shortcuts currently use browser-safe bindings:

| Shortcut | Action |
|---|---|
| `Alt+S` | Sessions dialog. |
| `Alt+N` | New session modal. |
| `Alt+K` | Kill session if safe. |
| `Alt+R` | Sessions dialog on active session; press `R` to rename. |
| `Alt+P` | Save layout. |
| `Alt+M` | Toggle mouse mode. |
| `Alt+,` | Settings. |
| `Ctrl+Alt+Left` / `Ctrl+Alt+Right` | Previous/next session. |
| `Ctrl+Alt+[` / `Ctrl+Alt+]` | Previous/next connection. |
| `Ctrl+Alt+O` | Connections dialog. |
| `Ctrl+Alt+E` | Connections dialog on active connection; press `R` to rename. |
| `Ctrl+Alt+C` | New connection prompt. |

Web sessions/connections dialogs mirror TUI multi-function behavior as closely as browser prompts/confirm dialogs allow.

## WebSocket Protocol

Every WebSocket binary message uses byte 0 as a tag.

| Direction | Tag | Payload |
|---|---:|---|
| server → client | `0x01` | raw PTY bytes for xterm.js |
| server → client | `0x02` | JSON `MetaMsg` |
| client → server | `0x00` | raw terminal input bytes |
| client → server | `0x01` | JSON `ControlMsg` |
| client → server | `0x02` | JSON `ResizeMsg` |

Current structs:

```go
type SessionInfo struct {
    ID     int    `json:"id"`
    Title  string `json:"title"`
    Exited bool   `json:"exited"`
}

type ConnectionInfo struct {
    ID           string        `json:"id"`
    Name         string        `json:"name"`
    FocusedID    int           `json:"focusedId"`
    SessionCount int           `json:"sessionCount"`
    Sessions     []SessionInfo `json:"sessions,omitempty"`
}

type MetaMsg struct {
    Server           string           `json:"server,omitempty"`
    ActiveConnection string           `json:"activeConnection,omitempty"`
    Connections      []ConnectionInfo `json:"connections,omitempty"`
    FocusedID        int              `json:"focusedId"`
    Sessions         []SessionInfo    `json:"sessions"`
}

type ControlMsg struct {
    Action     string `json:"action"`
    ID         int    `json:"id"`
    To         int    `json:"to,omitempty"`
    Title      string `json:"title,omitempty"`
    Connection string `json:"connection,omitempty"`
    Mode       string `json:"mode,omitempty"`     // "same" | "local" | "remote"
    Cmd        string `json:"cmd,omitempty"`
    Target     string `json:"target,omitempty"`
    Port       string `json:"port,omitempty"`
    Password   string `json:"password,omitempty"`
    Key        string `json:"key,omitempty"`
    Choice     string `json:"choice,omitempty"`   // exit: "respawn" | "remove"
}

type ResizeMsg struct {
    ID   int `json:"id"`
    Cols int `json:"cols"`
    Rows int `json:"rows"`
}
```

Control actions:

| Action | Meaning |
|---|---|
| `focus` | Focus active-connection session by ID and send snapshot. |
| `new` | Create session in active connection. |
| `kill` | Kill session in active connection if safe. |
| `rename` | Rename session. |
| `move` | Reorder session. |
| `exit` | Respawn/remove exited session. |
| `save` | Save layout. |
| `focusConnection` | Focus connection by name/ID. |
| `prevConnection` / `nextConnection` | Cycle connection focus. |
| `newConnection` | Create and focus connection. |
| `renameConnection` | Rename connection. |
| `moveConnection` | Reorder connection. |
| `removeConnection` | Remove connection if safe. |

WebSocket gotchas:

- `gorilla/websocket` allows only one concurrent writer per connection; all writes use `wsClient.write()`.
- Metadata broadcasts do not include PTY snapshots; snapshots are sent through `wsClient.sendSnapshot` on focus changes.
- Browser accepts the old raw `[]SessionInfo` metadata shape for compatibility.
- The generated HTML must not be formatted with `fmt.Sprintf` over the whole template because literal `%` in CSS/JS corrupts formatting; use `strings.Replace(..., "__WS_QUERY__", wsQuery, 1)`.

## Rendering and Resize Semantics

- TUI uses `VTScreen.Render()` for styled visible screen rendering.
- Local TUI scrollback uses `VTScreen.RenderWithScrollback()` only when the user enters scrollback mode.
- Browser receives raw PTY bytes through `SendPTY` and raw snapshots through `RawSnapshot`.
- Browser resize sends `wsResizeMsg`; TUI resize uses `tea.WindowSizeMsg`.
- Last active resizer wins for the session being viewed.
- If the VT screen is taller than the TUI pane, local viewport anchors to cursor rather than blindly going bottom.
- Alt-screen transitions reset local scrollback mode and selection.
- **Resize reflow.** The vt emulator does not reflow: shrinking width crops the
  right edge of every row and the dropped characters are lost, so a later widen
  would leave blank cells. On a size change `VTScreen.Resize` reconstructs the
  visible screen by resetting the emulator (`RIS`) and replaying a bounded tail
  of raw history at the new dimensions, which re-wraps lines and restores
  characters an earlier shrink dropped. Only a screenful of history is replayed
  (see `reflowTail`), keeping each resize sub-millisecond instead of the ~95 ms
  a full 256 KiB replay took. Reflow is skipped on the alternate screen (those
  apps redraw themselves on SIGWINCH), and terminal replies are suppressed
  during the replay so replayed DSR/CPR queries are not forwarded to the child.
- `tea.WindowSizeMsg` re-renders the focused viewport after resizing so the
  reflowed content is shown immediately; without it the viewport keeps text
  wrapped at the previous width and shows stale "ghost" rows until the next PTY
  output.
- **Render throttling & memoization.** Bubble Tea rebuilds `View()` after every
  message, so PTY output notifications are coalesced: each connection's read
  loop only enqueues one `connectionOutputMsg` at a time (`outputPending`
  atomic flag, re-armed once the frame is drawn in `renderTickMsg`), capping a
  chatty child at ~60 view rebuilds/sec. The visible pane string
  (`renderPaneContent`) is memoized in `state.paneCache`, keyed on the viewport
  content, scroll offset, pane size and wrap flag, so redundant `View()` calls
  between frames reuse the last result instead of re-splitting/re-padding.

## VTScreen Details

`VTScreen` maintains:

- `vt.Emulator` visible screen.
- Raw replay history capped at 256 KiB for browser snapshots.
- TUI semantic scrollback capped at 10000 rendered lines.

Important behavior:

- `Render()` walks Charm vt cells and emits ANSI SGR color sequences.
- `RenderWithScrollback()` prepends semantic scrollback for local TUI browsing. It returns full, un-wrapped logical lines so copy/selection keep real line breaks; the scrollback pane re-wraps them to the pane width for display.
- Logical-line capture treats a `CR`+`LF` pair as one line break, but a **lone** `CR` returns to column 0 and overwrites the current line in place (prompt redraws, progress bars). A bare `CR` does **not** commit a new logical line, so repeated prompt redraws no longer accumulate duplicate blank prompt lines in scrollback. The decision is deferred (`pendingCR`) so a `CR` at a read-buffer boundary is resolved by the next byte.
- Scrollback pane rendering windows content with the viewport's `YOffset`,
  which is measured in soft-wrapped row space. Scrollback is cached as
  prewrapped ANSI/plain rows with `SoftWrap=false` on the viewport itself, so
  offset changes remain constant-time instead of rewrapping the entire history.
- `SetReplyWriter()` forwards CPR/DSR replies back to child PTY/SSH.
- `translateSCORC` rewrites bare `ESC[u` to `ESC 8` before feeding the Charm vt emulator because it lacks SCORC handling; raw browser history keeps original bytes.
- `Resize()` reflows the visible screen on a size change by replaying a bounded tail of raw history (see Rendering and Resize Semantics).

## Keyboard Escape Stripping

Bubble Tea v2 emits keyboard-enhancement sequences such as modifyOtherKeys and Kitty keyboard protocol requests. In a multiplexer these bytes can leak to child PTYs. `ui.NewKeyboardStripWriter` removes those specific CSI sequences while preserving normal output and Kitty reports that may come from the child.

`cmd/multicrum/main.go` wraps output with it via `tea.WithOutput(ui.NewKeyboardStripWriter(output))`.

## SSH Behavior

Package: `pkg/ssh_client`.

- Targets accept `host`, `user@host`, `host:port`, `user@host:port`, bracketed IPv6, and unbracketed IPv6 host-only forms.
- `Resolve` merges explicit flags/options, `~/.ssh/config`, `/etc/ssh/ssh_config`, default username/port, identity files, agent, default keys, and known-host verification.
- Explicit key uses only that key.
- Explicit password uses password and keyboard-interactive password auth.
- Config identities/default keys/agent are considered only when no explicit password/key is supplied.
- Known hosts verification uses `github.com/skeema/knownhosts` unless explicitly disabled.
- `RemoteSession` implements terminal `Read`, `Write`, `Resize`, `Close`, and OpenSSH-style line-start escapes (`~.`, `~~`).

## Mouse and Selection

TUI mouse modes:

- `mouse:select`: local selection/copy. The selection source **must match what
  the pane displays**: in live mode the pane shows the viewport's most recently
  rendered `VTScreen.Render()` snapshot, so selection maps mouse rows onto that
  snapshot (not the current emulator cells); in scrollback mode the pane shows
  `RenderWithScrollback()` (the full logical buffer), so it maps onto
  `VTScreen.BufferLines()`. `state.paneRowBase` is therefore just the viewport
  `YOffset` in both modes. Mapping live-mode rows onto the logical
  `BufferLines()` tail was a bug: after a `clear` the visible screen is a fresh
  top-aligned frame that diverges from the logical scrollback tail, so
  selecting the first on-screen lines picked unrelated history rows until
  enough output scrolled the screen back into alignment. Reading the current
  emulator cells was also wrong: a resize or child redraw may advance the
  emulator after the last render tick, making a visibly populated row select
  newer or blank content.
- Live selection expands the painted viewport snapshot into the same virtual
  rows Bubble Tea displays when the pane is narrower than the PTY grid.
  `VTScreen.VisibleLines()` reconstructs terminal-grid soft-wrap boundaries
  from logical lines, with full-width fallback for trailing-space, tab-expanded,
  and cursor-redrawn rows. Copy joins both kinds of continuation row without a
  newline while preserving proven CR/LF boundaries.
- `mouse:app`: forward mouse events to the child only when it has enabled terminal mouse reporting.

Right-clicking a session tab or connection tab opens a bordered context menu
anchored immediately inside the pane beside that tab. It uses the same modal
style and offers focus, rename, move, and remove. A clicked item selects the
target in the relevant selector and invokes its existing keyboard handler
(`Enter`, `R`, `M`, or `Delete`); it does not implement duplicate action logic.
While open, it uses all-motion mouse reporting so the item under the pointer is
highlighted with the modal selection style. Right-clicking another tab replaces
the current menu with one for that tab.

In left layout, clicking either mouse button on the `Multicrum` title opens a
full-screen-coordinate global menu: Help, New Session, Sessions, New
Connection, Connections, Toggle Mouse (showing `select` or `app`), Save Layout,
Detach, and Quit. Detach targets only the client that generated the click;
Quit uses the existing owner/server confirmation.

The TUI's visible `[+] Ctrl+Alt+T` tab-bar affordance and Help status-bar
affordance are also left-clickable. They dispatch the same existing new-session
and help shortcuts as their keyboard equivalents.

Both modes keep mouse reporting enabled (select mode uses `CellMotion`, app mode
`AllMotion`); `Model.View()` sets `MouseMode` and bubbletea's renderer emits the
enable/disable transitions. Two startup subtleties: (1) the input-mode reset in
`Model.Init()` (`resetTerminalInputModes`) must not reset the renderer-owned mouse
modes (1002/1003/1006), and (2) with a detached owner the renderer's one-time
mouse-enable is written before any client attaches, so on each client attach the
owner re-asserts the enable sequence (`state.mouseEnableSequence()`) for the newly
connected terminal — otherwise wheel/selection stay dead until the user toggles
mouse mode twice.

Attach handling also emits the complete current frame and cursor state. Raw
snapshot output is CRLF-normalized and ends with an absolute cursor placement
or explicit cursor hide, keeping the new terminal aligned with Bubble Tea's
existing renderer state.

Web mouse modes:

- `app`: xterm default behavior.
- `select`: wheel scrolls local xterm scrollback and left click forces selection with a synthesized Shift-modified mouse event.

## Testing and Known Diagnostics

Run after changes:

```bash
go test ./...
go build -v ./cmd/multicrum/
```

Useful focused commands:

```bash
go test ./pkg/ui ./pkg/transport ./cmd/multicrum
go build ./cmd/multicrum
```

Known LSP-only cross-platform diagnostic: `cmd/ptyrec/main.go` references `syscall.SIGWINCH` on Unix-oriented code and may show as a Windows diagnostic in gopls, while Linux `go test ./...` and `go build ./...` pass.

## Known Not Implemented

- Background-session activity indicator.
- Session persistence / scrollback export.
- Split-pane / tiling layout.
- Headless/daemon server mode without an owner TUI.
- Independent per-client UI state for multiple attached TUI clients; current local attach clients share the owner-rendered TUI/mode/focus/active connection.
- Windows named-pipe implementation for local long-running server attach.
