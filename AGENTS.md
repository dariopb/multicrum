# AGENTS.md

## Project Overview

**multicrum** is a Go terminal multiplexer for multiple persistent CLI/agent sessions. It has two synchronized frontends:

- A local Bubble Tea TUI.
- An optional browser UI served over WebSocket with embedded xterm.js.

Each session is backed by a real PTY/ConPTY and a `vt10x` screen model. The app treats child CLIs as black boxes and forwards terminal input/output rather than using any agent-specific protocol.

## Commands

```bat
# Build all packages
C:\go124\go\bin\go.exe build ./...

# Run tests
C:\go124\go\bin\go.exe test ./...

# Run local TUI only (default command is bash)
C:\go124\go\bin\go.exe run . --cmd "bash"

# Run with WebSocket/xterm.js UI
C:\go124\go\bin\go.exe run . --cmd "bash" --ws :9999 --token mytoken

# Browser URL
# http://localhost:9999/
# http://localhost:9999/?token=mytoken
```

`go.cmd` exists but may not be found through the shell PATH in this environment. The reliable command used during recent work is the explicit Go binary: `C:\go124\go\bin\go.exe`.

## Architecture & Data Flow

```text
main.go
  └─ ui.NewModel(cmd, cols, rows)
       └─ session.SessionManager
            └─ session.Session (one per tab/session)
                 ├─ platform Start(): Unix PTY or Windows ConPTY
                 ├─ readLoop goroutine
                 │    ├─ VTScreen.Write(raw PTY bytes)
                 │    └─ SendOutput(OutputMsg) → Bubble Tea program
                 └─ VTScreen
                      ├─ vt10x visible screen buffer
                      └─ rawHistory for WebSocket replay

ui.Model Update loop
  ├─ OutputMsg          → viewport content = sess.Screen().RenderWithScrollback()
  ├─ tea.KeyMsg         → TUI shortcuts/scrollback or keyToBytes() → focused PTY
  ├─ tea.WindowSizeMsg  → resize all sessions + viewports
  └─ wsControlMsg       → browser-driven focus/new/kill/rename

transport.WSTransport (--ws)
  ├─ /ws  binary WebSocket protocol
  └─ /    generated HTML/JS xterm.js client from indexHTML()
```

## Package Structure

| Package | Role |
|---|---|
| `main.go` | Entry point, flags, Bubble Tea program, optional WS startup |
| `ui/` | Bubble Tea model, key routing, tab/status rendering, viewport lifecycle |
| `session/` | Session lifecycle, manager, `VTScreen` rendering/replay buffer |
| `console/` | Windows ConPTY implementation, build-tagged for Windows |
| `transport/` | Local no-op transport interface and WebSocket/xterm.js transport |

## Keyboard / UI Behavior

### TUI

- `Alt+Backtick`: show/close centered help modal listing shortcuts.
- `Ctrl+T`: create a new session.
- `Ctrl+W`: kill focused session, but never kill the final remaining session.
- `Ctrl+Shift+R`: rename focused session inline in the status bar.
- `Ctrl+S`: open the filtered session selector; type to filter, arrows move, Enter selects.
- `Ctrl+Left` / `Ctrl+Right`: previous/next session.
- `Alt+1..9`: jump to session N.
- `Ctrl+Y` / `Ctrl+PgUp`: page up through local TUI scrollback.
- `Ctrl+PgDown`: page down through local TUI scrollback.
- `Ctrl+Up` / `Ctrl+Down`: scroll local TUI scrollback one line.
- `Ctrl+Home` / `Ctrl+End`: jump to top/bottom of local TUI scrollback.
- `Ctrl+Q`: quit and close sessions.

Shortcut keys are consumed before the default key forwarding path. Do not add a TUI shortcut after the `default` PTY forwarding case in `ui/model.go`.

### Web UI

- Browser UI is embedded inside `transport/websocket.go:indexHTML()`; there are no static assets in `web/`.
- `Ctrl+Shift+S`: open sessions modal with type-to-filter.
- `Ctrl+Shift+R`: rename focused session.
- `Ctrl+Shift+T`: new session.
- `Ctrl+Shift+W`: kill focused session, blocked when only one session remains.
- `Ctrl+Left` / `Ctrl+Right`: previous/next session.

Ctrl-arrow handling uses both xterm's custom key handler and a capture-phase `window.keydown` listener with `preventDefault()`/`stopPropagation()`.

## Session Naming

`Session.Title()` returns a user override when set, otherwise the process command name. Rename support is implemented via:

- `Session.title` and `Session.SetTitle()` in `session/session.go`.
- `SessionManager.Rename()` in `session/manager.go`.
- TUI `modeRenaming` state in `ui/model.go`.
- Web control message `ControlMsg{Action:"rename", ID, Title}`.

Metadata broadcasts are required after rename so both TUI and browser labels stay synchronized.

## WebSocket Protocol

Every WebSocket binary message uses byte 0 as a type tag.

| Direction | Tag | Payload |
|---|---:|---|
| server → client | `0x01` | raw PTY bytes for xterm.js |
| server → client | `0x02` | JSON `MetaMsg` (`focusedId`, `sessions`) |
| client → server | `0x00` | raw keystrokes |
| client → server | `0x01` | JSON `ControlMsg` (`focus`, `new`, `kill`, `rename`) |
| client → server | `0x02` | JSON `ResizeMsg` |

`MetaMsg` replaced the earlier raw `[]SessionInfo` metadata shape. The browser still accepts the old array shape for compatibility. Metadata includes server-focused session ID; the browser adopts it, clears xterm, sends a focus control to request a full snapshot, and then resizes.

### WebSocket gotchas

- `gorilla/websocket` allows only one concurrent writer per connection. `wsClient.write()` holds a per-client mutex. All writes to a browser connection must go through it.
- `BroadcastMeta()` sends metadata to all clients; it does not send PTY snapshots.
- Snapshots are sent through `wsClient.sendSnapshot(sessionID)`, using `VTScreen.RawSnapshot()`.
- Avoid sending a new-session snapshot from the WS read goroutine before Bubble Tea has processed the `new` control; it can replay the old focused session. Let metadata-driven focus request the snapshot.
- The generated HTML must not use `fmt.Sprintf` over the whole CSS/JS template because literal `%` in CSS/JS corrupts formatting. Use `strings.Replace(..., "__WS_QUERY__", wsQuery, 1)`.

## Viewport and Session Index Gotchas

Sessions are indexed 0-based and `Kill()` reindexes remaining sessions. `state.viewports` is keyed by session index, so stale viewport reuse is easy after kills/recreates.

Rules:

- Always call `ensureViewport()` before reading a viewport.
- On new session creation, call `resetViewport()` for the new focused index, not just `ensureViewport()`, so a reused index cannot show stale content.
- Deleting a session removes its current viewport key; remaining sessions are reindexed by `SessionManager.Kill()`.
- Killing the final remaining session is intentionally blocked in both manager/UI paths.

## VTScreen Rendering and Replay

`VTScreen` maintains two separate representations:

- `vt10x.Terminal` for visible screen state.
- `rawHistory` capped at 256 KiB for WebSocket replay.
- TUI-only semantic scrollback capped at 10000 rendered lines.

Important details:

- `vt10x.String()` returns characters only and drops color attributes. The TUI must use `VTScreen.Render()`, which walks `vt10x.Cell()` and emits ANSI SGR sequences so Bubble Tea can show colors.
- xterm.js receives raw PTY bytes from `SendPTY()` and snapshots from `RawSnapshot()`.
- `rawHistory` is not a full semantic terminal state; it is replay bytes for xterm. Keep the cap in mind when changing replay behavior.
- `Render()` emits ANSI foreground/background color sequences from vt10x cell attributes. If changing vt10x or rendering libraries, verify both local TUI colors and browser xterm colors.
- `RenderWithScrollback()` prepends captured scrolled-off lines to the current vt10x screen for local TUI scrolling/copying. WebSocket replay remains raw xterm bytes.

## Platform-specific PTY

- Unix: `session/start_unix.go` uses `github.com/creack/pty` and sets `TERM=xterm-256color`.
- Windows: `session/start_windows.go` uses `console.WinConsole`, which wraps ConPTY via `golang.org/x/sys/windows`.
- Keep PTY-specific code in build-tagged files. Shared `session.Session` only assumes an `io.ReadWriteCloser` and resize callback.

## Bubble Tea State Pattern

`Model` contains a pointer to `state`. This is intentional because Bubble Tea models are value-copied on `Update`/`View`. Put mutable state in `state`, not directly in `Model`, unless you are deliberately okay with value-copy behavior.

Current modes in `ui/model.go`:

- `modeNormal`
- `modeRenaming`
- `modeSelecting`
- `modeHelp`

Input handling checks these modes before normal shortcut/PTT forwarding.

## Testing / Verification

After changes, run both:

```bat
C:\go124\go\bin\go.exe test ./...
C:\go124\go\bin\go.exe build ./...
```

There are currently no committed tests, so these mainly verify compilation across packages/build tags available on the current platform.

## Dependencies of Note

- `github.com/charmbracelet/bubbletea v1.3.10` — TUI event loop.
- `github.com/charmbracelet/bubbles/viewport` — local viewport rendering.
- `github.com/charmbracelet/lipgloss` — TUI styling.
- `github.com/hinshun/vt10x` — virtual terminal screen model.
- `github.com/creack/pty` — Unix PTY.
- `golang.org/x/sys/windows` — Windows ConPTY syscalls.
- `github.com/gorilla/websocket` — browser transport.

## Known Not Implemented

- Background-session activity indicator.
- Config file such as `~/.multicrum.toml`.
- Session persistence / scrollback export.
- Split-pane / tiling layout.
