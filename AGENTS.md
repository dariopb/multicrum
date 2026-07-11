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
- `Ctrl+Alt+Q`: owner TUI opens server quit confirmation; attached clients use `Ctrl+Alt+Q` to detach without killing sessions. Plain `Ctrl+Q` is forwarded.
- Right-click a session tab or connection tab to open a modal-styled context menu anchored inside the pane next to it. Its focus, rename, move, and remove items must route through `handleSelectKey` / `handleConnectionsKey` with synthetic `Enter`, `R`, `M`, or `Delete` keys after selecting the clicked target; do not add a duplicate action implementation. `View()` uses `AllMotion` while `modeContextMenu` is open so the item beneath the pointer is highlighted with `selectorActiveStyle`; restore select mode by closing the menu. Right-clicking a second tab while a menu is open must replace it with the new tab's menu, not merely dismiss it.
- Left-click `[+] Ctrl+Alt+T` in the tab bar or the Help label in the status bar to dispatch the existing new-session or help shortcut. Their bounds are recorded as `newSessionHitbox` and `helpHitbox` while rendering; do not create duplicate action paths.

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

`multicrum --server NAME` attaches to an existing per-user local server named `NAME` (default `default`). If no live server exists, the visible process starts a detached `--owner` daemon, waits for the endpoint, then attaches as a client. Unix uses `$XDG_RUNTIME_DIR/multicrum/<server>.sock` or `/tmp/multicrum-$UID/multicrum/<server>.sock`; Windows uses a loopback TCP address stored in `%LOCALAPPDATA%\multicrum\<server>.addr`.

Lifecycle commands are `multicrum list` / `multicrum ls`, `multicrum status --server NAME`, and `multicrum stop --server NAME`. Status/list output includes PID, socket path, and startup settings such as command, config, WebSocket address, token presence (redacted as `token=set`), and SSH options.

The runtime state is a tree: server → connections → sessions. `state.connections` stores `connectionState` objects, each with its own `SessionManager`, viewport map, alt-screen map, and scrollback-mode map. `state.syncActiveConnectionFields()` keeps legacy `state.manager`/`state.viewports` aliases pointed at the active connection so older UI paths keep working.

Config files now save `connections[].sessions[]`; legacy top-level `sessions` are loaded into a `default` connection by `Config.Normalize()`. `cmdline` entries are parsed into startup argv with `ui.ParseCmdLine` while preserving the original `cmdline` for round-trip saves. SSH-backed sessions include an `ssh` block with target, port, key, default-key/agent flags, known-host settings, and remote command persistence.

Attach clients stream raw terminal input to the owner through length-prefixed frames and receive mirrored owner TUI output. `SIGWINCH` from Unix attach clients is forwarded as resize frames. Windows attach/server uses loopback TCP with the same frame protocol.

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

## Rendering Performance (hot path)

Bubble Tea calls `Model.View()` **after every message** (see bubbletea `eventLoop` → `p.render`), then flushes to the terminal on a separate 60fps ticker. So the cost that matters for perceived lag is not the terminal write but how often/expensively `View()` rebuilds the frame string when a child spews output. Two mechanisms keep this bounded — do not remove either:

- **Output-notification coalescing.** The session read loop applies bytes to the `VTScreen` synchronously, then notifies the program. Each connection carries an `outputPending atomic.Bool`: the read-loop callback only enqueues a `connectionOutputMsg` when it was previously clear (`Swap(true)` was false), so a burst of small child writes collapses into a single Bubble Tea message. The flag is **re-armed in `renderTickMsg`** (after the frame is drawn), not in the output handler, so every write during a frame window coalesces into one notification and output tops out at ~60 `View()` rebuilds/sec regardless of how chatty the child is. Background/non-focused output re-arms immediately in the handler (it isn't rendered) so those connections keep notifying. Nothing is lost by dropping intermediate notifications because the bytes are already in the `VTScreen`.
- **Pane render memoization.** `renderPaneContent` is a pure function of `(viewport content, YOffset, paneCols, paneRows, wrap)`; `state.paneCache` memoizes its output keyed on exactly those inputs. Between frames the same pane is rebuilt on every redundant `View()` with identical inputs, so the cache turns an ~87µs split/pad pass into a cheap key comparison (measured `View()` ~126µs → ~43µs). The pure worker is `renderPaneContentUncached`; the cache needs no manual invalidation because any change to the inputs misses the key. Overlays (selection/search/scroll indicator) are applied by `renderPane` *after* the cached content, so they are never stale.

Benchmarks live in `pkg/ui/bench_render_test.go` (`BenchmarkView`, `BenchmarkScreenRender`, `BenchmarkRenderPaneContent[Cached]`); the cache-correctness guard is `TestRenderPaneContentCacheMatchesUncached`.

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

### Resize reflow (`VTScreen.Resize`)

The underlying emulator does **not** reflow: shrinking width crops the right edge of every row and the dropped characters are lost, so a later widen leaves blank cells. `VTScreen.Resize` therefore reflows the visible screen by replaying raw history at the new size — `RIS` (`ESC c`) resets the emulator, then `reflowTail` replays just enough of the tail (a screenful of logical lines, bounded by a byte cap) to reconstruct the `rows x cols` visible grid. Replaying the *full* 256 KiB history per resize tick was ~95 ms/session and far too slow; the bounded tail is sub-millisecond. Reflow is skipped on the alternate screen (those apps redraw themselves on SIGWINCH). Terminal replies are suppressed during the replay so replayed DSR/CPR queries don't leak responses to the child. `WindowSizeMsg` re-renders the focused viewport after the reflow, otherwise the viewport keeps text wrapped at the old width and shows stale "ghost" rows until the next PTY output.

### Logical-line capture and bare carriage returns

`captureLogicalLines` builds the TUI-only logical scrollback from the byte stream. A `CR` immediately followed by `LF` is one CRLF line break (the usual PTY line ending). A **lone** `CR` returns to column 0 so following bytes overwrite the current line in place (prompt redraws, progress bars) and must **not** commit a new logical line — otherwise every prompt redraw appends a duplicate, blank-looking prompt line that is absent from the live emulator screen. The decision is deferred via `pendingCR` so a CR at a write-buffer boundary is resolved by the next byte.

### Scrollback pane soft-wrap alignment

The TUI scrollback pane (`renderPaneContent`) windows content with the viewport's `YOffset`, which the viewport computes in **soft-wrapped** row space. `RenderWithScrollback()` returns full, un-wrapped logical lines (so copy/selection keep real line breaks), so in scrollback mode `renderPaneContent` must first expand each logical line into fixed-width wrapped rows (`softWrapRows`, mirroring `viewport.softWrap`) before applying `YOffset`. Without this the offset counts wrapped rows while the renderer indexes logical lines and scrolling drifts/duplicates rows. The live/alt-screen path keeps strict no-wrap behavior so cell-accurate grids (btop dialogs) never shift.

### Mouse reporting must survive the startup input-mode reset and client attach

Two things must be true for wheel/selection to work from launch:

1. `Model.Init()` runs `resetTerminalInputModes` (a `tea.RawMsg`) to clear stray legacy/keyboard input modes. It must **not** reset the button-event (1002), any-event (1003) or SGR-extended (1006) mouse modes; `Model.View()` owns those via `MouseMode` (`CellMotion` in select mode, `AllMotion` in app mode) and the renderer enables them on its first flush. Since the reset runs after that flush, resetting the mouse modes there disables reporting.

2. **The bigger issue with a detached owner:** the owner's renderer emits the mouse-enable CSI only once (first flush / on a `MouseMode` change), and that first flush happens with no attach client connected — so the enable is written to the socket fan-out and lost. A later-attaching client's terminal therefore never receives `?1002h/?1006h`, and mouse stays dead until the user toggles mouse mode (`Ctrl+Alt+M`) twice (which forces a `MouseMode` change → re-emit). Fix: on client attach the owner sends `LocalClientCountMsg`; the handler detects an increased count and re-asserts `state.mouseEnableSequence()` (matching the current capture mode) via a `tea.RawMsg`, alongside `tea.ClearScreen`. Keep mouse-enable owned by `MouseMode` for changes, but re-assert it on every new attach.

Regression tests: `TestResetTerminalInputModesKeepsMouseReporting` and `TestMouseEnableSequenceMatchesMode` in `pkg/ui/mouse_test.go`.

### Mouse selection source must match the displayed pane

Live-mode selection maps mouse rows onto the **viewport's rendered `Render()` snapshot**, while scrollback-mode selection maps onto `VTScreen.BufferLines()` (the logical scrollback — what `RenderWithScrollback()` paints). `state.selectionLines(idx, vp)` picks the source by `scrollbackMode[idx]`, and `state.paneRowBase` is just the viewport `YOffset` in both modes. Do **not** map live-mode rows onto the `BufferLines()` tail: `BufferLines()` is the logical history and only coincides with the visible screen while output is scrolling at the bottom. After a `clear` (fresh top-aligned screen with blank padding below the cursor) the two diverge, so selecting the first on-screen lines returned unrelated logical-history text until enough output realigned them. Also do not read live selection from the current emulator cells: resize/reflow or child redraw output can advance them between the last render tick and the mouse event, making a visibly populated row copy newer or blank content. `overlaySelection` and `selectionText` must both read from `selectionLines`. Regression tests: `TestVisibleLinesMatchScreenAfterClear` and `TestLiveSelectionUsesRenderedViewportSnapshot`.

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
