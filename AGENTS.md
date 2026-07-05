# AGENTS.md

## Project Overview

**multicrum** is a Go terminal multiplexer for multiple persistent CLI/agent sessions. It has two synchronized frontends:

- A local Bubble Tea TUI.
- An optional browser UI served over WebSocket with embedded xterm.js.

Each session is backed by a real PTY/ConPTY and a `vt10x` screen model. The app treats child CLIs as black boxes and forwards terminal input/output rather than using any agent-specific protocol.

## Commands

```bash
# Build the runnable app (preferred build command)
go build -v ./cmd/multicrum/

# Run tests
go test ./...

# Attach to/create the default server (default command is bash)
# First run starts a detached owner daemon and attaches this terminal.
go run ./cmd/multicrum/ --cmd "bash"

# Run with WebSocket/xterm.js UI
# The detached owner records ws/token presence for status/list output.
go run ./cmd/multicrum/ --cmd "bash" --ws :9999 --token mytoken

# Lifecycle commands
go run ./cmd/multicrum/ list
go run ./cmd/multicrum/ status --server default
go run ./cmd/multicrum/ stop --server default

# Browser URL
# http://localhost:9999/
# http://localhost:9999/?token=mytoken
```

Go is expected to be available in `PATH`. Use `go build -v ./cmd/multicrum/` for build verification; do not use hard-coded local Go installation paths.

## Commit Messages

For broad commits, use a detailed multi-bullet commit body summarizing the major areas changed, without being too verbose.

## Architecture & Data Flow

```text
cmd/multicrum/main.go
  ├─ parse CLI flags (--server/--config/--cmd/--ssh/--ws)
  ├─ localserver.TryAttach(server socket)
  │    └─ attach client: raw local TTY ↔ Unix-socket frames ↔ owner-rendered TUI
  ├─ no live server: spawn detached --owner process, wait for socket, attach
  ├─ lifecycle commands: list/status/stop show PID/socket/startup settings
  └─ owner process
       ├─ config.Load() → ui.Model.SetConfigConnections()
       ├─ ui.InputMux(attached-client input; nil stdin when daemonized)
       ├─ localserver.ListenWithSettings()
       ├─ Bubble Tea program
       │    └─ ui.Model
       │         └─ server state
       │              └─ connections[]
       │                   └─ session.SessionManager
       │                        └─ session.Session
       │                             ├─ Unix PTY / Windows ConPTY / SSH PTY
       │                             ├─ readLoop → VTScreen.Write(raw bytes)
       │                             └─ OutputMsg/ExitMsg → Bubble Tea program
       └─ optional transport.WSTransport (--ws)
            ├─ /ws binary WebSocket protocol
            └─ / generated embedded xterm.js UI

ui.Model Update loop
  ├─ connectionOutputMsg/OutputMsg → active viewport render tick
  ├─ connectionExitMsg/ExitMsg     → exited-session respawn/remove modal
  ├─ tea.KeyPressMsg               → global shortcuts first, then modal/normal handlers
  ├─ tea.WindowSizeMsg             → resize active connection sessions
  ├─ wsResizeMsg                   → resize one browser-viewed session
  └─ wsControlMsg                  → browser session/connection controls
```

## Package Structure

| Package | Role |
|---|---|
| `cmd/multicrum/` | CLI entry point, flags, Bubble Tea program, optional WS startup |
| `cmd/ptyrec/` | Diagnostic PTY recorder/replay tool |
| `pkg/ui/` | Bubble Tea model, connection/session dialogs, global shortcuts, viewport lifecycle, layout save, input mux |
| `pkg/session/` | Session lifecycle, manager, move/respawn/rename/resize, `VTScreen` rendering/replay buffer |
| `pkg/ssh_client/` | SSH target resolution and remote PTY backend |
| `pkg/console/` | Unix PTY and Windows ConPTY implementations |
| `pkg/config/` | YAML config tree with server → connections → sessions plus legacy migration |
| `pkg/transport/` | Local no-op transport interface and WebSocket/xterm.js transport with embedded assets |
| `pkg/localserver/` | Named local server Unix-socket attach protocol, frame codec, fan-out writer |

## Keyboard / UI Behavior

### TUI

- `Alt+Backtick`: show/close centered help modal listing shortcuts.
- `Ctrl+Alt+T`: open the new-session modal in the active connection. This is a global shortcut and must work from modal states, including exited-session prompts.
- `Ctrl+Alt+O`: open the connections modal (focus, create, rename, move/reorder, filter, remove).
- `Ctrl+Alt+E`: open the connections modal on the active connection; press `R` to rename.
- `Ctrl+Alt+C`: quick-create a new connection/workspace and focus it.
- `Ctrl+Alt+[` / `Ctrl+Alt+]`: previous/next connection/workspace.
- `Ctrl+Alt+W`: kill focused session, but never kill the final remaining session.
- `Ctrl+Alt+R`: open the sessions dialog on the active session; press `R` to rename.
- `Ctrl+Alt+S`: open the sessions dialog (focus, create, rename, move/reorder, filter, remove).
- `Ctrl+Alt+Left` / `Ctrl+Alt+Right`: previous/next session inside the active connection.
- `Alt+1..9`: jump to session N inside the active connection.
- `Ctrl+Y` / `Ctrl+PgUp`: page up through local TUI scrollback.
- `Ctrl+PgDown`: page down through local TUI scrollback.
- `Ctrl+Up` / `Ctrl+Down`: scroll local TUI scrollback one line.
- `Ctrl+Home` / `Ctrl+End`: jump to top/bottom of local TUI scrollback.
- `Ctrl+Alt+Q`: owner TUI opens server quit confirmation; attached clients use `Ctrl+Q`/`Alt+Ctrl+Q` to detach without killing sessions.

Global shortcuts (`Ctrl+Alt+T`, `Ctrl+Alt+Left/Right`, `Ctrl+Alt+[`/`]`, `Ctrl+Alt+Q`) are centralized in `state.handleGlobalShortcut` and run before modal-specific handlers. Do not duplicate these bindings inside individual modal handlers; that caused regressions where exited-session dialogs blocked connection/session switching or quit.

Shortcut keys are consumed before the default key forwarding path. Do not add a TUI shortcut after the default PTY forwarding case in `pkg/ui/model.go`.

### Web UI

- Browser UI is embedded inside `pkg/transport/websocket.go:indexHTML()`; there are no static assets in `web/`.
- `Alt+S`: open sessions dialog.
- `Alt+N`: new session.
- `Alt+K`: kill focused session when safe.
- `Alt+R`: open sessions dialog on the focused session; press `R` to rename.
- `Alt+P`: save layout.
- `Alt+M`: toggle web mouse mode.
- `Alt+,`: settings.
- `Ctrl+Alt+Left` / `Ctrl+Alt+Right`: previous/next session.
- `Ctrl+Alt+[` / `Ctrl+Alt+]`: previous/next connection.
- `Ctrl+Alt+O`: open connections dialog.
- `Ctrl+Alt+E`: open connections dialog on active connection; press `R` to rename.
- `Ctrl+Alt+C`: new connection prompt.

Web shortcut handling uses both xterm's custom key handler and a capture-phase `window.keydown` listener with `preventDefault()`/`stopPropagation()` where needed.

## Long-running server and connections

`multicrum --server NAME` attaches to an existing per-user local Unix socket server named `NAME` (default `default`). If no live server exists, the visible process starts a detached `--owner` daemon, waits for the socket, then attaches as a client. The socket path is generated by `pkg/localserver.SocketPath`, using `$XDG_RUNTIME_DIR/multicrum/<server>.sock` or `/tmp/multicrum-$UID/multicrum/<server>.sock` fallback.

Lifecycle commands are `multicrum list` / `multicrum ls`, `multicrum status --server NAME`, and `multicrum stop --server NAME`. Status/list output includes PID, socket path, and startup settings such as command, config, WebSocket address, token presence (redacted as `token=set`), and SSH options.

The runtime state is a tree: server → connections → sessions. `state.connections` stores `connectionState` objects, each with its own `SessionManager`, viewport map, alt-screen map, and scrollback-mode map. `state.syncActiveConnectionFields()` keeps legacy `state.manager`/`state.viewports` aliases pointed at the active connection so older UI paths keep working.

Config files now save `connections[].sessions[]`; legacy top-level `sessions` are loaded into a `default` connection by `Config.Normalize()`. `cmdline` entries are parsed into startup argv with `ui.ParseCmdLine` while preserving the original `cmdline` for round-trip saves. SSH-backed sessions include an `ssh` block with target, port, key, default-key/agent flags, known-host settings, and remote command persistence.

Attach clients stream raw terminal input to the owner through length-prefixed frames and receive mirrored owner TUI output. `SIGWINCH` from attach clients is forwarded as resize frames. Local socket support is Unix-only for now; Windows stubs return a clear unsupported error.

## Session Naming

`Session.Title()` returns a user override when set, otherwise the process command name. Rename support is implemented via:

- `Session.title` and `Session.SetTitle()` in `pkg/session/session.go`.
- `SessionManager.Rename()` in `pkg/session/manager.go`.
- TUI sessions dialog (`modeSelecting`, press `R`).
- Web sessions dialog / control message `ControlMsg{Action:"rename", ID, Title}`.

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

## Resize Synchronization Across Viewers

A session can be observed by multiple viewers at different sizes — the local TUI, one or more attached clients (over the local Unix-socket server), and one or more browsers connected to `--ws`. The PTY/ConPTY only has one size, so multicrum uses a "last-resizer-wins" rule: whichever surface most recently issued a resize is the authoritative size.

Crucially, **the client that performs a switch (session or connection) is the "active" one and becomes authoritative** — its size is applied to the newly focused session, and other viewers merely display whatever fits. This must NOT degrade into "whichever client's automatic re-fit lands last wins":

- Server (`wsControlMsg` `"focus"`) only calls `refreshFocused()` (which re-pushes the local TUI pane size) when the focus **actually changes**. A browser that is merely *adopting* a focus another client initiated sends a `focus` control to update its `client.sessionID` + get a snapshot, but since `msg.ID` is already focused, the server skips the resize so it can't clobber the initiator's dimensions.
- Browser (`indexHTML` metadata-adopt block) only calls `sendResize()` when **this** client initiated the switch. Session focus is initiated locally via `focusSession()` (which resizes directly and never reaches the adopt block), so a session-only change in the adopt block is always an adopter and must not resize. A connection switch does not know the new `focusedId` until the broadcast arrives, so its initiator resizes in the adopt block — gated by the `weInitiatedSwitch` flag (set in `control()` for `focusConnection`/`prevConnection`/`nextConnection`/`newConnection`/`removeConnection`).

`SessionManager` caches a `cols/rows` pair updated by `ResizeAll` and used by `New`/`Respawn`. `ResizeOne` (used for browser-driven resizes) does **not** update that cache, so it is intentionally per-session. To keep the active viewer consistent across focus/respawn boundaries:

- `tea.WindowSizeMsg` calls `ResizeAll(cols,rows)` on **every** connection's manager (not just the active one), so background connections don't drift to stale init sizes when later focused or when `New`/`Respawn` runs there.
- `state.refreshFocused()` calls `ResizeOne(idx, paneCols, paneRows)` on the now-focused session so the local viewer sees the session at its own dimensions immediately. When the switch was browser-initiated, the browser's follow-up `sendResize()` then overrides with the browser's size (the browser is the active client).
- `state.resolveExitPrompt()` (TUI) and `handleWSExit()` (WS) call `ResizeOne(id, paneCols, paneRows)` after `manager.Respawn(id)` so the freshly started PTY is in sync with the viewer that asked for the respawn. The browser JS exit handler also issues `sendResize()` after sending `action:"exit", choice:"respawn"`, so a browser-initiated respawn sizes the new PTY to the browser xterm.

When adding new code paths that mutate the session set (focus change, new session, respawn, move, connection switch), always re-push the active viewer's pane size to the affected session. Otherwise viewers will diverge into "two/three buffer representations" — different layouts in TUI vs. browser, broken popup placement, and offset cursors.

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

### Emulator quirks worked around in `VTScreen.Write`

Inbound PTY bytes pass through `translateSCORC` before reaching the emulator. Any future emulator workaround should be added to the same helper (or alongside it) and documented in this section.

- **Missing SCORC handler in `github.com/charmbracelet/x/vt`**. The emulator registers a handler for **DECRC** (`ESC 8`, Restore Cursor) but **not for SCORC** (`ESC[u`, the CSI form). Apps that draw popups with the `ESC[s` / `ESC[u` pair — notably **btop's kill/signal confirmation dialog** — would silently no-op on every restore, so each subsequent dialog line drifted right and down from where the previous one ended. `VTScreen.Write` translates the canonical 3-byte `ESC[u` to `ESC 8` before feeding the emulator (`translateSCORC` in `pkg/session/vtscreen.go`). The browser `rawHistory` keeps the original bytes because xterm.js handles SCORC natively. **Only the bare 3-byte form is rewritten** — `ESC[<params>u` is the kitty keyboard protocol report and must not be touched, or it will corrupt input replies.

  Reproduction / diagnosis tool: `cmd/ptyrec` records every byte a child PTY emits and can replay the capture through `vt.Emulator` at a chosen size, isolating emulator bugs from UI-layer bugs. Use it whenever a terminal app renders correctly in the browser/xterm but wrong in the local TUI.

### Stripping bubbletea keyboard-enhancement escapes from stdout

Bubble Tea v2 unconditionally emits `CSI > 4 ; 2 m` (modifyOtherKeys),
`CSI ? u` (request Kitty keyboard), and `CSI = / > / < ... u` (Kitty
keyboard set/push/pop) on program start, exit, and alt-screen
transitions. In a multiplexer these bytes leak into child PTYs and
break input handling for children that don't speak those protocols.

`pkg/ui.NewKeyboardStripWriter` wraps an `io.Writer` and removes those
specific CSI sequences while passing everything else through
untouched, including CSI `u` reports without the `=`/`>`/`<`/`?`
prefix (which are Kitty keyboard *reports* coming back the other way
and must never be stripped). `cmd/multicrum/main.go` wraps `os.Stdout`
with it via `tea.WithOutput(...)`.

Library consumers should do the same when constructing their own
`tea.Program`. This removes the need to maintain a patched bubbletea
fork.

## Platform-specific PTY

- Unix: `pkg/session/start_unix.go` uses `github.com/creack/pty` and sets `TERM=xterm-256color`.
- Windows: `pkg/session/start_windows.go` uses `console.WinConsole`, which wraps ConPTY via `golang.org/x/sys/windows`.
- Keep PTY-specific code in build-tagged files. Shared `session.Session` only assumes an `io.ReadWriteCloser` and resize callback.

## Bubble Tea State Pattern

`Model` contains a pointer to `state`. This is intentional because Bubble Tea models are value-copied on `Update`/`View`. Put mutable state in `state`, not directly in `Model`, unless you are deliberately okay with value-copy behavior.

Current modes in `pkg/ui/model.go`:

- `modeNormal`
- `modeRenaming` (legacy direct rename; current shortcuts use sessions dialog)
- `modeSelecting` (sessions dialog)
- `modeHelp`
- `modeExitPrompt`
- `modeNewSession`
- `modeConnections`
- `modeQuitConfirm`
- `modeDeleteConfirm`

Input handling runs `handleGlobalShortcut` before mode-specific handlers, then normal PTY forwarding only in `modeNormal`.

## Testing / Verification

After changes, run both:

```bash
go test ./...
go build -v ./cmd/multicrum/
```

There are unit tests in config, localserver, SSH parsing/session helpers, keyboard stripping, command-line parsing, and UI config loading. These commands verify both tests and compilation across packages/build tags available on the current platform.

## Dependencies of Note

- `charm.land/bubbletea/v2` — TUI event loop.
- `charm.land/bubbles/v2/viewport` — local viewport rendering.
- `charm.land/lipgloss/v2` — TUI styling.
- `github.com/hinshun/vt10x` — virtual terminal screen model.
- `github.com/creack/pty` — Unix PTY.
- `golang.org/x/sys/windows` — Windows ConPTY syscalls.
- `github.com/gorilla/websocket` — browser transport.

## Known Not Implemented

- Background-session activity indicator.
- Session persistence / scrollback export.
- Split-pane / tiling layout.
