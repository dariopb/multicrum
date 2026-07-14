# Configurable Connection Layout: Bottom Bar and Left Rail

## Goal

Make the TUI connection switcher configurable:

- **`bottom`** (default): retain the current one-line connection pills in the
  bottom status bar.
- **`left`**: render a connection rail on the left side of the terminal.

The selected placement is a persisted layout preference. The `left` rail has
this required structure:

```text
Multicrum                    # row 0; pink background across the rail
server: default              # row 1; active server name
                              # row 2; blank
[1] default                  # rows 3-4; one connection item
    2 sessions
[2] work                     # rows 5-6; next connection item
    4 sessions
...
```

Every connection item occupies exactly two terminal rows. The active connection
style covers both rows over the full rail width, not only the label text. The
rail uses the terminal-pane dark background; its active selection uses a
subdued dark-grey background. Its only pink background is the `Multicrum`
title; the main tab bar omits that duplicate title in left mode.

The default connection-rail width is **15 terminal cells**. The dim vertical
separator runs from the top to the bottom of the terminal between the rail and
the main TUI column. Dragging this separator resizes the rail directly. The
separator consumes its own cell and is not part of either pane's usable width.

## Persistence and Compatibility

### Current format clarification

The project currently saves layout/configuration as YAML through
`pkg/config.Config` and `gopkg.in/yaml.v3`; it does **not** currently save a
JSON file. YAML is a superset of JSON, so JSON-form input remains accepted by
the parser, but `config.Save` emits YAML.

Add this field to `config.Config`:

```go
ConnectionLayout string `yaml:"connectionLayout,omitempty" json:"connectionLayout,omitempty"`
ConnectionRailWidth int `yaml:"connectionRailWidth,omitempty" json:"connectionRailWidth,omitempty"`
```

Allowed normalized values:

| Value | Meaning |
|---|---|
| `bottom` | Current horizontal bottom connection/status layout. |
| `left` | New vertical left connection rail. |

`Normalize()` must map empty and unrecognized values to `bottom`. That makes
all existing config files preserve their current rendering.

`state.saveLayout()` must populate `ConnectionLayout` and
`ConnectionRailWidth`; `Model.SetConfigConnections` must receive and apply both
before the initial render/managers are started. Missing/non-positive widths use
the 15-cell UI default.

If literal JSON output, rather than JSON-compatible configuration input, is a
product requirement, make that a separate config-format migration decision.
This work should preserve the existing YAML save format and persist the new
field there.

### Changing the placement

Add **`L`** to the existing Connections modal:

- Toggle `bottom` ↔ `left`.
- Update the modal footer/help text to advertise it.
- Recompute geometry, resize all session PTYs/viewports, clear stale hitboxes,
  and redraw immediately.
- Persist it through the existing save-layout workflow (`Ctrl+Alt+P`), matching
  the existing layout persistence model instead of silently writing config on
  every toggle.

The browser UI is out of scope for the first implementation: it keeps its
current HTML/JavaScript layout. The preference controls the local Bubble Tea
TUI only.

## Geometry Refactor (Required Before Rendering the Rail)

The current implementation assumes a fixed horizontal layout:

- `paneSize(totalCols, totalRows)` returns `(totalCols, totalRows-2)`.
- `renderTabBar()` is always global row `0`.
- `renderStatusBar()` is always global row `paneRows+1`.
- Mouse handling subtracts one row from `ev.Y`.
- Session/connection controls use one-dimensional horizontal `mouseHitbox`
  ranges.

Those assumptions must be removed rather than patched with additional
`if layout == "left"` branches.

### Introduce a single layout geometry model

Create a `layoutGeometry` value derived from:

```go
type connectionLayout string

const (
    connectionLayoutBottom connectionLayout = "bottom"
    connectionLayoutLeft   connectionLayout = "left"
)

type rect struct {
    X, Y int
    Width, Height int
}

type layoutGeometry struct {
    Screen         rect
    ConnectionRail rect // zero-sized for bottom layout
    TabBar         rect
    Pane           rect
    StatusBar      rect
}
```

`state.geometry()` is the only source of coordinate truth. It must clamp
degenerate terminal dimensions so every rect remains non-negative and the pane
keeps at least one cell in each dimension.

Geometry definitions:

| Layout | Connection region | Tab bar | Pane | Status bar |
|---|---|---|---|---|
| `bottom` | Global row `height-1`, embedded in the status bar | `(0, 0, width, 1)` | `(0, 1, width, height-2)` | `(0, height-1, width, 1)` |
| `left` | `(0, 0, railWidth, height)` | `(railWidth+dividerWidth, 0, width-railWidth-dividerWidth, 1)` | `(railWidth+dividerWidth, 1, width-railWidth-dividerWidth, height-1)` | omitted |

Use a named default width of `15`, a minimum rail width of `8`, and a named
`connectionRailDividerWidth` constant set to `1`. The requested persisted width
is clamped at render time so the main pane retains at least
`minimumMainPaneWidth`; temporary clamping does not overwrite the saved
preference.

Replace `paneSize` call sites with geometry accessors:

```go
geom := s.geometry()
cols, rows := geom.Pane.Width, geom.Pane.Height
```

Retain a compatibility wrapper only temporarily, then remove it after all
callers use `layoutGeometry`.

### Coordinate conversion API

Add helpers and prohibit direct coordinate arithmetic outside them:

```go
func (g layoutGeometry) ScreenToPane(x, y int) (paneX, paneY int, ok bool)
func (g layoutGeometry) PaneToScreen(x, y int) (screenX, screenY int)
func (g layoutGeometry) Contains(r rect, x, y int) bool
```

This is especially important for:

- local select-mode drag/copy and scrollback wheel events;
- `encodeMouseSGR` sent to child TUIs in app mouse mode;
- cursor placement in `Model.cursor()`;
- context-menu positioning;
- tab/status/rail hit testing;
- modal and scroll-indicator overlays.

The child PTY must always receive pane-relative coordinates and pane dimensions,
not full-terminal coordinates. In `left` mode this means subtracting
`railWidth` from mouse X before encoding SGR mouse events.

### Replace one-dimensional hitboxes

Replace the horizontal `mouseHitbox{Start, End, Index}` with a rectangle-based
control model:

```go
type mouseHitbox struct {
    Bounds rect
    Index  int
    Action hitboxAction // session, connection, new-session, help, connections
}
```

Alternatively preserve separate typed slices, but each must use `rect` bounds.
Do not retain separate magic row checks such as `ev.Y == 0` or
`ev.Y == paneRows+1`.

This allows:

- session tabs to remain one-row rectangles;
- bottom connection pills to remain one-row rectangles;
- left-rail connection items to be two-row rectangles;
- Help, `conn`, and new-session controls to remain directly clickable;
- right-click context menu dispatch to identify the clicked entity uniformly.

## Rendering Plan

### Frame composition

Replace `strings.Join([]string{tabBar, pane, statusBar}, "\n")` as the
top-level frame composition path. It cannot place a full-height left rail.

Build fixed-height row slices for each rendered region:

1. Render the main column as `TabBar` and `Pane`, all sized from `geom`.
2. In `bottom` mode, compose exactly as today, with the connection region
   embedded in the status row.
3. In `left` mode, render `geom.ConnectionRail.Height` rail rows, a one-cell
   dim vertical separator for every terminal row, and the corresponding
   main-column row.

All region renderers must return exactly their rectangle dimensions. Continue
using ANSI-aware width handling (`ansi.Cut`, `ansi.Truncate`, `overlayLine`,
and `lipgloss.Width`) so colors do not shift columns.

### Left rail renderer

Implement `renderConnectionRail(geom layoutGeometry) []string`:

1. Render `Multicrum` at row zero using the pink brand background, padded to
   the entire rail width. Row one shows `server: <name>` using the regular
   rail style, followed by one blank rail-background row.
2. For each connection, allocate two rows:
   - line 1: `[%d] <name>` truncated to the rail interior width;
   - line 2: indented `<N> sessions`.
3. Render the active connection with a full-width two-row active style. Render
   inactive entries with the inactive connection style over their full widths.
4. Register one two-row connection hitbox per entry.
5. If entries exceed available rows, define a deterministic overflow policy:
   initially show a focused/active-centered window with top/bottom overflow
   indicators. Do not silently draw past the status/pane boundary.

Use the existing connection modal for filtering/reordering rather than adding
rail scrolling behavior in the first pass.

Render the divider with a distinct dim style (for example, a dark-gray
foreground on the rail/main background) and exactly one visible `│` cell per
row. It must stay continuous behind the entries, tab bar, and pane.

### Existing controls

- Session tabs stay in the main column's tab bar.
- The `[+] Ctrl+Alt+T` control stays in that tab bar and receives a rectangle
  hitbox.
- The main-column status bar is omitted in `left` mode, so the child terminal
  uses the final main-column row. The rail's final row contains a clickable
  `New` control on the left (dispatching the existing Ctrl+Alt+C
  new-connection action) and clickable `Alt+\`` Help shortcut aligned right.
- Centered dialogs are centered within `geom.Pane`, not the full terminal and
  not the rail.
- Context menus are anchored from screen coordinates, then clamped inside
  `geom.Screen`. A right-clicked rail item opens at the pointer over the full
  composed frame rather than being translated into pane coordinates; a
  session-tab menu remains below the tab bar.
- Left- or right-clicking the `Multicrum` title opens the global actions menu:
  Help, session/connection creation and selectors, Toggle Mouse with its current
  mode, Save Layout, client-only Detach, and server-wide Quit.

## Resize, PTY, Viewport, and Rendering-State Changes

On `tea.WindowSizeMsg` and a runtime layout toggle:

1. Recompute `layoutGeometry`.
2. Resize every `SessionManager` with `geom.Pane.Width` × `geom.Pane.Height`.
3. Resize every viewport to the same pane dimensions.
4. Re-render the focused session into the viewport, retaining the current
   scrollback/live-mode semantics.
5. Re-anchor the viewport cursor with geometry-derived dimensions.
6. Reset/rebuild all hitboxes and invalidate `paneCache` if its dimensions no
   longer match.
7. Keep the current last-resizer-wins rules for WebSocket viewers. A local TUI
   layout toggle is a local resize and must push its pane dimensions to the
   newly visible focused session.

Audit every current `paneSize` consumer, including:

- `WindowSizeMsg`, `refreshFocused`, respawn/new-session paths, and all
  `ResizeAll`/`ResizeOne` calls;
- viewport initialization/reset, scrollback entry, and cursor anchoring;
- `renderPane`, `renderPaneContent`, selection overlays, search overlays, and
  scroll indicators;
- `Model.cursor`;
- TUI mouse dispatch and SGR forwarding;
- Web control/resize paths where they assume a TUI pane size;
- context-menu bounds and modal placement.

## Input and Interaction Behavior

### Mouse

- Left-click a rail connection item focuses that connection.
- Right-click a rail item opens its existing context menu (focus, rename,
  move, remove) targeting that connection.
- Left- or right-click the `Multicrum` title to open global actions. Detach
  closes only the attach client that generated the click; Quit retains the
  owner/server confirmation.
- The active/hover semantics remain unchanged: context menu uses all-motion
  while open; regular select/app mouse behavior continues inside the pane.
- A click in the rail must never begin a terminal selection or forward a
  child mouse event.
- Pressing and dragging the divider resizes the rail immediately; release ends
  the resize gesture without forwarding mouse input to the child.

### Keyboard

Existing connection shortcuts retain their behavior regardless of placement:

- Ctrl+Alt+O opens Connections.
- Ctrl+Alt+C creates a connection.
- Ctrl+Alt+[/] switches connections.
- In left mode, Ctrl+Alt+Up/Down switches to the previous/next connection
  instead of scrolling the focused session.
- The Connections modal's new `L` action toggles placement.

### Accessibility/minimum-size behavior

Define and test a minimum usable main pane width. If the terminal is narrower
than `railWidth + minimumPaneWidth`, clamp the rail first; if it still cannot
fit, fall back to `bottom` for that render without changing the persisted
preference, and show a concise status message explaining the temporary
fallback.

## Tests

### Config and persistence

- Missing `connectionLayout` normalizes to `bottom`.
- Valid `bottom` and `left` round-trip through `config.Save`/`Load`.
- Unknown layout value normalizes to `bottom`.
- Save-layout writes the selected placement.
- Initial config application sets the TUI placement before first render.

### Geometry

- Table tests for both layouts across normal, narrow, and short terminal
  dimensions.
- Pane dimensions, global rects, and screen-to-pane conversions are exact.
- `left` never gives the PTY/viewport full terminal width.
- Temporary narrow-screen fallback does not overwrite the configured choice.

### Rendering and hit testing

- Bottom layout remains byte/visual compatible with current tab/status
  placement.
- Left rail has the pink title, server row, one blank row, then exact two-row
  entries on the terminal-pane background with a subdued dark-grey active
  selection.
- Active style covers both full-width entry rows.
- Rail hitboxes cover both rows and do not overlap pane hitboxes.
- Tab/new-session/Help/`conn` click targets use geometry-derived rectangles.
- Dialogs remain within the main pane. Context menus are composed over the
  full frame so rail and title menus stay at their screen-coordinate pointer.

### Regression coverage

Run the existing resize-reflow, scrollback, mouse selection, context-menu,
render-cache, and attach-client tests under both layouts where applicable.
Add explicit tests that:

- SGR mouse coordinates in left mode are pane-relative;
- live selection remains aligned after a resize and after a layout toggle;
- context menu replacement works when right-clicking different rail items;
- the title menu exposes Detach separately from Quit and Detach targets only
  the input-source attach client;
- switching layout while scrolled or with a modal open does not retain stale
  viewport content/hitboxes;
- plain Ctrl+Q remains forwarded and Ctrl+Alt+Q detaches an attached client.

## Implementation Sequence

1. Add `connectionLayout` config parsing, normalization, save/load plumbing,
   and tests (default stays `bottom`).
2. Introduce `rect`/`layoutGeometry`; migrate resize, viewport, and PTY size
   callers before changing visuals.
3. Convert hitboxes and mouse coordinate conversion to geometry-based
   rectangles; preserve bottom-layout tests.
4. Refactor frame composition and modal/context overlay placement to use
   geometry.
5. Implement the left rail renderer and its two-row hitboxes.
6. Add the Connections-modal `L` toggle and save-layout integration.
7. Test bottom and left layouts through resize, scrolling, selection,
   contexts, modal placement, attach, and WebSocket coexistence.
8. Update README, `spec.md`, and `AGENTS.md` after behavior is verified.

## Acceptance Criteria

- Existing configs render exactly as bottom layout without migration.
- `left` survives save/load and starts in the saved placement.
- The left rail begins with pink `Multicrum`, the active server row, exactly
  one blank row, then two-row connection items.
- Active connection styling spans both full-width item rows.
- A focused faulted session retains active styling while showing its `✗`.
- PTY dimensions, viewport dimensions, mouse selection, SGR mouse forwarding,
  cursor placement, overlays, and dialogs all use main-pane geometry.
- Switching placement is immediate, safely resizes sessions, and leaves no
  stale rows, selection offsets, or hitboxes.
- Bottom layout continues to pass all existing rendering/input regressions.
