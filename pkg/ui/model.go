package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"multicrum/pkg/session"
	"multicrum/pkg/ssh_client"
	"multicrum/pkg/transport"
)

// OutputMsg is re-exported here so main.go can use the same type.
type OutputMsg = session.OutputMsg
type ExitMsg = session.ExitMsg

// state holds all mutable model data behind a pointer so Bubble Tea's
// value-copy semantics don't lose mutations between Init/Update/View calls.
type mode int

const (
	modeNormal mode = iota
	modeRenaming
	modeSelecting
	modeHelp
	modeExitPrompt
	modeNewSession
)

type state struct {
	manager      *session.SessionManager
	viewports    map[int]*viewport.Model
	altScreens   map[int]bool // last-seen alt-screen state per session index, for transition detection
	program      *tea.Program
	width        int
	height       int
	errMsg       string
	mode         mode
	renameText   string
	renameCursor int
	selectFilter string
	selectFilterCursor int
	selectCursor int
	selectScroll int // first visible row index when sessions overflow modal height
	selectMoving bool // when true, Up/Down reorders the selected session instead of moving cursor
	selectMoveStart int // original session index of the moving entry, so Esc can revert
	exitPromptID int                // session index waiting for user decision (in exit prompt)
	exitChoice   int                // 0 = respawn, 1 = remove
	newSession   newSessionState    // state for the new-session modal
	mouseCapture bool               // when true, mouse events are forwarded to the child PTY (app-mode)
	sel          selection          // in-progress / completed mouse selection over the focused buffer
	sshClient    *ssh_client.Client // non-nil starts SSH-backed sessions
	onMetaChange func()             // called when sessions are added/removed/focused
	configPath   string             // path used by save/load layout shortcut
	initialCfg   []startupSession   // sessions to spawn on Init (instead of agentCmd)
	statusMsg    string             // transient status line (e.g. config save result)
	renderPending bool              // a render tick is in flight (coalescing PTY bursts)
	scrollbackMode map[int]bool     // sessions currently scrolled up (need full scrollback in viewport)
}

// startupSession is a session entry queued for startup, with its title and
// command tokens. Empty Title means "use command name". CmdLine, when set,
// is the original shell-syntax line and is remembered on the session so
// layout save can round-trip it as-is.
type startupSession struct {
	Title   string
	Cmd     []string
	CmdLine string
}

// Model is the top-level Bubble Tea model.
// Model is the top-level Bubble Tea model.
type Model struct {
	s            *state
	agentCmd     []string
	agentCmdLine string // original shell line for --cmd, when shell-parsing was needed
}

// NewModel constructs the model. agentCmd is the command to run per session.
func NewModel(agentCmd []string, cols, rows int) *Model {
	return NewModelWithSSH(agentCmd, cols, rows, nil)
}

// NewModelWithSSH constructs a model that starts SSH-backed sessions when
// sshClient is non-nil.
func NewModelWithSSH(agentCmd []string, cols, rows int, sshClient *ssh_client.Client) *Model {
	return &Model{
		agentCmd: agentCmd,
		s: &state{
			viewports:  make(map[int]*viewport.Model),
			altScreens: make(map[int]bool),
			scrollbackMode: make(map[int]bool),
			width:     cols,
			height:    rows,
			sshClient: sshClient,
		},
	}
}

// SetAgentCmdLine records the original shell line for the default
// agentCmd. Used so the layout-save shortcut round-trips the original
// string instead of the "bash -c <line>" expansion.
func (m *Model) SetAgentCmdLine(line string) { m.agentCmdLine = line }

// SetConfigPath records the path the layout-save shortcut writes to.
// Empty means "no config path", and the save shortcut becomes a no-op
// with an error status.
func (m *Model) SetConfigPath(path string) {
	m.s.configPath = path
}

// SetInitialSessions queues a list of sessions to spawn at Init time
// instead of the default single agentCmd session. Pass nil to keep the
// default behavior.
func (m *Model) SetInitialSessions(entries []startupSession) {
	m.s.initialCfg = entries
}

// AddInitialSession appends one startup session entry. Useful for
// callers that build the list incrementally.
func (m *Model) AddInitialSession(title string, cmd []string) {
	m.s.initialCfg = append(m.s.initialCfg, startupSession{Title: title, Cmd: cmd})
}

// AddInitialSessionLine appends one startup session entry whose command
// is described by a shell-syntax line. The line is parsed with
// ParseCmdLine and remembered verbatim on the session so subsequent
// layout saves round-trip the original string.
func (m *Model) AddInitialSessionLine(title, line string) {
	cmd := ParseCmdLine(line)
	m.s.initialCfg = append(m.s.initialCfg, startupSession{
		Title:   title,
		Cmd:     cmd,
		CmdLine: line,
	})
}

// SetProgram wires the tea.Program so sessions can send messages back into the
// event loop. Must be called before p.Run().
func (m *Model) SetProgram(p *tea.Program) {
	m.s.program = p
	sendOutput := func(msg session.OutputMsg) { p.Send(msg) }
	sendExit := func(msg session.ExitMsg) { p.Send(msg) }
	cols, rows := paneSize(m.s.width, m.s.height)
	m.s.manager = session.NewManagerWithSSH(cols, rows, sendOutput, sendExit, m.s.sshClient)
}

// StartWSTransport starts the WebSocket transport wired to the session manager.
// Returns the transport (caller doesn't need the writer; TUI stays local-only).
func StartWSTransport(addr, token string, m *Model) (*transport.WSTransport, error) {
	wst, err := transport.NewWSTransport(addr, token)
	if err != nil {
		return nil, err
	}

	// Wire callbacks.
	wst.OnInput = func(sessionID int, data []byte) {
		for _, s := range m.s.manager.Sessions() {
			if s.Index() == sessionID {
				_, _ = s.Write(data)
				return
			}
		}
	}

	wst.SnapOf = func(sessionID int) []byte {
		for _, s := range m.s.manager.Sessions() {
			if s.Index() == sessionID {
				return s.Screen().RawSnapshot()
			}
		}
		return nil
	}

	wst.Sessions = func() []transport.SessionInfo {
		var out []transport.SessionInfo
		for _, s := range m.s.manager.Sessions() {
			out = append(out, transport.SessionInfo{
				ID:     s.Index(),
				Title:  s.Title(),
				Exited: s.Exited(),
			})
		}
		return out
	}

	wst.FocusedID = func() int { return m.s.manager.FocusedIndex() }

	wst.OnResize = func(rm transport.ResizeMsg) {
		m.s.manager.ResizeOne(rm.ID, rm.Cols, rm.Rows)
	}

	wst.OnControl = func(cm transport.ControlMsg) {
		if m.s.program != nil {
			m.s.program.Send(wsControlMsg(cm))
		}
	}

	// Also broadcast PTY output to watching browser clients.
	origSend := m.s.manager.SendOutputFn()
	m.s.manager.SetSendOutput(func(msg session.OutputMsg) {
		origSend(msg)
		wst.SendPTY(msg.Index, msg.Data)
	})

	m.s.onMetaChange = wst.BroadcastMeta

	return wst, nil
}

// wsInputMsg carries raw keystroke bytes from a WebSocket client.
type wsInputMsg []byte

// wsControlMsg carries a session-management command from a browser client.
type wsControlMsg transport.ControlMsg

// renderTickMsg is sent ~at renderInterval after an OutputMsg arrives so the
// visible viewport gets refreshed once per frame rather than once per PTY
// chunk. Coalescing avoids the per-keystroke lag that builds up when a busy
// child (htop, btop, vim, fast-scrolling logs) generates many small writes:
// re-rendering 60 times/s is plenty for visual fluidity, far cheaper than
// running RenderWithScrollback + viewport.SetContent on every read.
type renderTickMsg struct{}

const renderInterval = 16 * time.Millisecond

// Init starts the first session.
func (m Model) Init() tea.Cmd {
	return tea.Sequence(resetTerminalInputModes, func() tea.Msg { return startFirstSessionMsg{} })
}

func resetTerminalInputModes() tea.Msg {
	return tea.RawMsg{Msg: ansi.ResetModeMouseX10 +
		ansi.ResetModeMouseNormal +
		ansi.ResetModeMouseButtonEvent +
		ansi.ResetModeMouseAnyEvent +
		ansi.ResetModeMouseExtSgr +
		ansi.ResetModifyOtherKeys +
		ansi.KittyKeyboard(0, 1) +
		ansi.DisableKittyKeyboard}
}

type startFirstSessionMsg struct{}

// Update handles all incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	s := m.s
	switch msg := msg.(type) {

	case startFirstSessionMsg:
		if len(s.initialCfg) > 0 {
			var failed []string
			for _, entry := range s.initialCfg {
				cmd := entry.Cmd
				if len(cmd) == 0 {
					cmd = m.agentCmd
				}
				sess, err := s.manager.New(cmd)
				if err != nil {
					failed = append(failed, fmt.Sprintf("%v", err))
					continue
				}
				if entry.Title != "" {
					sess.SetTitle(entry.Title)
				}
				if entry.CmdLine != "" {
					sess.SetCmdLine(entry.CmdLine)
				}
			}
			if len(failed) > 0 {
				s.errMsg = "ERROR starting some sessions: " + strings.Join(failed, "; ")
			} else {
				s.errMsg = ""
			}
			if s.manager.Len() == 0 {
				// Fall back to default session so the UI is never empty.
				sess, err := s.manager.New(m.agentCmd)
				if err != nil {
					s.errMsg = fmt.Sprintf("ERROR starting session: %v", err)
					return m, nil
				}
				if m.agentCmdLine != "" {
					sess.SetCmdLine(m.agentCmdLine)
				}
			}
		} else {
			sess, err := s.manager.New(m.agentCmd)
			if err != nil {
				s.errMsg = fmt.Sprintf("ERROR starting session: %v", err)
				return m, nil
			}
			if m.agentCmdLine != "" {
				sess.SetCmdLine(m.agentCmdLine)
			}
			s.errMsg = ""
		}
		s.ensureViewport(s.manager.FocusedIndex(), s.width, s.height)
		s.notifyMeta()
		return m, nil

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		cols, rows := paneSize(s.width, s.height)
		s.manager.ResizeAll(cols, rows)
		for idx, vp := range s.viewports {
			vp.SetWidth(cols)
			vp.SetHeight(rows)
			s.viewports[idx] = vp
		}
		return m, nil

	case wsInputMsg:
		if sess := s.manager.Focused(); sess != nil {
			_, _ = sess.Write([]byte(msg))
		}
		return m, nil

	case wsControlMsg:
		switch msg.Action {
		case "focus":
			s.manager.Focus(msg.ID)
			s.refreshFocused()
			s.notifyMeta()
		case "new":
			s.handleWSNew(m, transport.ControlMsg(msg))
		case "kill":
			if s.manager.Len() > 1 {
				delete(s.viewports, msg.ID)
				s.manager.Kill(msg.ID)
				s.refreshFocused()
				s.notifyMeta()
			}
		case "rename":
			s.manager.Rename(msg.ID, strings.TrimSpace(msg.Title))
			s.notifyMeta()
		case "move":
			s.manager.Move(msg.ID, msg.To)
			s.refreshFocused()
			s.notifyMeta()
		case "save":
			s.saveLayout()
			s.notifyMeta()
		case "exit":
			s.handleWSExit(transport.ControlMsg(msg))
		}
		return m, nil

	case OutputMsg:
		// Only the focused session has a visible viewport, so non-focused
		// sessions don't need a re-render here — their VT state has already
		// been updated by Session.readLoop. Avoiding a full
		// RenderWithScrollback + SetContent per chunk for background tabs
		// keeps the message loop responsive when a background TUI is busy.
		if msg.Index != s.manager.FocusedIndex() {
			return m, nil
		}
		s.ensureViewport(msg.Index, s.width, s.height)
		if s.renderPending {
			return m, nil
		}
		s.renderPending = true
		return m, tea.Tick(renderInterval, func(time.Time) tea.Msg { return renderTickMsg{} })

	case renderTickMsg:
		s.renderPending = false
		idx := s.manager.FocusedIndex()
		s.ensureViewport(idx, s.width, s.height)
		vp := s.viewports[idx]
		for _, sess := range s.manager.Sessions() {
			if sess.Index() == idx {
				alt := sess.Screen().IsAltScreen()
				prev := s.altScreens[idx]
				if alt != prev {
					s.altScreens[idx] = alt
					s.scrollbackMode[idx] = false
					vp.SetContent(sess.Screen().Render())
					vp.GotoBottom()
					s.clearSelection()
				} else if s.scrollbackMode[idx] && !alt {
					// User is browsing scrollback: keep the full content so
					// YOffset stays meaningful relative to it.
					wasAtBottom := vp.AtBottom()
					vp.SetContent(sess.Screen().RenderWithScrollback())
					if wasAtBottom {
						// Caught back up to live tail — drop the heavy
						// scrollback content and resume cheap rendering.
						s.scrollbackMode[idx] = false
						vp.SetContent(sess.Screen().Render())
						vp.GotoBottom()
					}
				} else {
					// Hot path: only the visible screen, bounded cols x rows
					// of work per frame regardless of scrollback depth.
					vp.SetContent(sess.Screen().Render())
					vp.GotoBottom()
				}
				s.viewports[idx] = vp
				break
			}
		}
		return m, nil

	case ExitMsg:
		// Only prompt for the focused session; other exits still mark the tab
		// as exited but don't yank the user away from what they're doing.
		if s.mode == modeNormal && msg.Index == s.manager.FocusedIndex() {
			s.mode = modeExitPrompt
			s.exitPromptID = msg.Index
			s.exitChoice = 0
		}
		s.notifyMeta()
		return m, nil

	case tea.PasteStartMsg, tea.PasteEndMsg:
		// Swallow bracketed-paste boundary markers from the outer terminal so
		// they never reach the child PTY as visible text. We re-wrap the
		// payload ourselves in the PasteMsg handler when the child has
		// bracketed paste enabled.
		return m, nil

	case tea.PasteMsg:
		// The outer terminal delivered bracketed paste content.
		// In editable modal modes, insert it into the focused field.
		// In normal mode, forward it to the focused child PTY, wrapping
		// in ESC[200~..ESC[201~ when the child itself has bracketed-
		// paste mode on. This is what makes Ctrl+Shift+V, middle-click,
		// and Shift+Insert "just work" inside multicrum, and —
		// critically — prevents Bubble Tea from silently consuming the
		// paste (which previously made keys appear "stuck" after a
		// mouse selection followed by a paste).
		text := sanitizePaste(msg.Content)
		switch s.mode {
		case modeRenaming:
			s.renameText, s.renameCursor = insertAt(s.renameText, s.renameCursor, text)
			return m, nil
		case modeNewSession:
			s.appendNewSessionField(text)
			return m, nil
		case modeSelecting:
			s.selectFilter, s.selectFilterCursor = insertAt(s.selectFilter, s.selectFilterCursor, text)
			s.selectCursor = 0
			return m, nil
		case modeNormal:
			// fall through to PTY forwarding below
		default:
			return m, nil
		}
		sess := s.manager.Focused()
		if sess == nil {
			return m, nil
		}
		data := []byte(msg.Content)
		if sess.Screen().BracketedPasteMode() {
			out := make([]byte, 0, len(data)+12)
			out = append(out, "\x1b[200~"...)
			out = append(out, data...)
			out = append(out, "\x1b[201~"...)
			_, _ = sess.Write(out)
		} else {
			_, _ = sess.Write(data)
		}
		return m, nil

	case tea.MouseMsg:
		if s.mode != modeNormal {
			return m, nil
		}
		ev := mouseEventFromMsg(msg)
		debugMouse(ev)
		_, paneRows := paneSize(s.width, s.height)
		// Translate from whole-window coords to pane-relative coords. The
		// pane starts at row 1 (row 0 is the tab bar). The status bar is
		// the row just past paneRows.
		ev.Y -= 1
		inPane := ev.Y >= 0 && ev.Y < paneRows && ev.X >= 0 && ev.X < s.width
		sess := s.manager.Focused()
		if sess == nil {
			return m, nil
		}
		// Mouse-capture mode: forward to the child PTY for apps like btop,
		// htop, vim, lazygit. When disabled, Bubble Tea leaves mouse selection
		// to the outer terminal by not enabling mouse reporting.
		if s.mouseCapture {
			if !inPane {
				return m, nil
			}
			anyButton, motion, anyMotion := sess.Screen().MouseMode()
			if !anyButton {
				return m, nil
			}
			if ev.Action == mouseMotion {
				if ev.Button == tea.MouseNone {
					if !anyMotion {
						return m, nil
					}
				} else if !motion && !anyMotion {
					return m, nil
				}
			}
			if seq := encodeMouseSGR(ev); seq != nil {
				_, _ = sess.Write(seq)
			}
			return m, nil
		}
		return m, nil

	case tea.KeyPressMsg:
		if s.mode == modeHelp {
			s.handleHelpKey(msg)
			return m, nil
		}
		if s.mode == modeRenaming {
			s.handleRenameKey(msg)
			return m, nil
		}
		if s.mode == modeSelecting {
			s.handleSelectKey(msg)
			return m, nil
		}
		if s.mode == modeExitPrompt {
			cmd := s.handleExitPromptKey(m, msg)
			return m, cmd
		}
		if s.mode == modeNewSession {
			cmd := s.handleNewSessionKey(m, msg)
			return m, cmd
		}
		if handled, cmd := s.handleShortcut(m, msg); handled {
			return m, cmd
		}
		// Any other keypress clears a stale transient status toast.
		s.statusMsg = ""
		if msg.Key().Code == tea.KeyEnter {
			idx := s.manager.FocusedIndex()
			if vp, ok := s.viewports[idx]; ok && !vp.AtBottom() {
				vp.GotoBottom()
				s.viewports[idx] = vp
				return m, nil
			}
		}
		if sess := s.manager.Focused(); sess != nil {
			if b := keyToBytes(msg, sess.Screen().AppCursorMode()); len(b) > 0 {
				_, _ = sess.Write(b)
			}
		}
		return m, nil
	}

	return m, nil
}

// View renders the full TUI.
func (m Model) View() tea.View {
	view := tea.NewView(m.viewString())
	view.AltScreen = true
	if m.s.mouseCapture {
		view.MouseMode = tea.MouseModeAllMotion
	}
	view.Cursor = m.cursor()
	return view
}

func (m Model) cursor() *tea.Cursor {
	s := m.s
	if s.mode != modeNormal || s.manager == nil || s.manager.Len() == 0 {
		return nil
	}
	sess := s.manager.Focused()
	if sess == nil {
		return nil
	}
	cur := sess.Screen().Cursor()
	if !cur.Visible {
		return nil
	}
	idx := s.manager.FocusedIndex()
	vp, ok := s.viewports[idx]
	if !ok {
		return nil
	}
	_, rows := paneSize(s.width, s.height)
	var y int
	if s.scrollbackMode[idx] {
		// Viewport content is scrollback + screen; map vt row to viewport line.
		line := len(sess.Screen().BufferLines()) - rows + cur.Y
		y = 1 + line - vp.YOffset()
	} else {
		// Cheap path: viewport content is just the rows-line screen.
		y = 1 + cur.Y
	}
	if y < 1 || y > rows || cur.X < 0 || cur.X >= s.width {
		return nil
	}
	cursor := tea.NewCursor(cur.X, y)
	cursor.Shape = teaCursorShape(cur.Shape)
	cursor.Blink = cur.Blink
	return cursor
}

func teaCursorShape(shape vt.CursorStyle) tea.CursorShape {
	switch shape {
	case vt.CursorUnderline:
		return tea.CursorUnderline
	case vt.CursorBar:
		return tea.CursorBar
	default:
		return tea.CursorBlock
	}
}

func (m Model) viewString() string {
	s := m.s
	if s.errMsg != "" {
		return s.errMsg + "\n\nPress Ctrl+Alt+Q to quit."
	}
	if s.manager == nil || s.manager.Len() == 0 {
		return "Starting…\n"
	}
	return strings.Join([]string{
		m.renderTabBar(),
		m.renderPane(),
		m.renderStatusBar(),
	}, "\n")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *state) notifyMeta() {
	if s.onMetaChange != nil {
		go s.onMetaChange()
	}
}

func (s *state) ensureViewport(idx, w, h int) {
	if _, ok := s.viewports[idx]; !ok {
		s.resetViewport(idx, w, h)
	}
}

func (s *state) resetViewport(idx, w, h int) {
	cols, rows := paneSize(w, h)
	vp := viewport.New(viewport.WithWidth(cols), viewport.WithHeight(rows))
	vp.SetContent("")
	s.viewports[idx] = &vp
}

// enterScrollback loads the full Render+scrollback content into the viewport
// and positions YOffset at the bottom so subsequent ScrollUp moves into
// scrolled-off history. Called lazily when the user actually scrolls; the
// hot path (live tail) keeps only the visible-screen worth of content.
func (s *state) enterScrollback(idx int) {
	sess := s.manager.Focused()
	if sess == nil || sess.Index() != idx {
		for _, c := range s.manager.Sessions() {
			if c.Index() == idx {
				sess = c
				break
			}
		}
	}
	if sess == nil || sess.Screen().IsAltScreen() {
		return
	}
	vp := s.viewports[idx]
	vp.SetContent(sess.Screen().RenderWithScrollback())
	vp.GotoBottom()
	s.scrollbackMode[idx] = true
	s.viewports[idx] = vp
}

func (s *state) scrollFocused(delta int) {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	if delta < 0 && !s.scrollbackMode[idx] {
		s.enterScrollback(idx)
	}
	vp := s.viewports[idx]
	if delta < 0 {
		vp.ScrollUp(-delta)
	} else {
		vp.ScrollDown(delta)
	}
	s.viewports[idx] = vp
}

func (s *state) pageFocused(delta int) {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	if delta < 0 && !s.scrollbackMode[idx] {
		s.enterScrollback(idx)
	}
	vp := s.viewports[idx]
	if delta < 0 {
		vp.PageUp()
	} else {
		vp.PageDown()
	}
	s.viewports[idx] = vp
}

func (s *state) topFocused() {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	if !s.scrollbackMode[idx] {
		s.enterScrollback(idx)
	}
	vp := s.viewports[idx]
	vp.GotoTop()
	s.viewports[idx] = vp
}

func (s *state) bottomFocused() {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	vp := s.viewports[idx]
	vp.GotoBottom()
	if s.scrollbackMode[idx] {
		// Drop the heavy scrollback content now that we're back at the tail.
		for _, sess := range s.manager.Sessions() {
			if sess.Index() == idx {
				vp.SetContent(sess.Screen().Render())
				break
			}
		}
		s.scrollbackMode[idx] = false
	}
	s.viewports[idx] = vp
}

func (s *state) refreshFocused() {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	for _, sess := range s.manager.Sessions() {
		if sess.Index() == idx {
			vp := s.viewports[idx]
			vp.SetContent(sess.Screen().Render())
			vp.GotoBottom()
			s.scrollbackMode[idx] = false
			s.viewports[idx] = vp
			s.maybePromptExited(sess)
			return
		}
	}
}

// maybePromptExited opens the respawn/remove modal if the given session
// is already exited and we're currently in normal mode. Used when the
// user focuses a background session that died while not visible.
func (s *state) maybePromptExited(sess *session.Session) {
	if sess == nil || !sess.Exited() || s.mode != modeNormal {
		return
	}
	s.mode = modeExitPrompt
	s.exitPromptID = sess.Index()
	s.exitChoice = 0
}

// ── shortcuts ─────────────────────────────────────────────────────────────────
//
// All multicrum shortcuts use Ctrl+Alt+<key> so they don't clash with the
// regular terminal/CLI bindings (Ctrl+C, Ctrl+W, Ctrl+T, …) that have to be
// forwarded into the child PTY. Digit shortcuts also accept the bare Alt+<n>
// form because not every terminal emits a distinct sequence for Ctrl+Alt+<n>.
const (
	shortcutHelp         = "alt+`"
	shortcutNew          = "ctrl+alt+t"
	shortcutKill         = "ctrl+alt+w"
	shortcutRename       = "ctrl+alt+r"
	shortcutSessions     = "ctrl+alt+s"
	shortcutSaveLayout   = "ctrl+alt+p"
	shortcutPrev         = "ctrl+alt+left"
	shortcutNext         = "ctrl+alt+right"
	shortcutScrollUp     = "ctrl+alt+up"
	shortcutScrollDown   = "ctrl+alt+down"
	shortcutPageUp       = "ctrl+alt+pgup"
	shortcutPageDown     = "ctrl+alt+pgdown"
	shortcutScrollTop    = "ctrl+alt+home"
	shortcutScrollBottom = "ctrl+alt+end"
	shortcutMouse        = "alt+enter" // Ctrl+Alt+M; terminals encode Ctrl+M as Enter
	shortcutQuit         = "ctrl+alt+q"
)

func (s *state) handleShortcut(m Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.Keystroke()
	switch key {
	case shortcutHelp:
		s.mode = modeHelp
		return true, nil
	case shortcutNew:
		s.openNewSessionModal(m.agentCmd)
		return true, nil
	case shortcutKill:
		if s.manager.Len() > 1 {
			idx := s.manager.FocusedIndex()
			delete(s.viewports, idx)
			s.manager.Kill(idx)
			s.refreshFocused()
			s.notifyMeta()
		}
		return true, nil
	case shortcutRename:
		s.mode = modeRenaming
		if sess := s.manager.Focused(); sess != nil {
			s.renameText = sess.Title()
		}
		s.renameCursor = len([]rune(s.renameText))
		return true, nil
	case shortcutSessions:
		s.mode = modeSelecting
		s.selectFilter = ""
		s.selectFilterCursor = 0
		s.selectCursor = s.manager.FocusedIndex()
		s.selectScroll = 0
		s.selectMoving = false
		return true, nil
	case shortcutSaveLayout:
		s.saveLayout()
		return true, nil
	case shortcutScrollUp:
		s.scrollFocused(-1)
		return true, nil
	case shortcutScrollDown:
		s.scrollFocused(1)
		return true, nil
	case shortcutPageUp:
		s.pageFocused(-1)
		return true, nil
	case shortcutPageDown:
		s.pageFocused(1)
		return true, nil
	case shortcutScrollTop:
		s.topFocused()
		return true, nil
	case shortcutScrollBottom:
		s.bottomFocused()
		return true, nil
	case shortcutMouse:
		s.mouseCapture = !s.mouseCapture
		s.clearSelection()
		if !s.mouseCapture {
			return true, resetTerminalInputModes
		}
		return true, nil
	case shortcutNext:
		s.manager.Focus((s.manager.FocusedIndex() + 1) % s.manager.Len())
		s.refreshFocused()
		s.notifyMeta()
		return true, nil
	case shortcutPrev:
		s.manager.Focus((s.manager.FocusedIndex() - 1 + s.manager.Len()) % s.manager.Len())
		s.refreshFocused()
		s.notifyMeta()
		return true, nil
	case shortcutQuit:
		for _, sess := range s.manager.Sessions() {
			_ = sess.Close()
		}
		return true, tea.Quit
	}
	// Ctrl+Alt+<digit> (or the more portable Alt+<digit>): jump to session.
	if n, ok := digitShortcut(key); ok {
		if n >= 0 && n < s.manager.Len() {
			s.manager.Focus(n)
			s.refreshFocused()
			s.notifyMeta()
		}
		return true, nil
	}
	return false, nil
}

func digitShortcut(s string) (int, bool) {
	for _, prefix := range []string{"alt+ctrl+", "ctrl+alt+"} {
		if strings.HasPrefix(s, prefix) && len(s) == len(prefix)+1 {
			c := s[len(prefix)]
			if c >= '1' && c <= '9' {
				return int(c - '1'), true
			}
		}
	}
	return 0, false
}

func (s *state) handleHelpKey(msg tea.KeyPressMsg) {
	key := msg.Key()
	switch key.Code {
	case tea.KeyEscape, tea.KeyEnter:
		s.mode = modeNormal
	default:
		if msg.Keystroke() == shortcutHelp || (key.Code == 'c' && key.Mod.Contains(tea.ModCtrl)) {
			s.mode = modeNormal
		}
	}
}

func (s *state) handleExitPromptKey(m Model, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Key().Code {
	case tea.KeyLeft, tea.KeyRight, tea.KeyTab:
		s.exitChoice = 1 - s.exitChoice
		return nil
	case tea.KeyEnter:
		return s.resolveExitPrompt(m)
	case tea.KeyEscape:
		// Esc keeps the tab around (effectively "remove later"); close the
		// modal but leave the exited state.
		s.mode = modeNormal
		return nil
	}
	switch msg.String() {
	case "r", "R":
		s.exitChoice = 0
		return s.resolveExitPrompt(m)
	case "x", "X", "k", "K":
		s.exitChoice = 1
		return s.resolveExitPrompt(m)
	case "y", "Y":
		return s.resolveExitPrompt(m)
	case "n", "N", "q", "Q":
		s.mode = modeNormal
	}
	return nil
}

func (s *state) resolveExitPrompt(m Model) tea.Cmd {
	id := s.exitPromptID
	choice := s.exitChoice
	s.mode = modeNormal
	if choice == 0 {
		if err := s.manager.Respawn(id); err != nil {
			s.errMsg = fmt.Sprintf("respawn failed: %v", err)
			return nil
		}
		s.resetViewport(id, s.width, s.height)
		s.notifyMeta()
		return nil
	}
	// Remove: if this is the last session, quit the program entirely.
	if s.manager.Len() <= 1 {
		for _, sess := range s.manager.Sessions() {
			_ = sess.Close()
		}
		return tea.Quit
	}
	delete(s.viewports, id)
	s.manager.Kill(id)
	s.refreshFocused()
	s.notifyMeta()
	return nil
}

func (s *state) handleRenameKey(msg tea.KeyPressMsg) {
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case 'h':
			s.renameText, s.renameCursor = backspaceAt(s.renameText, s.renameCursor)
			return
		case 'u':
			s.renameText = ""
			s.renameCursor = 0
			return
		case 'a':
			s.renameCursor = 0
			return
		case 'e':
			s.renameCursor = len([]rune(s.renameText))
			return
		}
	}
	if key.Text != "" {
		s.renameText, s.renameCursor = insertAt(s.renameText, s.renameCursor, key.Text)
		return
	}
	switch key.Code {
	case tea.KeyEnter:
		s.manager.Rename(s.manager.FocusedIndex(), strings.TrimSpace(s.renameText))
		s.mode = modeNormal
		s.renameText = ""
		s.renameCursor = 0
		s.notifyMeta()
	case tea.KeyEscape:
		s.mode = modeNormal
		s.renameText = ""
		s.renameCursor = 0
	case tea.KeyBackspace:
		s.renameText, s.renameCursor = backspaceAt(s.renameText, s.renameCursor)
	case tea.KeyDelete:
		s.renameText, s.renameCursor = deleteAt(s.renameText, s.renameCursor)
	case tea.KeyLeft:
		if s.renameCursor > 0 {
			s.renameCursor--
		}
	case tea.KeyRight:
		if s.renameCursor < len([]rune(s.renameText)) {
			s.renameCursor++
		}
	case tea.KeyHome:
		s.renameCursor = 0
	case tea.KeyEnd:
		s.renameCursor = len([]rune(s.renameText))
	}
}

func (s *state) handleSelectKey(msg tea.KeyPressMsg) {
	matches := s.filteredSessions()
	key := msg.Key()

	// In moving sub-mode, only navigation and Space/Esc are honored; all
	// other input (typing, ctrl shortcuts) is ignored so the user can't
	// edit the filter mid-move and break the index correspondence.
	if s.selectMoving {
		switch key.Code {
		case tea.KeyEscape:
			// Revert: move the session back to its original index.
			if len(matches) > 0 {
				cur := matches[s.selectCursor]
				s.manager.Move(cur.Index(), s.selectMoveStart)
			}
			s.selectMoving = false
			s.selectCursor = s.selectMoveStart
			s.notifyMeta()
			return
		case tea.KeySpace:
			s.selectMoving = false
			s.notifyMeta()
			return
		case tea.KeyEnter:
			// Commit the move and select the session.
			s.selectMoving = false
			s.mode = modeNormal
			s.selectFilter = ""
			s.selectFilterCursor = 0
			s.selectCursor = 0
			if len(matches) > 0 {
				s.manager.Focus(matches[s.selectCursor].Index())
				s.refreshFocused()
			}
			s.notifyMeta()
			return
		case tea.KeyUp:
			if s.selectCursor > 0 && len(matches) > 0 {
				cur := matches[s.selectCursor]
				prev := matches[s.selectCursor-1]
				s.manager.Move(cur.Index(), prev.Index())
				s.selectCursor--
				s.notifyMeta()
			}
			return
		case tea.KeyDown:
			if s.selectCursor < len(matches)-1 {
				cur := matches[s.selectCursor]
				next := matches[s.selectCursor+1]
				s.manager.Move(cur.Index(), next.Index())
				s.selectCursor++
				s.notifyMeta()
			}
			return
		}
		return
	}

	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case 'h':
			s.selectFilter, s.selectFilterCursor = backspaceAt(s.selectFilter, s.selectFilterCursor)
			s.selectCursor = 0
			return
		case 'u':
			s.selectFilter = ""
			s.selectFilterCursor = 0
			s.selectCursor = 0
			return
		case 'a':
			s.selectFilterCursor = 0
			return
		case 'e':
			s.selectFilterCursor = len([]rune(s.selectFilter))
			return
		}
	}
	if key.Text != "" {
		s.selectFilter, s.selectFilterCursor = insertAt(s.selectFilter, s.selectFilterCursor, key.Text)
		s.selectCursor = 0
		return
	}
	switch key.Code {
	case tea.KeySpace:
		// Begin reordering the currently highlighted session. Only
		// allowed with an empty filter so visible row indices match
		// real session indices.
		if strings.TrimSpace(s.selectFilter) == "" && len(matches) > 0 {
			s.selectMoving = true
			s.selectMoveStart = matches[s.selectCursor].Index()
			return
		}
		s.selectFilter, s.selectFilterCursor = insertAt(s.selectFilter, s.selectFilterCursor, " ")
		s.selectCursor = 0
	case tea.KeyEnter:
		s.mode = modeNormal
		s.selectFilter = ""
		s.selectFilterCursor = 0
		if len(matches) > 0 {
			if s.selectCursor >= len(matches) {
				s.selectCursor = len(matches) - 1
			}
			s.manager.Focus(matches[s.selectCursor].Index())
			s.refreshFocused()
			s.notifyMeta()
		}
		s.selectCursor = 0
	case tea.KeyEscape:
		s.mode = modeNormal
		s.selectFilter = ""
		s.selectFilterCursor = 0
		s.selectCursor = 0
	case tea.KeyUp:
		if len(matches) > 0 {
			s.selectCursor = (s.selectCursor - 1 + len(matches)) % len(matches)
		}
	case tea.KeyDown:
		if len(matches) > 0 {
			s.selectCursor = (s.selectCursor + 1) % len(matches)
		}
	case tea.KeyPgUp:
		if len(matches) > 0 {
			step := s.selectorPageStep()
			s.selectCursor -= step
			if s.selectCursor < 0 {
				s.selectCursor = 0
			}
		}
	case tea.KeyPgDown:
		if len(matches) > 0 {
			step := s.selectorPageStep()
			s.selectCursor += step
			if s.selectCursor >= len(matches) {
				s.selectCursor = len(matches) - 1
			}
		}
	case tea.KeyLeft:
		if s.selectFilterCursor > 0 {
			s.selectFilterCursor--
		}
	case tea.KeyRight:
		if s.selectFilterCursor < len([]rune(s.selectFilter)) {
			s.selectFilterCursor++
		}
	case tea.KeyHome:
		s.selectFilterCursor = 0
	case tea.KeyEnd:
		s.selectFilterCursor = len([]rune(s.selectFilter))
	case tea.KeyBackspace:
		s.selectFilter, s.selectFilterCursor = backspaceAt(s.selectFilter, s.selectFilterCursor)
		s.selectCursor = 0
	case tea.KeyDelete:
		s.selectFilter, s.selectFilterCursor = deleteAt(s.selectFilter, s.selectFilterCursor)
		s.selectCursor = 0
	}
}

func (s *state) filteredSessions() []*session.Session {
	query := strings.ToLower(strings.TrimSpace(s.selectFilter))
	var out []*session.Session
	for _, sess := range s.manager.Sessions() {
		label := fmt.Sprintf("%d %s", sess.Index()+1, sess.Title())
		if query == "" || strings.Contains(strings.ToLower(label), query) {
			out = append(out, sess)
		}
	}
	return out
}

// selectorPageStep returns the number of list rows the session-selector
// modal can display, used as the PgUp/PgDown step size.
func (s *state) selectorPageStep() int {
	const chrome = 4
	step := s.height*80/100 - chrome
	if step < 1 {
		step = 1
	}
	return step
}

func (m Model) renderTabBar() string {
	s := m.s
	focused := s.manager.FocusedIndex()
	sessions := s.manager.Sessions()

	brand := brandStyle.Render("multicrum")
	brandW := lipgloss.Width(brand)

	// Build each tab as a styled string with its measured width.
	tabs := make([]string, len(sessions))
	widths := make([]int, len(sessions))
	for i, sess := range sessions {
		label := fmt.Sprintf("[%d] %s", sess.Index()+1, sess.Title())
		var tab string
		switch {
		case sess.Exited():
			tab = tabExitedStyle.Render(label + " ✗")
		case sess.Index() == focused:
			tab = tabActiveStyle.Render(label)
		default:
			tab = tabInactiveStyle.Render(label)
		}
		tabs[i] = tab
		widths[i] = lipgloss.Width(tab)
	}

	newTab := tabInactiveStyle.Render("[+] Ctrl+Alt+T")
	newTabW := lipgloss.Width(newTab)

	// If the screen is wide enough to show all tabs + the new-tab button,
	// no scrolling needed.
	total := brandW + newTabW
	for _, w := range widths {
		total += w
	}
	if total <= s.width || s.width <= 0 {
		bar := lipgloss.JoinHorizontal(lipgloss.Top, append(tabs, newTab)...)
		if pad := s.width - lipgloss.Width(bar) - brandW; pad > 0 {
			bar += tabBarStyle.Render(strings.Repeat(" ", pad))
		}
		bar += brand
		return bar
	}

	// Otherwise, pick a window [start..end) of tabs around `focused` that
	// fits in `available` cells. Overflow markers ‹/› occupy 2 cells each
	// (with the modal-gap style so the background matches the tab bar).
	left := tabBarStyle.Render(" ‹ ")
	right := tabBarStyle.Render(" › ")
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	// Find a focused-anchored window. We always show the new-tab button
	// at the right; overflow markers appear only when there are tabs
	// outside the visible window on that side.
	focusedPos := -1
	for i, sess := range sessions {
		if sess.Index() == focused {
			focusedPos = i
			break
		}
	}
	if focusedPos < 0 {
		focusedPos = 0
	}

	// Greedily expand outward from the focused tab.
	start, end := focusedPos, focusedPos+1
	// Budget = full width minus the new-tab button. We don't reserve
	// space for overflow markers up-front; they're added only if needed
	// at the end and we then shrink the window if necessary.
	used := widths[focusedPos]
	for {
		grew := false
		if end < len(tabs) && used+widths[end] <= s.width-newTabW-brandW {
			used += widths[end]
			end++
			grew = true
		}
		if start > 0 && used+widths[start-1] <= s.width-newTabW-brandW {
			start--
			used += widths[start]
			grew = true
		}
		if !grew {
			break
		}
	}

	// Reserve room for overflow markers and shrink if needed.
	needLeft := start > 0
	needRight := end < len(tabs)
	reserved := 0
	if needLeft {
		reserved += leftW
	}
	if needRight {
		reserved += rightW
	}
	for used+reserved > s.width-newTabW-brandW && end-start > 1 {
		// Drop the side farther from the focused tab.
		if focusedPos-start > end-1-focusedPos && start < focusedPos {
			used -= widths[start]
			start++
			needLeft = true
		} else if end-1 > focusedPos {
			end--
			used -= widths[end]
			needRight = true
		} else if start < focusedPos {
			used -= widths[start]
			start++
			needLeft = true
		} else {
			break
		}
		reserved = 0
		if start > 0 {
			needLeft = true
		}
		if end < len(tabs) {
			needRight = true
		}
		if needLeft {
			reserved += leftW
		}
		if needRight {
			reserved += rightW
		}
	}

	parts := make([]string, 0, end-start+4)
	if needLeft {
		parts = append(parts, left)
	}
	parts = append(parts, tabs[start:end]...)
	if needRight {
		parts = append(parts, right)
	}
	parts = append(parts, newTab)
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	if pad := s.width - lipgloss.Width(bar) - brandW; pad > 0 {
		bar += tabBarStyle.Render(strings.Repeat(" ", pad))
	}
	bar += brand
	// If even that overflows (a single tab wider than the screen), hard
	// truncate so we never wrap the bar onto a second row.
	if lipgloss.Width(bar) > s.width {
		bar = ansi.Truncate(bar, s.width, "")
	}
	return bar
}

func (m Model) renderPane() string {
	s := m.s
	idx := s.manager.FocusedIndex()
	paneCols, paneRows := paneSize(s.width, s.height)
	var pane string
	if vp, ok := s.viewports[idx]; ok {
		pane = s.renderPaneContent(vp, paneCols, paneRows)
		if s.scrollbackMode[idx] && !vp.AtBottom() {
			pane = overlayScrollIndicator(pane, vp, paneCols, paneRows)
		}
	} else {
		pane = blankPane(paneCols, paneRows)
	}
	switch s.mode {
	case modeHelp:
		return m.overlayBox(pane, m.renderHelpModal())
	case modeRenaming:
		return m.overlayBox(pane, m.renderRenameModal())
	case modeExitPrompt:
		return m.overlayBox(pane, m.renderExitModal())
	case modeNewSession:
		return m.overlayBox(pane, m.renderNewSessionModal())
	case modeSelecting:
		return m.overlayBox(pane, m.renderSessionSelectorModal())
	}
	return pane
}

// renderPaneContent builds the visible pane string directly from the focused
// session's emulator output, bypassing viewport.View()'s lipgloss-based
// wrap/pad pipeline. Going through lipgloss.Style.Width(...).Render(...) inside
// viewport.View() applies ansi.Wrap to the content, and any disagreement
// between the emulator's cell-accurate width and lipgloss's grapheme/width
// measurement (e.g. on braille / box-drawing rows produced by btop) can soft
// wrap one row and shift every subsequent row down by one — that's the
// "dialog buttons are in the wrong place" bug. We pad/truncate to exactly
// paneCols x paneRows ourselves so no wrapping is ever possible.
func (s *state) renderPaneContent(vp *viewport.Model, paneCols, paneRows int) string {
	content := vp.GetContent()
	if content == "" {
		return blankPane(paneCols, paneRows)
	}
	all := strings.Split(content, "\n")
	yoff := vp.YOffset()
	if yoff < 0 {
		yoff = 0
	}
	if yoff > len(all) {
		yoff = len(all)
	}
	end := yoff + paneRows
	if end > len(all) {
		end = len(all)
	}
	out := make([]string, 0, paneRows)
	for i := yoff; i < end; i++ {
		out = append(out, padLine(all[i], paneCols))
	}
	for len(out) < paneRows {
		out = append(out, strings.Repeat(" ", paneCols))
	}
	return strings.Join(out, "\n")
}

// padLine truncates or right-pads an ANSI-styled line to exactly width cells.
func padLine(line string, width int) string {
	w := ansi.StringWidth(line)
	if w > width {
		return ansi.Truncate(line, width, "")
	}
	if w < width {
		return line + strings.Repeat(" ", width-w)
	}
	return line
}

// blankPane returns a paneRows-line string of paneCols spaces each.
func blankPane(paneCols, paneRows int) string {
	row := strings.Repeat(" ", paneCols)
	out := make([]string, paneRows)
	for i := range out {
		out[i] = row
	}
	return strings.Join(out, "\n")
}

// overlayBox paints box centered over pane (both already ANSI-styled).
func (m Model) overlayBox(pane, box string) string {
	s := m.s
	cols, rows := paneSize(s.width, s.height)
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)
	left := max(0, (cols-boxWidth)/2)
	top := max(0, (rows-boxHeight)/2)
	paneLines := strings.Split(pane, "\n")
	boxLines := strings.Split(box, "\n")
	for len(paneLines) < rows {
		paneLines = append(paneLines, strings.Repeat(" ", cols))
	}
	for i, line := range boxLines {
		y := top + i
		if y >= rows {
			break
		}
		paneLines[y] = overlayLine(paneLines[y], line, left, cols)
	}
	return strings.Join(paneLines[:min(len(paneLines), rows)], "\n")
}

func (m Model) renderRenameModal() string {
	s := m.s
	current := ""
	if sess := s.manager.Focused(); sess != nil {
		current = sess.Title()
	}
	width := 44
	rows := []string{
		"Rename session",
		"",
		"Current: " + truncate(current, width-9),
		"New:     " + truncate(renderWithCursor(s.renameText, s.renameCursor), width-9),
		"",
		"Enter save   Esc cancel",
	}
	return padBox(rows, width)
}

func (m Model) renderExitModal() string {
	s := m.s
	title := ""
	cmd := ""
	if sess := s.manager.ByID(s.exitPromptID); sess != nil {
		title = sess.Title()
		cmd = strings.Join(sess.Cmd(), " ")
	}
	width := 50
	respawn := "[ Respawn ]"
	remove := "[ Remove ]"
	if s.exitChoice == 0 {
		respawn = exitChoiceActiveStyle.Render(respawn)
		remove = exitChoiceInactiveStyle.Render(remove)
	} else {
		respawn = exitChoiceInactiveStyle.Render(respawn)
		remove = exitChoiceActiveStyle.Render(remove)
	}
	choices := respawn + modalGapStyle.Render("   ") + remove
	rows := []string{
		"Session exited",
		"",
		"Tab:     " + truncate(title, width-9),
		"Command: " + truncate(cmd, width-9),
		"",
		choices,
		"",
		"←/→ or Tab to choose   Enter confirm",
		"R respawn   X remove   Esc dismiss",
	}
	return padBox(rows, width)
}

func padBox(rows []string, width int) string {
	for _, row := range rows {
		if w := lipgloss.Width(row); w > width {
			width = w
		}
	}
	for i, row := range rows {
		if pad := width - lipgloss.Width(row); pad > 0 {
			rows[i] = row + modalGapStyle.Render(strings.Repeat(" ", pad))
		}
	}
	return helpModalStyle.Render(strings.Join(rows, "\n"))
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

func (m Model) renderHelpModal() string {
	rows := []string{
		"Keyboard shortcuts",
		"",
		"Alt+`                show/close help",
		"Ctrl+Alt+T           new session",
		"Ctrl+Alt+W           kill session",
		"Ctrl+Alt+R           rename session",
		"Ctrl+Alt+S           session selector",
		"Ctrl+Alt+P           save layout to --config file",
		"Ctrl+Alt+Left/Right  previous/next session",
		"Ctrl+Alt+1..9        jump to session",
		"Ctrl+Alt+PgUp/PgDown page scrollback",
		"Ctrl+Alt+Up/Down     line scrollback",
		"Ctrl+Alt+Home/End    top/bottom of scrollback",
		"Ctrl+Alt+M           toggle mouse mode (select ↔ app)",
		"Ctrl+Alt+Q           quit",
		"",
		"Esc or Enter closes this help",
	}
	return padBox(rows, 0)
}

// overlayScrollIndicator paints a "current/total" badge into the top-right
// of pane while the user is scrolled into the scrollback buffer. The
// "current" line number reflects the bottom-most visible line.
func overlayScrollIndicator(pane string, vp *viewport.Model, paneCols, paneRows int) string {
	if paneRows <= 0 || paneCols <= 0 {
		return pane
	}
	total := vp.TotalLineCount()
	if total <= 0 {
		return pane
	}
	cur := vp.YOffset() + vp.Height()
	if cur > total {
		cur = total
	}
	if cur < 1 {
		cur = 1
	}
	badge := scrollIndicatorStyle.Render(fmt.Sprintf("%d/%d", cur, total))
	bw := ansi.StringWidth(badge)
	if bw > paneCols {
		return pane
	}
	lines := strings.Split(pane, "\n")
	if len(lines) == 0 {
		return pane
	}
	left := paneCols - bw
	lines[0] = overlayLine(lines[0], badge, left, paneCols)
	return strings.Join(lines, "\n")
}

// overlayLine composites overlay over base at horizontal cell offset left,
// preserving the ANSI styling of both the base and the overlay. We extract
// the base's cells outside the overlay window via ansi.Cut/Truncate, and
// emit an SGR reset between segments so the overlay's colors don't bleed
// into the right-hand tail of base.
func overlayLine(base, overlay string, left, width int) string {
	baseWidth := ansi.StringWidth(base)
	if baseWidth < width {
		base += strings.Repeat(" ", width-baseWidth)
		baseWidth = width
	}
	overlayWidth := ansi.StringWidth(overlay)
	prefix := ansi.Truncate(base, left, "")
	suffix := ""
	suffixStart := left + overlayWidth
	if suffixStart < baseWidth {
		suffix = ansi.Cut(base, suffixStart, baseWidth)
	}
	const reset = "\x1b[0m"
	return prefix + reset + overlay + reset + suffix
}

// renderSessionSelectorModal builds the centered sessions modal. The list
// scrolls when there are more matches than fit in 80% of the screen height.
func (m Model) renderSessionSelectorModal() string {
	s := m.s
	matches := s.filteredSessions()
	if s.selectCursor >= len(matches) {
		s.selectCursor = max(0, len(matches)-1)
	}
	if s.selectCursor < 0 {
		s.selectCursor = 0
	}

	// Modal sizing: width = min(76, 80% of screen); list height = up to 80%
	// of screen rows minus title + filter + footer overhead.
	width := s.width * 80 / 100
	if width > 76 {
		width = 76
	}
	if width < 30 {
		width = 30
	}

	// Reserve 4 lines: title, filter, blank, footer.
	const chrome = 4
	maxListRows := s.height*80/100 - chrome
	if maxListRows < 3 {
		maxListRows = 3
	}
	listRows := len(matches)
	if listRows == 0 {
		listRows = 1
	}
	if listRows > maxListRows {
		listRows = maxListRows
	}

	// Keep cursor visible: adjust scroll window.
	if s.selectCursor < s.selectScroll {
		s.selectScroll = s.selectCursor
	}
	if s.selectCursor >= s.selectScroll+listRows {
		s.selectScroll = s.selectCursor - listRows + 1
	}
	if max := len(matches) - listRows; s.selectScroll > max && max >= 0 {
		s.selectScroll = max
	}
	if s.selectScroll < 0 {
		s.selectScroll = 0
	}

	rows := []string{
		"Sessions",
		"Filter: " + renderWithCursor(s.selectFilter, s.selectFilterCursor),
		"",
	}
	if len(matches) == 0 {
		rows = append(rows, "  (no sessions match)")
		for i := 1; i < listRows; i++ {
			rows = append(rows, "")
		}
	} else {
		end := s.selectScroll + listRows
		if end > len(matches) {
			end = len(matches)
		}
		for i := s.selectScroll; i < end; i++ {
			sess := matches[i]
			state := "running"
			if sess.Exited() {
				state = "exited"
			}
			marker := "  "
			label := fmt.Sprintf("[%d] %s  %s", sess.Index()+1, sess.Title(), state)
			label = truncate(label, width-4)
			line := marker + label
			if i == s.selectCursor {
				if s.selectMoving {
					line = selectorMovingStyle.Render("↕ " + label)
				} else {
					line = selectorActiveStyle.Render("▶ " + label)
				}
			}
			rows = append(rows, line)
		}
		// Pad to listRows so footer position stays fixed.
		for i := end - s.selectScroll; i < listRows; i++ {
			rows = append(rows, "")
		}
	}

	footer := "↑/↓ move   Space reorder   Enter select   Esc cancel"
	if s.selectMoving {
		footer = "↑/↓ reorder   Space drop   Enter commit & select   Esc revert"
	}
	if len(matches) > listRows {
		footer = fmt.Sprintf("%d/%d   %s", s.selectCursor+1, len(matches), footer)
	}
	rows = append(rows, "", footer)

	return padBox(rows, width)
}

func (m Model) renderStatusBar() string {
	s := m.s
	focused := s.manager.FocusedIndex()
	var status string
	for _, sess := range s.manager.Sessions() {
		if sess.Index() == focused {
			state := "running"
			if sess.Exited() {
				state = "exited"
			}
			cols, rows := paneSize(s.width, s.height)
			mouseTag := "mouse:select"
			if s.mouseCapture {
				mouseTag = "mouse:app"
			}
			status = fmt.Sprintf(" session %d │ %s │ %dx%d │ %s ", focused+1, state, cols, rows, mouseTag)
			break
		}
	}
	if s.mode == modeRenaming {
		help := helpStyle.Render(" Rename: " + renderWithCursor(s.renameText, s.renameCursor) + "  Enter save  Esc cancel")
		left := statusKeyStyle.Render(status)
		if pad := s.width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeSelecting {
		help := helpStyle.Render(" Sessions — see modal")
		left := statusKeyStyle.Render(status)
		if pad := s.width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeHelp {
		help := helpStyle.Render(" Help: Esc/Enter close")
		left := statusKeyStyle.Render(status)
		if pad := s.width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeExitPrompt {
		help := helpStyle.Render(" Session exited — choose action in modal")
		left := statusKeyStyle.Render(status)
		if pad := s.width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	help := helpStyle.Render(" Alt+` help")
	if s.statusMsg != "" {
		help = helpStyle.Render(" " + s.statusMsg + " ")
	}
	left := statusKeyStyle.Render(status) + help
	if pad := s.width - lipgloss.Width(left); pad > 0 {
		left += statusBarStyle.Render(strings.Repeat(" ", pad))
	}
	return left
}
