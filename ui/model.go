package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"multicrum/session"
	"multicrum/ssh_client"
	"multicrum/transport"
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
	selectFilter string
	selectCursor int
	exitPromptID int                // session index waiting for user decision (in exit prompt)
	exitChoice   int                // 0 = respawn, 1 = remove
	newSession   newSessionState    // state for the new-session modal
	mouseCapture bool               // when true, mouse events are forwarded to the child PTY (app-mode)
	sel          selection          // in-progress / completed mouse selection over the focused buffer
	sshClient    *ssh_client.Client // non-nil starts SSH-backed sessions
	onMetaChange func()             // called when sessions are added/removed/focused
}

// Model is the top-level Bubble Tea model.
type Model struct {
	s        *state
	agentCmd []string
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
			width:     cols,
			height:    rows,
			sshClient: sshClient,
		},
	}
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
		_, err := s.manager.New(m.agentCmd)
		if err != nil {
			s.errMsg = fmt.Sprintf("ERROR starting session: %v", err)
			return m, nil
		}
		s.errMsg = ""
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
		case "exit":
			s.handleWSExit(transport.ControlMsg(msg))
		}
		return m, nil

	case OutputMsg:
		s.ensureViewport(msg.Index, s.width, s.height)
		vp := s.viewports[msg.Index]
		for _, sess := range s.manager.Sessions() {
			if sess.Index() == msg.Index {
				wasAtBottom := vp.AtBottom()
				alt := sess.Screen().IsAltScreen()
				prev := s.altScreens[msg.Index]
				vp.SetContent(sess.Screen().RenderWithScrollback())
				if alt != prev {
					// Alt-screen entry/exit: scrollback disappears or returns.
					// Reset YOffset so popups/dialogs (e.g. btop's Yes/No)
					// render against a fresh, full-pane grid and prior
					// main-screen selections don't index into stale rows.
					vp.GotoBottom()
					s.clearSelection()
					s.altScreens[msg.Index] = alt
				} else if msg.Index == s.manager.FocusedIndex() && wasAtBottom {
					vp.GotoBottom()
				}
				s.viewports[msg.Index] = vp
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
		// The outer terminal delivered bracketed paste content. Forward it to
		// the focused child PTY, wrapping in ESC[200~..ESC[201~ when the child
		// itself has bracketed-paste mode on. This is what makes Ctrl+Shift+V,
		// middle-click, and Shift+Insert "just work" inside multicrum, and —
		// critically — prevents Bubble Tea from silently consuming the paste
		// (which previously made keys appear "stuck" after a mouse selection
		// followed by a paste).
		if s.mode != modeNormal {
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
	line := len(sess.Screen().BufferLines()) - rows + cur.Y
	y := 1 + line - vp.YOffset()
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

func (s *state) scrollFocused(delta int) {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
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
	vp := s.viewports[idx]
	vp.GotoTop()
	s.viewports[idx] = vp
}

func (s *state) bottomFocused() {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	vp := s.viewports[idx]
	vp.GotoBottom()
	s.viewports[idx] = vp
}

func (s *state) refreshFocused() {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	for _, sess := range s.manager.Sessions() {
		if sess.Index() == idx {
			vp := s.viewports[idx]
			vp.SetContent(sess.Screen().RenderWithScrollback())
			vp.GotoBottom()
			s.viewports[idx] = vp
			return
		}
	}
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
		return true, nil
	case shortcutSessions:
		s.mode = modeSelecting
		s.selectFilter = ""
		s.selectCursor = s.manager.FocusedIndex()
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
			r := []rune(s.renameText)
			if len(r) > 0 {
				s.renameText = string(r[:len(r)-1])
			}
			return
		case 'u':
			s.renameText = ""
			return
		}
	}
	if key.Text != "" {
		s.renameText += key.Text
		return
	}
	switch key.Code {
	case tea.KeyEnter:
		s.manager.Rename(s.manager.FocusedIndex(), strings.TrimSpace(s.renameText))
		s.mode = modeNormal
		s.renameText = ""
		s.notifyMeta()
	case tea.KeyEscape:
		s.mode = modeNormal
		s.renameText = ""
	case tea.KeyBackspace:
		r := []rune(s.renameText)
		if len(r) > 0 {
			s.renameText = string(r[:len(r)-1])
		}
	}
}

func (s *state) handleSelectKey(msg tea.KeyPressMsg) {
	matches := s.filteredSessions()
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case 'h':
			r := []rune(s.selectFilter)
			if len(r) > 0 {
				s.selectFilter = string(r[:len(r)-1])
				s.selectCursor = 0
			}
			return
		case 'u':
			s.selectFilter = ""
			s.selectCursor = 0
			return
		}
	}
	if key.Text != "" {
		s.selectFilter += key.Text
		s.selectCursor = 0
		return
	}
	switch key.Code {
	case tea.KeyEnter:
		if len(matches) > 0 {
			if s.selectCursor >= len(matches) {
				s.selectCursor = len(matches) - 1
			}
			s.manager.Focus(matches[s.selectCursor].Index())
			s.refreshFocused()
			s.notifyMeta()
		}
		s.mode = modeNormal
		s.selectFilter = ""
		s.selectCursor = 0
	case tea.KeyEscape:
		s.mode = modeNormal
		s.selectFilter = ""
		s.selectCursor = 0
	case tea.KeyUp:
		if len(matches) > 0 {
			s.selectCursor = (s.selectCursor - 1 + len(matches)) % len(matches)
		}
	case tea.KeyDown:
		if len(matches) > 0 {
			s.selectCursor = (s.selectCursor + 1) % len(matches)
		}
	case tea.KeyBackspace:
		r := []rune(s.selectFilter)
		if len(r) > 0 {
			s.selectFilter = string(r[:len(r)-1])
			s.selectCursor = 0
		}
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

func (m Model) renderTabBar() string {
	s := m.s
	focused := s.manager.FocusedIndex()
	var tabs []string
	for _, sess := range s.manager.Sessions() {
		label := fmt.Sprintf("[%d] %s", sess.Index()+1, sess.Title())
		if sess.Exited() {
			tabs = append(tabs, tabExitedStyle.Render(label+" ✗"))
		} else if sess.Index() == focused {
			tabs = append(tabs, tabActiveStyle.Render(label))
		} else {
			tabs = append(tabs, tabInactiveStyle.Render(label))
		}
	}
	tabs = append(tabs, tabInactiveStyle.Render("[+] Ctrl+Alt+T"))
	bar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	if pad := s.width - lipgloss.Width(bar); pad > 0 {
		bar += tabBarStyle.Render(strings.Repeat(" ", pad))
	}
	return bar
}

func (m Model) renderPane() string {
	s := m.s
	if s.mode == modeSelecting {
		return m.renderSessionSelector()
	}
	idx := s.manager.FocusedIndex()
	paneCols, paneRows := paneSize(s.width, s.height)
	var pane string
	if vp, ok := s.viewports[idx]; ok {
		pane = s.renderPaneContent(vp, paneCols, paneRows)
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
		"New:     " + truncate(s.renameText, width-9) + "█",
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
	choices := respawn + "   " + remove
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
			rows[i] = row + strings.Repeat(" ", pad)
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

func overlayLine(base, overlay string, left, width int) string {
	plain := stripANSI(base)
	if lipgloss.Width(plain) < width {
		plain += strings.Repeat(" ", width-lipgloss.Width(plain))
	}
	prefix := takeRunes(plain, left)
	suffixStart := left + lipgloss.Width(stripANSI(overlay))
	suffix := ""
	if suffixStart < lipgloss.Width(plain) {
		suffix = dropRunes(plain, suffixStart)
	}
	return prefix + overlay + suffix
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inEsc {
			if c >= '@' && c <= '~' {
				inEsc = false
			}
			continue
		}
		if c == 0x1b {
			inEsc = true
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func takeRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if n > len(runes) {
		n = len(runes)
	}
	return string(runes[:n])
}

func dropRunes(s string, n int) string {
	runes := []rune(s)
	if n >= len(runes) {
		return ""
	}
	return string(runes[n:])
}

func (m Model) renderSessionSelector() string {
	s := m.s
	cols, rows := paneSize(s.width, s.height)
	matches := s.filteredSessions()
	if s.selectCursor >= len(matches) {
		s.selectCursor = max(0, len(matches)-1)
	}
	lines := []string{"Sessions"}
	for i, sess := range matches {
		prefix := "  "
		if i == s.selectCursor {
			prefix = "> "
		}
		state := "running"
		if sess.Exited() {
			state = "exited"
		}
		lines = append(lines, fmt.Sprintf("%s[%d] %s  %s", prefix, sess.Index()+1, sess.Title(), state))
	}
	if len(matches) == 0 {
		lines = append(lines, "  no sessions match")
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	out := strings.Join(lines[:min(len(lines), rows)], "\n")
	if lipgloss.Width(out) > cols {
		return out
	}
	return out
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
		help := helpStyle.Render(" Rename: " + s.renameText + "█  Enter save  Esc cancel")
		left := statusKeyStyle.Render(status)
		if pad := s.width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeSelecting {
		help := helpStyle.Render(" Filter: " + s.selectFilter + "█  ↑/↓ move  Enter select  Esc cancel")
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
	help := helpStyle.Render(" Alt+` help  C-A-T new  C-A-W kill  C-A-R rename  C-A-S sessions  C-A-←/→ switch  C-A-Q quit")
	left := statusKeyStyle.Render(status)
	if pad := s.width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
		left += statusBarStyle.Render(strings.Repeat(" ", pad))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
}
