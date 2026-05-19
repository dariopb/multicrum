# multiAgent — Current Specification

## Overview

multiAgent is a Go terminal multiplexer for running multiple persistent CLI/agent sessions. It provides:

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
| `--cmd` | `bash` | Command to run for each new session; parsed with `strings.Fields` |
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
multiAgent/
├── main.go                  # urfave/cli parsing, Bubble Tea startup, optional web startup
├── console/
│   ├── console_unix.go      # Unix PTY wrapper, implements io.ReadWriteCloser + Resize + Done
│   └── console_windows.go   # Windows ConPTY wrapper, implements io.ReadWriteCloser + Resize + Done
├── session/
│   ├── session.go           # Session lifecycle, read loop, rename state
│   ├── manager.go           # SessionManager create/focus/kill/rename/resize
│   ├── start_unix.go        # wires Session.Start to console.NewUnixConsole
│   ├── start_windows.go     # wires Session.Start to console.NewWinConsole
│   └── vtscreen.go          # vt10x-backed screen, ANSI TUI render, raw replay buffer
├── ui/
│   ├── model.go             # Bubble Tea model, modes, session actions, WS callback integration
│   ├── keys.go              # key constants and KeyMsg→PTY byte conversion
│   ├── layout.go            # pane sizing
│   └── styles.go            # lipgloss styles
├── transport/
│   ├── transport.go         # generic transport interface
│   ├── local.go             # no-op local transport
│   └── websocket.go         # Echo server, Gorilla WS protocol, embedded xterm.js HTML
├── cmd/testconpty/          # Windows-only ConPTY smoke test behind build tag
├── AGENTS.md
├── spec.md
└── go.mod
```

---

## Core Components

### `Session`

`session.Session` owns one running child process and its terminal state.

Current responsibilities:

- Store command args, display title override, exited state, PTY read/write handle, resize callback.
- Start through platform-specific `Start(cols, rows)` implementation.
- `Write(p []byte)` forwards keyboard bytes to PTY/ConPTY.
- `Resize(cols, rows)` resizes both `VTScreen` and underlying PTY/ConPTY.
- `Close()` kills/closes the process backend.
- `readLoop()` reads raw bytes, updates `VTScreen`, and emits `OutputMsg`.
- `Title()` returns renamed title if set, otherwise command name.

### `console.UnixConsole`

Unix backend implemented in `console/console_unix.go`:

- Uses `creack/pty.StartWithSize`.
- Sets `TERM=xterm-256color`.
- Implements `Read`, `Write`, `Resize`, `Close`, and `Done`.
- Used by `session/start_unix.go`.

### `console.WinConsole`

Windows backend implemented in `console/console_windows.go`:

- Creates ConPTY pipes with `os.Pipe()`.
- Calls `windows.CreatePseudoConsole`.
- Builds `STARTUPINFOEX` via `windows.NewProcThreadAttributeList`.
- Uses `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` (`0x00020016`).
- Calls `windows.CreateProcess`.
- Implements `Read`, `Write`, `Resize`, `Close`, and `Done`.
- Used by `session/start_windows.go`.

### `VTScreen`

`VTScreen` keeps two representations:

- `vt10x.Terminal` for current visible screen state.
- `rawHistory` capped at 256 KiB for xterm.js replay snapshots.

Important behavior:

- `Write(p)` feeds vt10x and appends to replay history.
- `Render()` walks vt10x cells and emits ANSI SGR sequences so the Bubble Tea viewport can show colors.
- `Render()` also draws the cursor as an explicit black-on-white cell when vt10x reports it visible.
- `RawSnapshot()` returns a copy of replay bytes for browser clients.

### `SessionManager`

`SessionManager` owns the session slice and focused index.

Implemented operations:

- `New(cmd []string)` creates and focuses a new session.
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

### TUI Key Bindings

| Key | Action |
|---|---|
| `Ctrl+T` | New session |
| `Ctrl+W` | Kill focused session, except final remaining session |
| `Ctrl+R` | Rename focused session |
| `Ctrl+S` | Open filtered session selector |
| `Alt+Left` / `Alt+Right` | Previous / next session |
| `Alt+1..9` | Jump to session N |
| `Ctrl+Q` | Close all sessions and quit |
| Other handled keys | Converted by `keyToBytes()` and forwarded to focused PTY |

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

The web UI is served from `transport/websocket.go`:

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
- Config file such as `~/.multiagent.toml`.
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
