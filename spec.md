# multicrum — Current Specification

## Overview

multicrum is a Go terminal multiplexer for running multiple persistent CLI/agent sessions. It provides:

- A local Bubble Tea TUI.
- An optional browser UI served by Labstack Echo and rendered with xterm.js over WebSocket.
- One real PTY/ConPTY-backed process per session.
- Shared session management between local TUI and web UI.

The app treats each child command as a black-box terminal program and forwards terminal bytes rather than speaking any agent-specific protocol.

---

## Current Tech Stack

| Concern | Library |
|---|---|
| CLI parsing | `github.com/urfave/cli/v3` |
| TUI framework | `github.com/charmbracelet/bubbletea` |
| TUI viewport | `github.com/charmbracelet/bubbles/viewport` |
| TUI styling | `github.com/charmbracelet/lipgloss` |
| VT screen model | `github.com/hinshun/vt10x` |
| Unix PTY | `github.com/creack/pty` |
| Windows ConPTY | `golang.org/x/sys/windows` via `console.WinConsole` |
| Web framework | `github.com/labstack/echo/v4` |
| WebSocket upgrade/protocol | `github.com/gorilla/websocket` |
| Browser terminal | xterm.js from CDN |

---

## Commands

```bat
# Build all packages
C:\go124\go\bin\go.exe build ./...

# Run compile checks / tests
C:\go124\go\bin\go.exe test ./...

# Run local TUI
C:\go124\go\bin\go.exe run . --cmd "bash"

# Run local TUI plus browser UI
C:\go124\go\bin\go.exe run . --cmd "bash" --ws :9999 --token mytoken

# Browser URL
# http://localhost:9999/
# http://localhost:9999/?token=mytoken
```

`main.go` uses `urfave/cli/v3` flags:

| Flag | Default | Purpose |
|---|---:|---|
| `--cmd` | `bash` | Command to run for each local session, or remote command when `--ssh` is set; parsed with `strings.Fields` |
| `--ssh` | empty | SSH target for remote-backed sessions, e.g. `user@host` or `user@host:2222` |
| `-i`, `--ssh-key` | empty | Explicit SSH identity file; overrides config/default identities |
| `--ssh-passwd` | empty | Explicit password / keyboard-interactive password; overrides config/default identities |
| `--ssh-use-default-keys` | false | Try standard `~/.ssh/id_*` identity files |
| `--ssh-agent` | true | Use `SSH_AUTH_SOCK` when no explicit password/key is provided |
| `--ssh-known-hosts` | empty | Override known_hosts path |
| `--ssh-insecure-ignore-host-key` | false | Disable host key verification; unsafe testing-only escape hatch |
| `--ws` | empty | If set, starts the Echo/xterm.js web UI on this address |
| `--token` | empty | Optional token required on `/ws?token=...` |

---

## Architecture

```text
main.go
  └─ urfave/cli command
      └─ ui.NewModel(agentCmd, cols, rows)
          └─ Bubble Tea program
              └─ session.SessionManager
                  └─ session.Session per tab/session
                      ├─ console.UnixConsole on Unix
                      ├─ console.WinConsole on Windows
                      ├─ readLoop → VTScreen.Write(raw PTY bytes)
                      └─ SendOutput(OutputMsg) → Bubble Tea Update

optional web path
  ui.StartWSTransport(...)
    └─ transport.NewWSTransport(...)
        ├─ Echo routes: GET / and GET /ws
        ├─ Gorilla websocket upgrade on /ws
        ├─ server→client raw PTY stream
        └─ client→server input/control/resize messages
```

### Package Layout

```text
multicrum/
├── cmd/
│   └── multicrum/           # urfave/cli parsing, Bubble Tea startup, optional web startup
│       └── main.go
├── pkg/
│   ├── console/
│   │   ├── console_unix.go      # Unix PTY wrapper, implements io.ReadWriteCloser + Resize + Done
│   │   └── console_windows.go   # Windows ConPTY wrapper, implements io.ReadWriteCloser + Resize + Done
│   ├── ssh_client/              # reusable SSH client/session backend
│   │   ├── config.go            # target/config/auth/known_hosts resolution
│   │   ├── parse.go             # OpenSSH-like [user@]host[:port] parsing
│   │   ├── auth.go              # key/password/agent auth methods
│   │   ├── client.go            # reusable Client factory
│   │   └── session.go           # interactive SSH PTY Read/Write/Resize/Close adapter
│   ├── session/
│   │   ├── session.go           # Session lifecycle, read loop, rename state
│   │   ├── manager.go           # SessionManager create/focus/kill/rename/resize
│   │   ├── start_unix.go        # wires Session.Start to console.NewUnixConsole
│   │   ├── start_windows.go     # wires Session.Start to console.NewWinConsole
│   │   └── vtscreen.go          # vt10x-backed screen, ANSI TUI render, raw replay buffer
│   ├── ui/
│   │   ├── model.go             # Bubble Tea model, modes, session actions, WS callback integration
│   │   ├── keys.go              # key constants and KeyMsg→PTY byte conversion
│   │   ├── layout.go            # pane sizing
│   │   └── styles.go            # lipgloss styles
│   └── transport/
│       ├── transport.go         # generic transport interface
│       ├── local.go             # no-op local transport
│       └── websocket.go         # Echo server, Gorilla WS protocol, embedded xterm.js HTML
├── cmd/ptyrec/             # PTY recorder/replay diagnostic tool
├── AGENTS.md
├── spec.md
└── go.mod
```

---

## Core Components

### `Session`

`session.Session` owns one running child process and its terminal state.

Current responsibilities:

- Store command args, display title override, exited state, PTY/SSH read-write handle, resize callback, and optional `ssh_client.Client`.
- Start through platform-specific `Start(cols, rows)` implementation; when `sshClient` is non-nil, `Start` delegates to `startSSH` instead of local PTY/ConPTY.
- `Write(p []byte)` forwards keyboard bytes to PTY/ConPTY or SSH remote stdin.
- `Resize(cols, rows)` resizes both `VTScreen` and underlying PTY/ConPTY or sends SSH `WindowChange`.
- `Close()` kills/closes the process backend or SSH remote session.
- `readLoop()` reads raw bytes, updates `VTScreen`, and emits `OutputMsg`.
- `Title()` returns renamed title if set, otherwise command name.

### `console.UnixConsole`

Unix backend implemented in `pkg/console/console_unix.go`:

- Uses `creack/pty.StartWithSize`.
- Sets `TERM=xterm-256color`.
- Implements `Read`, `Write`, `Resize`, `Close`, and `Done`.
- Used by `pkg/session/start_unix.go`.

### `console.WinConsole`

Windows backend implemented in `pkg/console/console_windows.go`:

- Creates ConPTY pipes with `os.Pipe()`.
- Calls `windows.CreatePseudoConsole`.
- Builds `STARTUPINFOEX` via `windows.NewProcThreadAttributeList`.
- Uses `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` (`0x00020016`).
- Calls `windows.CreateProcess`.
- Implements `Read`, `Write`, `Resize`, `Close`, and `Done`.
- Used by `pkg/session/start_windows.go`.

### `ssh_client`

Top-level package `pkg/ssh_client/` is a reusable library for opening interactive SSH PTY sessions.

Implemented behavior:

- `ParseTarget` accepts OpenSSH-like targets: `host`, `user@host`, `host:port`, `user@host:port`, bracketed IPv6, and unbracketed IPv6 host-only forms.
- `Resolve` combines explicit options, `~/.ssh/config` / `/etc/ssh/ssh_config`, default username/port, auth methods, and known-host verification into a concrete `ResolvedConfig`.
- Auth precedence is explicit and intentional:
  - `--ssh-key` / `Options.IdentityFile` uses only that explicit key.
  - `--ssh-passwd` / `Options.Password` uses only password and keyboard-interactive password auth.
  - Config `IdentityFile`, `--ssh-use-default-keys`, and `SSH_AUTH_SOCK` are considered only when no explicit password/key is supplied.
- Default keys, when enabled, are tried in this order: `id_ed25519`, `id_ecdsa`, `id_rsa`, `id_dsa`.
- Host keys are verified with `github.com/skeema/knownhosts`; insecure ignore is only available via explicit option.
- `Client.Start(cols, rows)` opens a fresh SSH connection and remote PTY; `RemoteSession` implements `io.ReadWriteCloser` plus `Resize` and `Done`.
- SSH remote sessions support OpenSSH-style escapes at line start: `~.` disconnects, `~~` sends a literal `~`.

### `VTScreen`

`VTScreen` keeps three representations:

- `vt10x.Terminal` for current visible screen state.
- `rawHistory` capped at 256 KiB for xterm.js replay snapshots.
- ANSI-rendered scrollback plus plain `BufferLine` scrollback for local rendering and mouse copy extraction.

Important behavior:

- `Write(p)` feeds vt10x and appends to replay history.
- Bulk output is split at scroll-trigger bytes so scrollback captures large outputs reliably.
- `Render()` walks vt10x cells and emits ANSI SGR sequences so the Bubble Tea viewport can show colors.
- Reverse-video rendering handles vt10x default color sentinels so reverse search highlights show correctly.
- `BufferLines()` returns plain text rows plus a soft-wrap flag; mouse selection uses this to avoid inserting newlines inside soft-wrapped logical lines.
- `RawSnapshot()` returns a copy of replay bytes for browser clients.
- `SetReplyWriter()` forwards vt10x CPR/DSR replies to the PTY/SSH backend so remote TUIs that query cursor position work.

### `SessionManager`

`SessionManager` owns the session slice and focused index.

Implemented operations:

- `New(cmd []string)` creates and focuses a new session using the manager's default backend.
- `NewWithSSH(cmd []string, sshClient *ssh_client.Client)` creates a one-off local or SSH-backed session. If start fails, it rolls back the half-created session and restores focus.
- `Focus(index int)` changes focus.
- `Rename(index, title)` updates a session title override.
- `Kill(index)` closes and removes a session, but refuses to remove the final remaining session.
- `ResizeOne` / `ResizeAll` forward terminal size changes.
- `SetSendOutput` chains output callbacks for TUI + WebSocket broadcast.

Session indexes are re-numbered after kill. Because viewports are keyed by index, new sessions must reset their viewport to avoid stale content from a reused index.

---

## Local TUI

The Bubble Tea model stores mutable data in a pointer `*state` to avoid losing mutations through Bubble Tea value-copy semantics.

### TUI Modes

| Mode | Purpose |
|---|---|
| `modeNormal` | PTY forwarding + global shortcuts |
| `modeRenaming` | Inline rename prompt in status bar |
| `modeSelecting` | Filterable session selector |
| `modeHelp` | Centered help modal |
| `modeExitPrompt` | Centered modal for respawn/remove after a session exits |
| `modeNewSession` | Centered modal opened by Ctrl+Alt+T for choosing same/local/remote session creation |

### TUI Key Bindings

| Key | Action |
|---|---|
| `Ctrl+Alt+T` | Open new-session modal; Enter defaults to same current/default session, or choose typed local command / remote SSH target |
| `Ctrl+Alt+W` | Kill focused session, except final remaining session |
| `Ctrl+Alt+R` | Rename focused session |
| `Ctrl+Alt+S` | Open filtered session selector |
| `Ctrl+Alt+Left` / `Ctrl+Alt+Right` | Previous / next session |
| `Alt+1..9` | Jump to session N |
| `Ctrl+Alt+M` | Toggle mouse mode between select/copy and app forwarding |
| `Ctrl+Alt+Q` | Close all sessions and quit |
| Other handled keys | Converted by `keyToBytes()` and forwarded to focused PTY |

### New-session Modal

`Ctrl+Alt+T` opens `modeNewSession` instead of immediately creating a tab.

Options:

1. **Same as current/default** — default selection; pressing Enter preserves the old behavior and starts another session using the model's default local command or default SSH client factory.
2. **Local command** — user types a free-form local command. It is parsed with `strings.Fields` and started through the local PTY/ConPTY backend.
3. **Remote SSH** — user enters `user@host[:port]`, optional password, optional key file, and optional remote command. This creates a one-off `ssh_client.Client` and starts the new tab through the SSH PTY backend.

If the new command/SSH connection fails to start, the half-created session is rolled back by `SessionManager.NewWithSSH`, focus returns to the previous session, and the modal remains open with a wrapped inline error block (at least four lines) instead of setting global `errMsg`.

### Mouse Modes and Selection

Bubble Tea mouse tracking is enabled at startup. The status bar shows:

- `mouse:select` — app-owned selection mode. Left-drag selects text from the focused viewport; release copies through available clipboard transports (native helpers, tmux paste buffer, then OSC 52). Soft-wrapped rows are joined without spurious newlines by using `VTScreen.BufferLines()` and vt10x's wrap flag.
- `mouse:app` — mouse events are forwarded to the child process only when the child has enabled terminal mouse reporting.

`Ctrl+Alt+M` toggles between these modes.

### Layout

```text
┌──────────────────────────────────────────────┐
│ [1] name  [2] name  [+] Ctrl+T               │  tab bar
├──────────────────────────────────────────────┤
│                                              │
│           active session viewport            │
│                                              │
├──────────────────────────────────────────────┤
│ session N │ running/exited │ cols×rows help  │  status bar
└──────────────────────────────────────────────┘
```

`paneSize()` subtracts two rows for tab bar and status bar.

---

## Web UI

The web UI is served from `pkg/transport/websocket.go`:

- `GET /` serves embedded HTML generated by `indexHTML()`.
- `GET /ws` upgrades to WebSocket using Gorilla.
- HTTP routing is handled by Labstack Echo.
- Browser terminal rendering uses xterm.js and fit addon from CDN.

### Web Header

The header includes:

- Sessions button.
- Current session label.
- WebSocket connection state badge: `connecting`, `connected`, `disconnected`.
- New / Kill / Rename controls.
- Shortcut hint.

Controls are disabled when WebSocket is not connected.

### Web Shortcuts

| Key | Action |
|---|---|
| `Alt+Left` / `Alt+Right` | Previous / next session |
| `Ctrl+Shift+S` | Open filterable sessions modal |
| `Ctrl+Shift+R` | Rename focused session |
| `Ctrl+Shift+T` | New session |
| `Ctrl+Shift+W` | Kill focused session, except final remaining session |

Alt-arrow uses both xterm's custom key handler and a capture-phase `window.keydown` handler with `preventDefault()` and `stopPropagation()` so browsers do not navigate history.

### WebSocket Protocol

Every WebSocket message is binary. Byte `0` is a message type tag.

| Direction | Tag | Payload |
|---|---:|---|
| server → client | `0x01` | raw PTY bytes for xterm.js |
| server → client | `0x02` | JSON `MetaMsg` |
| client → server | `0x00` | raw keyboard/input bytes |
| client → server | `0x01` | JSON `ControlMsg` |
| client → server | `0x02` | JSON `ResizeMsg` |

```go
type MetaMsg struct {
    FocusedID int           `json:"focusedId"`
    Sessions  []SessionInfo `json:"sessions"`
}

type SessionInfo struct {
    ID     int    `json:"id"`
    Title  string `json:"title"`
    Exited bool   `json:"exited"`
}

type ControlMsg struct {
    Action string `json:"action"` // "focus" | "new" | "kill" | "rename"
    ID     int    `json:"id"`
    Title  string `json:"title,omitempty"`
}

type ResizeMsg struct {
    ID   int `json:"id"`
    Cols int `json:"cols"`
    Rows int `json:"rows"`
}
```

### WebSocket Implementation Notes

- `gorilla/websocket` requires one writer at a time per connection. `wsClient.write()` serializes writes with a mutex.
- `BroadcastMeta()` sends the current focused ID and session list to all clients.
- `wsClient.sendSnapshot(sessionID)` sends `0x01 + RawSnapshot()`.
- On metadata-driven focus changes, the browser clears xterm, sends an explicit `focus` control, and resizes so the server sends a full snapshot for the focused session.
- The generated HTML template uses `strings.Replace(..., "__WS_QUERY__", wsQuery, 1)` rather than `fmt.Sprintf`, because literal `%` in CSS/JS would corrupt formatting.

---

## Current Implementation Status

Implemented:

- Multi-session local TUI.
- Unix PTY backend.
- Windows ConPTY backend.
- Browser xterm.js UI over WebSocket.
- Echo-based HTTP routing.
- urfave/cli v3 command parsing.
- Session creation, focus, kill, rename.
- Filterable session selector in TUI and web.
- Per-session resize from local terminal and xterm.js.
- TUI color rendering from vt10x cells.
- TUI cursor rendering.
- WebSocket connection state display.
- Token protection for `/ws` when `--token` is set.
- Last-session kill guard.

Not implemented:

- Scrollback navigation controls such as `Shift+PageUp/Down`.
- Background-session activity indicator.
- Config file such as `~/.multicrum.toml`.
- Session persistence / scrollback export.
- Split-pane / tiling layout.
- Mouse support inside panes.
- Static asset packaging for xterm.js; currently loaded from CDN.

---

## Non-Goals For Now

- Agent-specific protocol parsing.
- Split-pane/tmux-style tiling.
- Mouse support inside child panes.
- Persisting child processes across application restarts.
