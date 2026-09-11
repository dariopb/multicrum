package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"multicrum/pkg/agentdetect"
	"multicrum/pkg/config"
	"multicrum/pkg/control"
	"multicrum/pkg/diagnostics"
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
	modeConnections
	modeQuitConfirm
	modeDeleteConfirm
	modeScrollSearch
	modeContextMenu
	modeFilePicker
	modeSettings
)

type connectionState struct {
	id              string
	name            string
	labels          map[string]string
	createdBy       string
	manager         *session.SessionManager
	viewports       map[int]*viewport.Model
	altScreens      map[int]bool
	scrollbackMode  map[int]bool
	scrollbackCache map[int]scrollWrapCache
	liveLines       map[int][]session.BufferLine
	initialCfg      []startupSession
	// outputPending coalesces PTY output notifications: the read loop sets it
	// and only enqueues a connectionOutputMsg when it was previously clear, so
	// a burst of small child writes collapses into a single Bubble Tea message
	// (and therefore a single View() rebuild) instead of thousands. The bytes
	// themselves are already applied to the VTScreen synchronously in the read
	// loop, so no output is lost by dropping the intermediate notifications.
	outputPending  atomic.Bool
	webActive      atomic.Bool
	callbacksBound bool
}

type detectedAgent struct {
	status     agentdetect.Status
	generation uint64
}

func (m Model) localAttachTerminalState() string {
	state := ansi.SetModeAltScreenSaveCursor +
		ansi.SetModeBracketedPaste +
		m.s.mouseEnableSequence()
	cursor := m.cursor()
	if cursor == nil {
		return state + ansi.HideCursor
	}
	style := (int(cursor.Shape) * 2) + 1
	if !cursor.Blink {
		style++
	}
	return state +
		ansi.SetCursorStyle(style) +
		ansi.ShowCursor
}

type state struct {
	manager                *session.SessionManager
	viewports              map[int]*viewport.Model
	altScreens             map[int]bool // last-seen alt-screen state per session index, for transition detection
	scrollbackCache        map[int]scrollWrapCache
	liveLines              map[int][]session.BufferLine
	connections            []*connectionState
	activeConn             int
	serverName             string
	localClients           int
	inputMux               *InputMux
	program                *tea.Program
	trace                  *diagnostics.Recorder
	width                  int
	height                 int
	connectionLayout       connectionLayout
	connectionRailWidth    int
	layoutFallback         bool
	errMsg                 string
	mode                   mode
	renameText             string
	renameCursor           int
	selectFilter           string
	selectFilterCursor     int
	selectCursor           int
	selectScroll           int  // first visible row index when sessions overflow modal height
	selectMoving           bool // when true, Up/Down reorders the selected session instead of moving cursor
	selectMoveStart        int  // original session index of the moving entry, so Esc can revert
	selectRenaming         bool
	selectFiltering        bool
	exitPromptID           int // session index waiting for user decision (in exit prompt)
	exitChoice             int // 0 = respawn, 1 = remove
	exitError              string
	deleteKind             string
	deleteIndex            int
	deleteName             string
	deleteReturn           mode
	deleteChoice           int
	newSession             newSessionState // state for the new-session modal
	newSessionReturn       mode
	filePicker             filePickerState
	connCursor             int
	connRename             string
	connRenameCursor       int
	connRenaming           bool
	connFilter             string
	connFilterCursor       int
	connFiltering          bool
	connMoving             bool
	contextMenu            contextMenu
	settingsCursor         int
	settingsDirty          bool
	sessionHitboxes        []mouseHitbox
	connectionHitboxes     []mouseHitbox
	newSessionHitbox       mouseHitbox
	newConnectionHitbox    mouseHitbox
	appMenuHitbox          mouseHitbox
	helpHitbox             mouseHitbox
	connectionsHitbox      mouseHitbox
	hasNewSessionHitbox    bool
	hasNewConnectionHitbox bool
	hasAppMenuHitbox       bool
	hasHelpHitbox          bool
	hasConnectionsHitbox   bool
	mouseDrag              mouseDrag
	mouseCapture           bool      // when true, mouse events are forwarded to the child PTY (app-mode)
	sel                    selection // in-progress / completed mouse selection over the focused buffer
	copySelectionOnRelease bool
	search                 scrollSearch       // vi-style scrollback search ('/') and line jump (':')
	sshClient              *ssh_client.Client // non-nil starts SSH-backed sessions
	onMetaChange           func()             // called when sessions are added/removed/focused
	wsTransport            *transport.WSTransport
	configPath             string           // path used by save/load layout shortcut
	initialCfg             []startupSession // sessions to spawn on Init (instead of agentCmd)
	statusMsg              string           // transient status line (e.g. config save result)
	clipboardWrite         func(string)
	detachClient           func()
	quitting               bool         // suppresses redraw after the terminal is explicitly cleared
	renderPending          bool         // a render tick is in flight (coalescing PTY bursts)
	lastRender             time.Time    // leading-edge render immediately after idle, then cap at frame rate
	scrollbackMode         map[int]bool // sessions currently scrolled up (need full scrollback in viewport)
	paneCache              paneCache    // memoizes renderPaneContent across redundant View() calls
	agentMu                sync.RWMutex
	agentStatuses          map[string]detectedAgent
	agentMonitor           *agentdetect.Monitor
	agentNativeServer      *agentdetect.NativeServer
	agentNativeEndpoint    string
	agentSpinnerEnabled    bool
	agentSpinnerStyle      string
	agentSpinnerRunning    bool
	agentSpinnerFrame      int
	controlService         *control.Service
	controlSessions        map[string]controlSessionMeta
	suppressedExits        sync.Map
	ready                  chan struct{}
	readyOnce              sync.Once
	repaintEpoch           bool
}

// paneCache memoizes the output of renderPaneContent, which is a pure function
// of the viewport content, scroll offset, pane size and wrap flag. Bubble Tea
// calls View() after every message, so between render frames the same pane is
// rebuilt many times over with identical inputs; caching turns those repeats
// into a cheap key comparison instead of a full split/pad pass.
type paneCache struct {
	valid   bool
	content string
	yoff    int
	cols    int
	rows    int
	wrap    bool
	out     string
}

type scrollWrapCache struct {
	content string
	cols    int
	rows    []string
	plain   []session.BufferLine
}

// startupSession is a session entry queued for startup, with its title and
// command tokens. Empty Title means "use command name". CmdLine, when set,
// is the original shell-syntax line and is remembered on the session so
// layout save can round-trip it as-is.
type startupSession struct {
	Title   string
	Cmd     []string
	CmdLine string
	Cwd     string
	SSH     *config.SSHEntry
}

type startupConnection struct {
	Name     string
	Sessions []startupSession
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
	conn := &connectionState{
		id:              control.NewID("con"),
		name:            "default",
		viewports:       make(map[int]*viewport.Model),
		altScreens:      make(map[int]bool),
		scrollbackMode:  make(map[int]bool),
		scrollbackCache: make(map[int]scrollWrapCache),
		liveLines:       make(map[int][]session.BufferLine),
	}
	st := &state{
		viewports:              conn.viewports,
		altScreens:             conn.altScreens,
		scrollbackMode:         conn.scrollbackMode,
		scrollbackCache:        conn.scrollbackCache,
		liveLines:              conn.liveLines,
		connections:            []*connectionState{conn},
		serverName:             "default",
		width:                  cols,
		height:                 rows,
		connectionLayout:       connectionLayoutBottom,
		connectionRailWidth:    connectionRailWidth,
		sshClient:              sshClient,
		clipboardWrite:         copyToClipboard,
		copySelectionOnRelease: true,
		agentStatuses:          make(map[string]detectedAgent),
		agentSpinnerEnabled:    true,
		agentSpinnerStyle:      config.AgentSpinnerStyleRectangle,
		controlSessions:        make(map[string]controlSessionMeta),
		ready:                  make(chan struct{}),
	}
	return &Model{agentCmd: agentCmd, s: st}
}

// SetAgentCmdLine records the original shell line for the default
// agentCmd. Used so the layout-save shortcut round-trips the original
// string instead of the "bash -c <line>" expansion.
func (m *Model) SetAgentCmdLine(line string) { m.agentCmdLine = line }

func (m *Model) SetServerName(name string) {
	if name == "" {
		name = "default"
	}
	m.s.serverName = name
}

func (m *Model) SetInputMux(input *InputMux) { m.s.inputMux = input }

// Ready is closed after initial connections and sessions have been started.
func (m *Model) Ready() <-chan struct{} { return m.s.ready }

type modelSyncMsg chan struct{}

type exitEventKey struct {
	sessionID  string
	generation uint64
}

// Sync waits until all messages already sent to the Bubble Tea program have
// been applied.
func (m *Model) Sync(ctx context.Context) error {
	if m.s.program == nil {
		return fmt.Errorf("model program is not running")
	}
	done := make(modelSyncMsg)
	m.s.program.Send(done)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Model) SetLocalClientCount(n int) { m.s.localClients = n }

// SetClipboardOutput routes OSC 52 through the same writer used by the TUI.
// Detached owners have no /dev/tty, so this is required for clipboard escapes
// to reach attached clients through the local-server fan-out.
func (m *Model) SetClipboardOutput(w io.Writer) {
	if w == nil {
		m.s.clipboardWrite = copyToClipboard
		return
	}
	m.s.clipboardWrite = func(text string) {
		copyToClipboardOutput(w, text)
	}
}

func (m *Model) SetClipboardHandler(handler func(string)) {
	if handler == nil {
		return
	}
	m.s.clipboardWrite = handler
}

func (m *Model) SetDetachHandler(handler func()) {
	m.s.detachClient = handler
}

// SetConfigPath records the path the layout-save shortcut writes to.
// Empty means "no config path", and the save shortcut becomes a no-op
// with an error status.
func (m *Model) SetConfigPath(path string) {
	m.s.configPath = path
}

// SetConnectionLayout selects the local TUI connection switcher placement.
// Invalid values intentionally retain the compatible bottom layout.
func (m *Model) SetConnectionLayout(layout string) {
	m.s.connectionLayout = normalizedConnectionLayout(layout)
	m.s.layoutFallback = m.s.connectionLayout == connectionLayoutLeft && !m.s.usingLeftRail()
	if m.s.manager != nil {
		m.s.applyGeometry()
	}
}

func (m *Model) SetConnectionRailWidth(width int) {
	if width <= 0 {
		width = connectionRailWidth
	}
	m.s.connectionRailWidth = width
	if m.s.manager != nil {
		m.s.applyGeometry()
	}
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
	nativeServer, err := agentdetect.ListenNativeUpdates(func(update agentdetect.Update) {
		p.Send(agentStatusMsg(update))
	})
	if err == nil {
		m.s.agentNativeServer = nativeServer
		m.s.agentNativeEndpoint = nativeServer.Endpoint()
	}
	geom := m.s.geometry()
	m.s.initManagers(geom.Pane.Width, geom.Pane.Height)
	m.s.agentMonitor = agentdetect.NewMonitor(agentdetect.NewProcessInventory(), 2*time.Second, func(update agentdetect.Update) {
		p.Send(agentStatusMsg(update))
	})
	m.s.agentMonitor.Start()
}

// SetControlService connects protocol publication to the owner model.
func (m *Model) SetControlService(service *control.Service) {
	m.s.controlService = service
}

func (m *Model) CloseAgentDetection() {
	if m.s.agentMonitor != nil {
		m.s.agentMonitor.Close()
	}
	if m.s.agentNativeServer != nil {
		m.s.agentNativeServer.Close()
	}
}

// CloseSessions stops every PTY owned by the model.
func (m *Model) CloseSessions() error {
	var errs []error
	for _, conn := range m.s.connections {
		if conn != nil && conn.manager != nil {
			if err := conn.manager.CloseAll(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
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
				Agent:  m.s.sessionAgentInfo(s),
			})
		}
		return out
	}

	wst.FocusedID = func() int { return m.s.manager.FocusedIndex() }
	wst.Server = func() string { return m.s.serverName }
	wst.ActiveConnection = func() string { return m.s.activeConnection().name }
	wst.Connections = func() []transport.ConnectionInfo {
		out := make([]transport.ConnectionInfo, 0, len(m.s.connections))
		for _, conn := range m.s.connections {
			info := transport.ConnectionInfo{ID: conn.name, Name: conn.name, Agent: m.s.connectionAgentInfo(conn)}
			if conn.manager != nil {
				info.FocusedID = conn.manager.FocusedIndex()
				info.SessionCount = conn.manager.Len()
				for _, sess := range conn.manager.Sessions() {
					info.Sessions = append(info.Sessions, transport.SessionInfo{
						ID: sess.Index(), Title: sess.Title(), Exited: sess.Exited(),
						Agent: m.s.sessionAgentInfo(sess),
					})
				}
			}
			out = append(out, info)
		}
		return out
	}
	wst.Settings = func() transport.SettingsInfo {
		return transport.SettingsInfo{
			SpinnerAnimation: m.s.agentSpinnerEnabled,
			SpinnerStyle:     m.s.agentSpinnerStyle,
			CopyOnRelease:    m.s.copySelectionOnRelease,
		}
	}

	wst.OnResize = func(rm transport.ResizeMsg) {
		if m.s.program != nil {
			m.s.program.Send(wsResizeMsg(rm))
		}
	}

	wst.OnControl = func(cm transport.ControlMsg) {
		if m.s.program != nil {
			m.s.program.Send(wsControlMsg(cm))
		}
	}

	m.s.wsTransport = wst
	m.s.rebindConnectionCallbacks()
	m.s.onMetaChange = wst.BroadcastMeta

	return wst, nil
}

// wsInputMsg carries raw keystroke bytes from a WebSocket client.
type wsInputMsg []byte

// wsControlMsg carries a session-management command from a browser client.
type wsControlMsg transport.ControlMsg

// wsResizeMsg carries a terminal resize report from a browser client.
type wsResizeMsg transport.ResizeMsg

type agentStatusMsg agentdetect.Update

type agentSpinnerTickMsg struct{}

type connectionOutputMsg struct {
	Conn *connectionState
	Msg  session.OutputMsg
}

type connectionExitMsg struct {
	Conn *connectionState
	Msg  session.ExitMsg
}

type LocalClientCountMsg int

type localClientCountMsg = LocalClientCountMsg

type localRepaintMsg struct{}

// renderTickMsg refreshes the visible viewport. The first output after an idle
// period is rendered immediately for responsive command echo; sustained output
// is then capped to one refresh per renderInterval.
type renderTickMsg struct {
	at time.Time
}

const renderInterval = 16 * time.Millisecond

func renderDelay(lastRender, now time.Time) time.Duration {
	if lastRender.IsZero() {
		return 0
	}
	delay := lastRender.Add(renderInterval).Sub(now)
	if delay < 0 {
		return 0
	}
	return delay
}

func (s *state) scheduleRender(now time.Time) tea.Cmd {
	if s.renderPending {
		return nil
	}
	s.renderPending = true
	delay := renderDelay(s.lastRender, now)
	if delay == 0 {
		return func() tea.Msg { return renderTickMsg{at: now} }
	}
	return tea.Tick(delay, func(at time.Time) tea.Msg { return renderTickMsg{at: at} })
}

// Init starts the first session.
func (m Model) Init() tea.Cmd {
	return tea.Sequence(resetTerminalInputModes, func() tea.Msg { return startFirstSessionMsg{} })
}

func resetTerminalInputModes() tea.Msg {
	// Reset only legacy/stray input modes a previous child or session may
	// have left enabled. We deliberately do NOT reset the button-event
	// (1002), any-event (1003) or SGR-extended (1006) mouse modes here:
	// bubbletea's renderer owns those via View().MouseMode and enables them
	// on the first flush. Resetting them here runs after that first flush and
	// silently disables mouse reporting at startup (wheel/selection dead until
	// the user toggles mouse mode twice, which forces the renderer to re-emit
	// the enable). See the mouse-mode notes in AGENTS.md/spec.md.
	return tea.RawMsg{Msg: ansi.ResetModeMouseX10 +
		ansi.ResetModeMouseNormal +
		ansi.ResetModifyOtherKeys +
		ansi.KittyKeyboard(0, 1) +
		ansi.DisableKittyKeyboard}
}

type startFirstSessionMsg struct{}

// mouseEnableSequence returns the CSI sequence that enables the mouse-reporting
// mode matching the current capture state, so a freshly attached client's
// terminal starts receiving wheel/motion events without waiting for a
// MouseMode change. It mirrors what bubbletea's renderer emits for
// View().MouseMode (CellMotion in select mode, AllMotion in app mode).
func (s *state) mouseEnableSequence() string {
	if s.mouseCapture {
		return ansi.SetModeMouseAnyEvent + ansi.SetModeMouseExtSgr
	}
	return ansi.SetModeMouseButtonEvent + ansi.SetModeMouseExtSgr
}

// Update handles all incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	s := m.s
	switch msg := msg.(type) {

	case startFirstSessionMsg:
		var failed []string
		for _, conn := range s.connections {
			if conn.manager == nil {
				continue
			}
			entries := conn.initialCfg
			if len(entries) == 0 {
				entries = []startupSession{{Cmd: m.agentCmd, CmdLine: m.agentCmdLine}}
			}
			for _, entry := range entries {
				cmd := entry.Cmd
				if entry.CmdLine != "" {
					cmd = ParseCmdLine(entry.CmdLine)
				}
				if len(cmd) == 0 && entry.SSH == nil {
					cmd = m.agentCmd
				}
				var sess *session.Session
				var err error
				if entry.SSH != nil {
					client, err := sshClientFromConfig(entry.SSH, cmd)
					if err != nil {
						failed = append(failed, fmt.Sprintf("%s: %v", conn.name, err))
						continue
					}
					sess, err = conn.manager.NewWithSSH(cmd, client)
				} else {
					sess, err = conn.manager.NewInDir(cmd, entry.Cwd)
				}
				if err != nil {
					failed = append(failed, fmt.Sprintf("%s: %v", conn.name, err))
					continue
				}
				if entry.Title != "" {
					sess.SetTitle(entry.Title)
				}
				if entry.CmdLine != "" {
					sess.SetCmdLine(entry.CmdLine)
				}
			}
			if conn.manager.Len() == 0 {
				sess, err := conn.manager.New(m.agentCmd)
				if err != nil {
					failed = append(failed, fmt.Sprintf("%s: %v", conn.name, err))
					continue
				}
				if m.agentCmdLine != "" {
					sess.SetCmdLine(m.agentCmdLine)
				}
			}
		}
		s.syncActiveConnectionFields()
		if len(failed) > 0 {
			s.errMsg = "ERROR starting some sessions: " + strings.Join(failed, "; ")
		} else {
			s.errMsg = ""
		}
		s.ensureViewport(s.manager.FocusedIndex(), s.width, s.height)
		s.notifyMeta()
		s.readyOnce.Do(func() { close(s.ready) })
		return m, nil

	case modelSyncMsg:
		close(msg)
		return m, nil

	case agentStatusMsg:
		if s.applyAgentUpdate(agentdetect.Update(msg)) {
			if service := s.controlService; service != nil {
				update := agentdetect.Update(msg)
				data := map[string]any{}
				if update.Status != nil {
					data["provider"] = update.Status.Provider
					data["state"] = update.Status.State
					data["source"] = update.Status.Source
					data["confidence"] = update.Status.Confidence
				}
				service.PublishEvent("agent.state", control.Event{
					Event: "agent.state", SessionID: update.ID,
					Generation: update.Generation, Data: data,
				})
			}
			s.notifyMeta()
		}
		return m, s.startAgentSpinner()

	case controlRequestMsg:
		result, controlErr := s.handleControlRequest(m, msg.method, msg.params, msg.controllerID)
		msg.response <- controlRequestResult{result: result, err: controlErr}
		return m, nil

	case agentSpinnerTickMsg:
		if !s.agentSpinnerEnabled || !s.hasWorkingAgent() {
			s.agentSpinnerRunning = false
			s.agentSpinnerFrame = 0
			return m, nil
		}
		s.agentSpinnerFrame = (s.agentSpinnerFrame + 1) % len(s.agentSpinnerFrames())
		return m, agentSpinnerTick()

	case localClientCountMsg:
		s.trace.Record("ui.clients old=%d new=%d", s.localClients, int(msg))
		attached := int(msg) > s.localClients
		s.localClients = int(msg)
		if attached {
			// A new terminal has not received the renderer's existing frame.
			// Initialize terminal modes, then make Bubble Tea repaint from its
			// own cell buffer; manually painting a frame here desynchronizes
			// the renderer's differential state from the physical terminal.
			state := m.localAttachTerminalState()
			return m, tea.Sequence(
				func() tea.Msg { return tea.RawMsg{Msg: state} },
				tea.ClearScreen,
				func() tea.Msg { return localRepaintMsg{} },
			)
		}
		return m, nil

	case localRepaintMsg:
		// clearScreen marks the terminal renderer for repaint, but Bubble Tea
		// skips its flush when the View string is unchanged. Alternate between
		// equivalent SGR resets so the repaint reaches a newly attached client.
		s.repaintEpoch = !s.repaintEpoch
		return m, nil

	case tea.WindowSizeMsg:
		s.trace.Record("resize.request source=tui old=%dx%d new=%dx%d",
			s.width, s.height, msg.Width, msg.Height)
		s.width = msg.Width
		s.height = msg.Height
		s.applyGeometry()
		return m, nil

	case wsResizeMsg:
		s.trace.Record("resize.request source=browser connection=%s index=%d new=%dx%d",
			s.activeConnection().id, msg.ID, msg.Cols, msg.Rows)
		// Last resizer wins: the surface actively viewing the session is
		// authoritative. The other renderer simply shows whatever fits.
		s.manager.ResizeOne(msg.ID, msg.Cols, msg.Rows)
		return m, nil

	case wsInputMsg:
		if sess := s.manager.Focused(); sess != nil {
			_, _ = sess.Write([]byte(msg))
		}
		return m, nil

	case wsControlMsg:
		var cmd tea.Cmd
		switch msg.Action {
		case "focus":
			// Only the client that actually changes the focus is the active
			// one; its own follow-up resize sets the authoritative size. When
			// another client is merely catching up to a focus someone else
			// initiated (msg.ID already focused), do NOT refreshFocused —
			// that would re-push the local TUI size and clobber the
			// initiator's dimensions ("last one wins"). The transport layer
			// still sends this client its snapshot regardless.
			if msg.ID != s.manager.FocusedIndex() {
				s.manager.Focus(msg.ID)
				s.refreshFocused()
				s.notifyMeta()
			}
		case "new":
			s.handleWSNew(m, transport.ControlMsg(msg))
		case "kill":
			if s.manager.Len() > 1 {
				delete(s.viewports, msg.ID)
				delete(s.scrollbackCache, msg.ID)
				delete(s.liveLines, msg.ID)
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
		case "prevConnection":
			s.focusConnection(s.activeConn - 1)
		case "nextConnection":
			s.focusConnection(s.activeConn + 1)
		case "focusConnection":
			name := msg.Connection
			if name == "" {
				name = msg.Title
			}
			if name != "" {
				s.focusConnectionByName(name)
			}
		case "moveConnection":
			target := strings.TrimSpace(msg.Connection)
			for i, c := range s.connections {
				if c.name == target {
					s.moveConnection(i, msg.To)
					break
				}
			}
		case "newConnection":
			name := strings.TrimSpace(msg.Connection)
			if name == "" {
				name = strings.TrimSpace(msg.Title)
			}
			if name == "" {
				name = fmt.Sprintf("conn-%d", len(s.connections)+1)
			}
			conn := s.createConnectionWithDefaultSession(name, m)
			for i, c := range s.connections {
				if c == conn {
					s.focusConnection(i)
					break
				}
			}
		case "renameConnection":
			target := strings.TrimSpace(msg.Connection)
			newName := strings.TrimSpace(msg.Title)
			if newName != "" {
				if target == "" {
					s.renameConnection(s.activeConn, newName)
				} else {
					for i, c := range s.connections {
						if c.name == target {
							s.renameConnection(i, newName)
							break
						}
					}
				}
			}
		case "removeConnection":
			target := strings.TrimSpace(msg.Connection)
			if target == "" {
				s.removeConnection(s.activeConn)
			} else {
				for i, c := range s.connections {
					if c.name == target {
						s.removeConnection(i)
						break
					}
				}
			}
		case "save":
			s.saveLayout()
			s.notifyMeta()
		case "setting":
			cmd = s.applyRemoteSetting(msg.Setting, msg.Value)
		case "exit":
			s.handleWSExit(transport.ControlMsg(msg))
		}
		return m, cmd

	case connectionOutputMsg:
		s.evaluateAgentScreen(msg.Conn, msg.Msg.Index)
		spinnerCmd := s.startAgentSpinner()
		connIndex := connectionIndex(s.connections, msg.Conn)
		if connIndex < 0 {
			msg.Conn.outputPending.Store(false)
			return m, spinnerCmd
		}
		if connIndex != s.activeConn {
			// Not visible: re-arm this connection's coalescing flag so its
			// next output still notifies (we just don't render it).
			msg.Conn.outputPending.Store(false)
			return m, spinnerCmd
		}
		if msg.Msg.Index != s.manager.FocusedIndex() {
			// Active connection but a background session produced the output;
			// re-arm so that session keeps notifying, but don't render it.
			msg.Conn.outputPending.Store(false)
			return m, spinnerCmd
		}
		s.ensureViewport(msg.Msg.Index, s.width, s.height)
		if cmd := s.scheduleRender(time.Now()); cmd == nil {
			// A frame is already scheduled; leave outputPending set so every
			// further child write in this frame window coalesces into it. The
			// flag is re-armed by renderTickMsg once the frame is drawn.
			return m, spinnerCmd
		} else {
			return m, tea.Batch(cmd, spinnerCmd)
		}

	case OutputMsg:
		s.evaluateAgentScreen(s.activeConnection(), msg.Index)
		spinnerCmd := s.startAgentSpinner()
		if msg.Index != s.manager.FocusedIndex() {
			return m, spinnerCmd
		}
		s.ensureViewport(msg.Index, s.width, s.height)
		return m, tea.Batch(s.scheduleRender(time.Now()), spinnerCmd)

	case renderTickMsg:
		s.renderPending = false
		if msg.at.IsZero() {
			s.lastRender = time.Now()
		} else {
			s.lastRender = msg.at
		}
		// A connection or session switch can happen after one connection
		// schedules this shared render tick. Output from the newly active
		// connection then joins the same frame window, so re-arming only the
		// connection active at tick time can leave another connection stuck
		// pending forever. Re-arm every connection covered by the shared tick.
		for _, conn := range s.connections {
			conn.outputPending.Store(false)
		}
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
					delete(s.scrollbackCache, idx)
					vp.SoftWrap = true
					s.setLiveContent(idx, vp, sess)
					vp.GotoBottom()
					s.clearSelection()
					s.clearSearch()
				} else if s.scrollbackMode[idx] && !alt {
					// User is browsing scrollback: keep the full content so
					// YOffset stays meaningful relative to it.
					wasAtBottom := vp.AtBottom()
					s.setScrollbackContent(idx, vp, sess.Screen().RenderWithScrollback())
					if wasAtBottom {
						// Caught back up to live tail — drop the heavy
						// scrollback content and resume cheap rendering.
						s.scrollbackMode[idx] = false
						delete(s.scrollbackCache, idx)
						vp.SoftWrap = true
						s.setLiveContent(idx, vp, sess)
						anchorViewportToCursor(vp, sess)
						s.clearSearch()
					}
				} else {
					// Hot path: only the visible screen, bounded cols x rows
					// of work per frame regardless of scrollback depth.
					s.setLiveContent(idx, vp, sess)
					anchorViewportToCursor(vp, sess)
				}
				s.viewports[idx] = vp
				s.evaluateAgentSession(sess)
				break
			}
		}
		return m, s.startAgentSpinner()

	case connectionExitMsg:
		s.trace.Record("ui.session-exit connection=%s session=%s generation=%d index=%d",
			msg.Conn.id, msg.Msg.SessionID, msg.Msg.Generation, msg.Msg.Index)
		if s.consumeSuppressedExit(msg.Msg) {
			s.notifyMeta()
			return m, nil
		}
		connIndex := connectionIndex(s.connections, msg.Conn)
		if connIndex < 0 || connIndex != s.activeConn {
			s.notifyMeta()
			return m, nil
		}
		exitIndex, exists := currentExitIndex(msg.Conn, msg.Msg)
		if !exists {
			s.notifyMeta()
			return m, nil
		}
		if s.mode == modeNormal && exitIndex == s.manager.FocusedIndex() {
			s.mode = modeExitPrompt
			s.exitPromptID = exitIndex
			s.exitChoice = 0
			s.exitError = ""
			if s.usingLeftRail() {
				s.notifyMeta()
				return m, tea.ClearScreen
			}
		}
		s.notifyMeta()
		return m, nil

	case ExitMsg:
		if s.consumeSuppressedExit(msg) {
			s.notifyMeta()
			return m, nil
		}
		// Only prompt for the focused session; other exits still mark the tab
		// as exited but don't yank the user away from what they're doing.
		if s.mode == modeNormal && msg.Index == s.manager.FocusedIndex() {
			s.mode = modeExitPrompt
			s.exitPromptID = msg.Index
			s.exitChoice = 0
			s.exitError = ""
			if s.usingLeftRail() {
				s.notifyMeta()
				return m, tea.ClearScreen
			}

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
		ev := mouseEventFromMsg(msg)
		debugMouse(ev)
		if s.mode == modeContextMenu {
			return m, s.handleContextMenuMouse(m, ev)
		}
		if s.mode == modeExitPrompt {
			if handled, cmd := s.handleMouseScopeClick(m, ev); handled {
				return m, cmd
			}
		}
		if s.centeredModalOpen() {
			return m, s.handleModalMouse(m, ev)
		}
		if handled, cmd := s.handleMouseScopeClick(m, ev); handled {
			return m, cmd
		}
		if s.mode != modeNormal {
			return m, nil
		}
		geom := s.geometry()
		paneX, paneY, inPane := geom.ScreenToPane(ev.X, ev.Y)
		sess := s.manager.Focused()
		if sess == nil {
			return m, nil
		}
		// Mouse-capture mode: forward to the child PTY for apps like btop,
		// htop, vim, lazygit. When disabled (select mode), the block below
		// owns the wheel (scrollback) and left-drag (selection/copy) instead.
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
			ev.X, ev.Y = paneX, paneY
			if seq := encodeMouseSGR(ev); seq != nil {
				_, _ = sess.Write(seq)
			}
			return m, nil
		}
		// Select mode: the local UI owns the mouse. The wheel drives the
		// scrollback buffer (same movement as Ctrl+Alt+PgUp/PgDown), and
		// left-drag builds a selection that is copied to the clipboard on
		// release. Clamp coordinates so a drag/release that drifts into the
		// tab/status bar still updates and finishes the selection.
		// Clamp in pane-relative screen space even when a drag/release is
		// outside the pane (for example over the left rail).
		clampX := ev.X - geom.Pane.X
		if clampX < 0 {
			clampX = 0
		}
		if clampX >= geom.Pane.Width {
			clampX = geom.Pane.Width - 1
		}
		clampY := ev.Y - geom.Pane.Y
		if clampY < 0 {
			clampY = 0
		}
		if clampY >= geom.Pane.Height {
			clampY = geom.Pane.Height - 1
		}
		switch ev.Button {
		case tea.MouseWheelUp:
			if inPane {
				s.scrollFocused(-3)
			}
			return m, nil
		case tea.MouseWheelDown:
			if inPane {
				s.scrollFocused(3)
			}
			return m, nil
		}
		// Shift+drag bypasses our app-level selection so the terminal's own
		// native selection/copy takes over. Most terminals already route
		// Shift-modified mouse events to native selection while reporting is
		// on; skipping here guarantees we never fight it (and never leave a
		// stray app selection behind).
		if ev.Mod.Contains(tea.ModShift) {
			if s.sel.active || s.sel.hasRange {
				s.clearSelection()
			}
			return m, nil
		}
		if inPane && ev.Button == tea.MouseRight &&
			(ev.Action == mousePress || ev.Action == mouseRelease) {
			cmd := s.copySelection()
			if cmd != nil {
				s.clearSelection()
				s.bottomFocused()
			}
			return m, cmd
		}
		switch ev.Action {
		case mousePress:
			if ev.Button == tea.MouseLeft && inPane {
				block := ev.Mod.Contains(tea.ModCtrl | tea.ModAlt)
				s.startSelection(clampX, clampY, block)
			}
		case mouseMotion:
			if s.sel.active {
				s.updateSelection(clampX, clampY)
			}
		case mouseRelease:
			if s.sel.active {
				s.updateSelection(clampX, clampY)
				return m, s.finishSelection()
			}
		}
		return m, nil

	case tea.KeyPressMsg:
		if handled, cmd := s.handleGlobalShortcut(msg); handled {
			return m, cmd
		}
		if s.mode == modeHelp {
			s.handleHelpKey(msg)
			return m, nil
		}
		if s.mode == modeRenaming {
			s.handleRenameKey(msg)
			return m, nil
		}
		if s.mode == modeSelecting {
			cmd := s.handleSelectKey(m, msg)
			return m, cmd
		}
		if s.mode == modeExitPrompt {
			cmd := s.handleExitPromptKey(m, msg)
			return m, cmd
		}
		if s.mode == modeNewSession {
			cmd := s.handleNewSessionKey(m, msg)
			return m, cmd
		}
		if s.mode == modeFilePicker {
			s.handleFilePickerKey(msg)
			return m, nil
		}
		if s.mode == modeConnections {
			cmd := s.handleConnectionsKey(m, msg)
			return m, cmd
		}
		if s.mode == modeSettings {
			return m, s.handleSettingsKey(msg)
		}
		if s.mode == modeContextMenu {
			if msg.Key().Code == tea.KeyEscape {
				s.closeContextMenu()
			}
			return m, nil
		}
		if s.mode == modeQuitConfirm {
			cmd := s.handleQuitConfirmKey(msg)
			return m, cmd
		}
		if s.mode == modeDeleteConfirm {
			cmd := s.handleDeleteConfirmKey(msg)
			return m, cmd
		}
		if s.mode == modeScrollSearch {
			return m, s.handleScrollSearchKey(msg)
		}
		if handled, cmd := s.handleShortcut(m, msg); handled {
			return m, cmd
		}
		// vi-style scrollback navigation ('/', ':', 'n', 'N') is consumed
		// only while the focused session is scrolled into its buffer.
		if s.maybeScrollSearchTrigger(msg) {
			return m, nil
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

func (s *state) suppressExit(sessionID string, generation uint64) {
	if sessionID == "" {
		return
	}
	key := exitEventKey{sessionID: sessionID, generation: generation}
	s.suppressedExits.Store(key, struct{}{})
	time.AfterFunc(10*time.Second, func() {
		s.suppressedExits.Delete(key)
	})
}

func (s *state) consumeSuppressedExit(msg session.ExitMsg) bool {
	if msg.SessionID == "" {
		return false
	}
	_, suppressed := s.suppressedExits.LoadAndDelete(exitEventKey{
		sessionID: msg.SessionID, generation: msg.Generation,
	})
	return suppressed
}

func currentExitIndex(conn *connectionState, msg session.ExitMsg) (int, bool) {
	if msg.SessionID == "" {
		return msg.Index, true
	}
	if conn == nil || conn.manager == nil {
		return 0, false
	}
	for _, sess := range conn.manager.Sessions() {
		sessionID, generation, _, _ := sess.RuntimeSnapshot()
		if sessionID == msg.SessionID && generation == msg.Generation {
			return sess.Index(), true
		}
	}
	return 0, false
}

// View renders the full TUI.
func (m Model) View() tea.View {
	view := tea.NewView(m.viewString())
	view.AltScreen = true
	if m.s.mouseCapture || m.s.mode == modeContextMenu {
		view.MouseMode = tea.MouseModeAllMotion
	} else {
		// Select mode still needs mouse reporting so we receive wheel events
		// (scrollback) and drag events (selection/copy). CellMotion (1002)
		// reports press/release + motion-while-pressed + wheel, which is all
		// we need and far less chatty than AllMotion. Enabling reporting
		// disables the terminal's native click-drag selection, so we own
		// selection ourselves (see the MouseMsg handler + overlaySelection);
		// Shift+drag still falls through to native selection in most
		// terminals.
		view.MouseMode = tea.MouseModeCellMotion
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
	geom := s.geometry()
	rows := geom.Pane.Height
	var y int
	if s.scrollbackMode[idx] {
		// Viewport content is scrollback + screen; map vt row to viewport line.
		line := len(sess.Screen().BufferLines()) - rows + cur.Y
		y = geom.Pane.Y + line - vp.YOffset()
	} else {
		// Cheap path: viewport content is the full vt screen; map vt-Y
		// through the viewport's current scroll offset so the on-screen
		// cursor stays aligned when the vt grid is taller than the pane.
		y = geom.Pane.Y + cur.Y - vp.YOffset()
	}
	if y < geom.Pane.Y || y >= geom.Pane.Y+rows || cur.X < 0 || cur.X >= geom.Pane.Width {
		return nil
	}
	cursor := tea.NewCursor(geom.Pane.X+cur.X, y)
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
	if s.quitting {
		return ""
	}
	if s.errMsg != "" {
		return s.errMsg + "\n\nPress Ctrl+Alt+Q to quit."
	}
	if s.manager == nil || s.manager.Len() == 0 {
		return "Starting…\n"
	}
	geom := s.geometry()
	tabBar := m.renderTabBar()
	pane := m.renderPane()
	var frame string
	if geom.ConnectionRail.Width == 0 {
		status := m.renderStatusBar()
		frame = strings.Join([]string{tabBar, pane, status}, "\n")
	} else {
		rail := m.renderConnectionRail(geom)
		mainRows := append([]string{tabBar}, strings.Split(pane, "\n")...)
		divider := dividerStyle.Render("│")
		rows := make([]string, geom.Screen.Height)
		for y := range rows {
			main := strings.Repeat(" ", geom.TabBar.Width)
			if y < len(mainRows) {
				main = padLine(mainRows[y], geom.TabBar.Width)
			}
			rows[y] = ansi.ResetStyle +
				padLine(rail[y], geom.ConnectionRail.Width) +
				divider +
				main +
				ansi.ResetStyle
		}
		frame = strings.Join(rows, "\n")
	}
	if s.mode == modeContextMenu {
		frame = m.overlayContextMenu(frame)
	}
	if s.repaintEpoch {
		return "\x1b[0m" + frame
	}
	return ansi.ResetStyle + frame
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *state) notifyMeta() {
	s.syncAgentSessions()
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
	geom := s.geometry()
	vp := viewport.New(viewport.WithWidth(geom.Pane.Width), viewport.WithHeight(geom.Pane.Height))
	vp.SoftWrap = false
	vp.SetContent("")
	s.viewports[idx] = &vp
	delete(s.scrollbackCache, idx)
	delete(s.liveLines, idx)
}

func (s *state) setLiveContent(idx int, vp *viewport.Model, sess *session.Session) {
	content, lines := sess.Screen().RenderSnapshot()
	// Live content is already an exact terminal cell grid. The pane renders
	// those physical rows without wrapping, so viewport offset math must count
	// the same rows or it can skip a row at the bottom of a full screen.
	vp.SoftWrap = false
	vp.SetContent(content)
	s.liveLines[idx] = lines
}

func (s *state) setScrollbackContent(idx int, vp *viewport.Model, content string) {
	cols := s.geometry().Pane.Width
	if s.scrollbackCache == nil {
		s.scrollbackCache = make(map[int]scrollWrapCache)
		s.activeConnection().scrollbackCache = s.scrollbackCache
	}
	cache, ok := s.scrollbackCache[idx]
	if ok && cache.content == content && cache.cols == cols {
		return
	}
	rows, plain := softWrapRowsWithPlain(strings.Split(content, "\n"), cols)
	s.scrollbackCache[idx] = scrollWrapCache{
		content: content,
		cols:    cols,
		rows:    rows,
		plain:   plain,
	}
	vp.SoftWrap = false
	vp.SetContent(strings.Join(rows, "\n"))
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
	s.setScrollbackContent(idx, vp, sess.Screen().RenderWithScrollback())
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
		delete(s.scrollbackCache, idx)
		vp.SoftWrap = true
		for _, sess := range s.manager.Sessions() {
			if sess.Index() == idx {
				s.setLiveContent(idx, vp, sess)
				break
			}
		}
		s.scrollbackMode[idx] = false
		s.clearSearch()
	}
	s.viewports[idx] = vp
}

func (s *state) refreshFocused() {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	// Re-push the current TUI pane size to whichever session just became
	// focused. Without this, a session that another viewer (a browser tab,
	// or another attached TUI client) had previously resized to its own
	// dimensions would stay at that stale size when the local viewer
	// switches to it — producing the "two/three buffer representations"
	// drift across viewers. The "last resizer wins" model already lets a
	// browser fit() override us right after; this just guarantees the
	// active viewer at the moment of focus is consistent.
	geom := s.geometry()
	s.trace.Record("resize.request source=tui-focus connection=%s index=%d new=%dx%d",
		s.activeConnection().id, idx, geom.Pane.Width, geom.Pane.Height)
	s.manager.ResizeOne(idx, geom.Pane.Width, geom.Pane.Height)
	for _, sess := range s.manager.Sessions() {
		if sess.Index() == idx {
			vp := s.viewports[idx]
			delete(s.scrollbackCache, idx)
			vp.SoftWrap = true
			s.setLiveContent(idx, vp, sess)
			vp.GotoBottom()
			s.scrollbackMode[idx] = false
			s.clearSearch()
			s.viewports[idx] = vp
			s.acknowledgeFocusedAgent(sess)
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
	shortcutConnections  = "ctrl+alt+o"
	shortcutRenameConn   = "ctrl+alt+e"
	shortcutNewConn      = "ctrl+alt+c"
	shortcutPrev         = "ctrl+alt+left"
	shortcutNext         = "ctrl+alt+right"
	shortcutScrollUp     = "ctrl+alt+up"
	shortcutScrollDown   = "ctrl+alt+down"
	shortcutPageUp       = "ctrl+alt+pgup"
	shortcutPageDown     = "ctrl+alt+pgdown"
	shortcutScrollTop    = "ctrl+alt+home"
	shortcutScrollBottom = "ctrl+alt+end"
	shortcutMouse        = "alt+enter" // Ctrl+Alt+M; terminals encode Ctrl+M as Enter
	shortcutForceResize  = "ctrl+alt+z"
	shortcutQuit         = "ctrl+alt+q"
)

func (s *state) handleGlobalShortcut(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	k := msg.Key()
	if isCtrlAlt(msg, 'z') {
		s.forceResizeFocused()
		return true, nil
	}
	if s.usingLeftRail() && k.Mod.Contains(tea.ModCtrl|tea.ModAlt) {
		switch k.Code {
		case tea.KeyUp, tea.KeyKpUp:
			s.mode = modeNormal
			s.focusConnection(s.activeConn - 1)
			return true, nil
		case tea.KeyDown, tea.KeyKpDown:
			s.mode = modeNormal
			s.focusConnection(s.activeConn + 1)
			return true, nil
		}
	}
	if k.Mod.Contains(tea.ModCtrl|tea.ModAlt) && (k.Code == '<' || k.Code == ',' || k.Code == '[') {
		s.mode = modeNormal
		s.focusConnection(s.activeConn - 1)
		return true, nil
	}
	if k.Mod.Contains(tea.ModCtrl|tea.ModAlt) && (k.Code == '>' || k.Code == '.' || k.Code == ']') {
		s.mode = modeNormal
		s.focusConnection(s.activeConn + 1)
		return true, nil
	}
	key := msg.Keystroke()
	if key == "alt+esc" || key == "ctrl+alt+[" || key == "alt+ctrl+[" {
		s.mode = modeNormal
		s.focusConnection(s.activeConn - 1)
		return true, nil
	}
	if key == "ctrl+alt+]" || key == "alt+ctrl+]" || key == "alt+ctrl+}" || key == "ctrl+alt+}" {
		s.mode = modeNormal
		s.focusConnection(s.activeConn + 1)
		return true, nil
	}
	if handled, cmd := s.dispatchShortcut(key, nil); handled {
		return true, cmd
	}
	return false, nil
}

func (s *state) dispatchShortcut(key string, defaultCmd []string) (bool, tea.Cmd) {
	switch key {
	case shortcutNew:
		s.openNewSessionModal(defaultCmd)
		return true, nil
	case shortcutNext:
		if s.manager != nil && s.manager.Len() > 0 {
			s.mode = modeNormal
			s.manager.Focus((s.manager.FocusedIndex() + 1) % s.manager.Len())
			s.refreshFocused()
			s.notifyMeta()
		}
		return true, nil
	case shortcutPrev:
		if s.manager != nil && s.manager.Len() > 0 {
			s.mode = modeNormal
			s.manager.Focus((s.manager.FocusedIndex() - 1 + s.manager.Len()) % s.manager.Len())
			s.refreshFocused()
			s.notifyMeta()
		}
		return true, nil
	case shortcutQuit:
		s.mode = modeQuitConfirm
		s.exitChoice = 0
		return true, nil
	case shortcutForceResize:
		s.forceResizeFocused()
		return true, nil
	}
	return false, nil
}

func (s *state) forceResizeFocused() {
	if s.manager == nil || s.manager.Len() == 0 {
		return
	}
	geom := s.geometry()
	if geom.Pane.Width <= 0 || geom.Pane.Height <= 0 {
		return
	}
	s.manager.ResizeOne(s.manager.FocusedIndex(), geom.Pane.Width, geom.Pane.Height)
	s.statusMsg = fmt.Sprintf("forced resize to %dx%d", geom.Pane.Width, geom.Pane.Height)
}

func (s *state) handleShortcut(m Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.Keystroke()
	if handled, cmd := s.dispatchShortcut(key, m.agentCmd); handled {
		return true, cmd
	}
	switch key {
	case shortcutHelp:
		s.mode = modeHelp
		return true, nil
	case shortcutKill:
		if s.manager.Len() > 1 {
			idx := s.manager.FocusedIndex()
			delete(s.viewports, idx)
			delete(s.scrollbackCache, idx)
			delete(s.liveLines, idx)
			s.manager.Kill(idx)
			s.refreshFocused()
			s.notifyMeta()
		}
		return true, nil
	case shortcutRename:
		s.openSessionSelector()
		return true, nil
	case shortcutSessions:
		s.openSessionSelector()
		return true, nil
	case shortcutSaveLayout:
		s.saveLayout()
		return true, nil
	case shortcutConnections:
		s.openConnectionsModal()
		return true, nil
	case shortcutRenameConn:
		s.openConnectionsModal()
		return true, nil
	case shortcutNewConn:
		s.quickAddConnection(m)
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
		// Both modes keep mouse reporting on (app-mode forwards to the child,
		// select-mode owns wheel+selection). View() switches MouseMode and the
		// renderer emits the needed enable/disable transitions, so we must NOT
		// raw-reset mouse modes here — doing so would disable reporting after
		// the renderer already enabled it, silently breaking wheel scroll.
		return true, nil
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
	}
	return nil
}

func (s *state) resolveExitPrompt(m Model) tea.Cmd {
	id := s.exitPromptID
	choice := s.exitChoice
	if choice == 0 {
		if err := s.manager.Respawn(id); err != nil {
			s.exitError = fmt.Sprintf("respawn failed: %v", err)
			s.mode = modeExitPrompt
			return nil
		}
		s.exitError = ""
		s.mode = modeNormal
		// Respawn rebuilds the VT screen at the manager's cached cols/rows.
		// Push the active TUI viewer's pane size on top so the new PTY is
		// in sync with what the user is looking at, even if a browser had
		// previously sized this session differently via ResizeOne.
		geom := s.geometry()
		s.manager.ResizeOne(id, geom.Pane.Width, geom.Pane.Height)
		s.resetViewport(id, s.width, s.height)
		s.notifyMeta()
		return nil
	}
	s.exitError = ""
	s.mode = modeNormal
	if s.manager.Len() <= 1 {
		name := "session"
		if sess := s.manager.ByID(id); sess != nil {
			name = sess.Title()
		}
		s.deleteKind = "lastSession"
		s.deleteIndex = id
		s.deleteName = name
		s.deleteReturn = modeExitPrompt
		s.deleteChoice = 1
		s.mode = modeDeleteConfirm
		return nil
	}
	delete(s.viewports, id)
	delete(s.scrollbackCache, id)
	delete(s.liveLines, id)
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

func (s *state) openSessionSelector() {
	s.mode = modeSelecting
	s.selectFilter = ""
	s.selectFilterCursor = 0
	s.selectCursor = s.manager.FocusedIndex()
	s.selectScroll = 0
	s.selectMoving = false
	s.selectRenaming = false
	s.selectFiltering = false
}

func (s *state) handleSelectKey(m Model, msg tea.KeyPressMsg) tea.Cmd {
	if s.selectRenaming {
		s.handleSessionRenameInSelector(msg)
		return nil
	}
	if s.selectFiltering {
		s.handleSessionFilterInSelector(msg)
		return nil
	}
	matches := s.filteredSessions()
	key := msg.Key()
	switch key.Code {
	case tea.KeyEscape:
		if s.selectMoving {
			s.selectMoving = false
			return nil
		}
		s.mode = modeNormal
		s.selectCursor = 0
		return nil
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
		s.selectCursor = 0
		return nil
	case tea.KeyUp:
		if s.selectMoving {
			s.moveSessionInSelector(-1)
			return nil
		}
		if len(matches) > 0 {
			s.selectCursor = (s.selectCursor - 1 + len(matches)) % len(matches)
		}
		return nil
	case tea.KeyDown:
		if s.selectMoving {
			s.moveSessionInSelector(1)
			return nil
		}
		if len(matches) > 0 {
			s.selectCursor = (s.selectCursor + 1) % len(matches)
		}
		return nil
	case tea.KeyPgUp:
		if len(matches) > 0 {
			step := s.selectorPageStep()
			s.selectCursor -= step
			if s.selectCursor < 0 {
				s.selectCursor = 0
			}
		}
		return nil
	case tea.KeyPgDown:
		if len(matches) > 0 {
			step := s.selectorPageStep()
			s.selectCursor += step
			if s.selectCursor >= len(matches) {
				s.selectCursor = len(matches) - 1
			}
		}
		return nil
	case tea.KeyDelete:
		s.confirmDeleteSessionAtSelectorCursor()
		return nil
	}
	switch msg.String() {
	case "n", "N":
		s.openNewSessionModalWithReturn(m.agentCmd, modeSelecting)
		return nil
	case "r", "R":
		if len(matches) > 0 {
			s.selectRenaming = true
			s.renameText = matches[s.selectCursor].Title()
			s.renameCursor = len([]rune(s.renameText))
		}
		return nil
	case "f", "F":
		s.selectFiltering = true
		s.selectFilterCursor = len([]rune(s.selectFilter))
		return nil
	case "m", "M":
		s.selectMoving = !s.selectMoving
		return nil
	case "x", "X", "d", "D":
		s.confirmDeleteSessionAtSelectorCursor()
		return nil
	}
	return nil
}

func (s *state) handleSessionRenameInSelector(msg tea.KeyPressMsg) {
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
		matches := s.filteredSessions()
		if len(matches) > 0 && strings.TrimSpace(s.renameText) != "" {
			s.manager.Rename(matches[s.selectCursor].Index(), strings.TrimSpace(s.renameText))
			s.notifyMeta()
		}
		s.selectRenaming = false
		s.renameText = ""
		s.renameCursor = 0
	case tea.KeyEscape:
		s.selectRenaming = false
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

func (s *state) handleSessionFilterInSelector(msg tea.KeyPressMsg) {
	key := msg.Key()
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
	case tea.KeyEnter:
		s.selectFiltering = false
		s.selectCursor = min(s.selectCursor, max(0, len(s.filteredSessions())-1))
	case tea.KeyEscape:
		s.selectFiltering = false
	case tea.KeyBackspace:
		s.selectFilter, s.selectFilterCursor = backspaceAt(s.selectFilter, s.selectFilterCursor)
		s.selectCursor = 0
	case tea.KeyDelete:
		s.selectFilter, s.selectFilterCursor = deleteAt(s.selectFilter, s.selectFilterCursor)
		s.selectCursor = 0
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
	}
}

func (s *state) moveSessionInSelector(delta int) {
	matches := s.filteredSessions()
	if len(matches) == 0 || s.selectCursor < 0 || s.selectCursor >= len(matches) {
		return
	}
	from := matches[s.selectCursor].Index()
	to := from + delta
	if to < 0 || to >= s.manager.Len() {
		return
	}
	s.moveSession(from, to)
	s.selectCursor = s.filteredSessionCursorForIndex(to)
}

func (s *state) moveSession(from, to int) {
	if s.manager == nil || from < 0 || to < 0 || from >= s.manager.Len() || to >= s.manager.Len() || from == to {
		return
	}
	conn := s.activeConnection()
	conn.viewports = moveIndexedMap(conn.viewports, from, to)
	conn.altScreens = moveIndexedMap(conn.altScreens, from, to)
	conn.scrollbackMode = moveIndexedMap(conn.scrollbackMode, from, to)
	conn.scrollbackCache = moveIndexedMap(conn.scrollbackCache, from, to)
	conn.liveLines = moveIndexedMap(conn.liveLines, from, to)
	s.manager.Move(from, to)
	s.syncActiveConnectionFields()
	s.notifyMeta()
}

func moveIndexedMap[T any](values map[int]T, from, to int) map[int]T {
	moved := make(map[int]T, len(values))
	for index, value := range values {
		next := index
		switch {
		case index == from:
			next = to
		case from < to && index > from && index <= to:
			next--
		case from > to && index >= to && index < from:
			next++
		}
		moved[next] = value
	}
	return moved
}

func (s *state) confirmDeleteSessionAtSelectorCursor() {
	matches := s.filteredSessions()
	if len(matches) == 0 || s.manager.Len() <= 1 {
		return
	}
	if s.selectCursor >= len(matches) {
		s.selectCursor = len(matches) - 1
	}
	sess := matches[s.selectCursor]
	s.deleteKind = "session"
	s.deleteIndex = sess.Index()
	s.deleteName = sess.Title()
	s.deleteReturn = modeSelecting
	s.deleteChoice = 1
	s.mode = modeDeleteConfirm
}

func (s *state) removeSessionAtSelectorCursor() {
	matches := s.filteredSessions()
	if len(matches) == 0 || s.manager.Len() <= 1 {
		return
	}
	if s.selectCursor >= len(matches) {
		s.selectCursor = len(matches) - 1
	}
	idx := matches[s.selectCursor].Index()
	delete(s.viewports, idx)
	delete(s.scrollbackCache, idx)
	delete(s.liveLines, idx)
	s.manager.Kill(idx)
	s.refreshFocused()
	s.notifyMeta()
	matches = s.filteredSessions()
	if s.selectCursor >= len(matches) {
		s.selectCursor = len(matches) - 1
	}
	if s.selectCursor < 0 {
		s.selectCursor = 0
	}
}

func (s *state) filteredSessionCursorForIndex(index int) int {
	matches := s.filteredSessions()
	for i, sess := range matches {
		if sess.Index() == index {
			return i
		}
	}
	if len(matches) == 0 {
		return 0
	}
	return min(s.selectCursor, len(matches)-1)
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
	step := s.geometry().Pane.Height*80/100 - chrome
	if step < 1 {
		step = 1
	}
	return step
}

func (m Model) renderTabBar() string {
	s := m.s
	geom := s.geometry()
	cols := geom.TabBar.Width
	focused := s.manager.FocusedIndex()
	sessions := s.manager.Sessions()

	s.sessionHitboxes = nil
	s.hasNewSessionHitbox = false

	brand := ""
	if !s.usingLeftRail() {
		brand = brandStyle.Render("multicrum")
	}
	brandW := lipgloss.Width(brand)

	// Build each tab as a styled string with its measured width.
	tabs := make([]string, len(sessions))
	widths := make([]int, len(sessions))
	for i, sess := range sessions {
		label := fmt.Sprintf("[%d] %s", sess.Index()+1, sess.Title())
		var tab string
		style := tabInactiveStyle
		suffix := ""
		switch {
		case sess.Exited() && sess.Index() == focused:
			style = tabActiveStyle
			suffix = " ✗"
		case sess.Exited():
			style = tabExitedStyle
			suffix = " ✗"
		case sess.Index() == focused:
			style = tabActiveStyle
		}
		if status, ok := s.agentStatus(sess); ok {
			tab = s.renderAgentContainer(style, label+" · ", suffix, status, 1)
		} else {
			tab = style.Render(label + suffix)
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
	if total <= cols || cols <= 0 {
		x := 0
		for i, w := range widths {
			if i < len(sessions) {
				s.sessionHitboxes = append(s.sessionHitboxes, mouseHitbox{Bounds: rect{X: geom.TabBar.X + x, Y: geom.TabBar.Y, Width: w, Height: 1}, Index: sessions[i].Index(), Action: hitboxSession})
			}
			x += w
		}
		s.newSessionHitbox = mouseHitbox{Bounds: rect{X: geom.TabBar.X + x, Y: geom.TabBar.Y, Width: newTabW, Height: 1}, Action: hitboxNewSession}
		s.hasNewSessionHitbox = true
		bar := lipgloss.JoinHorizontal(lipgloss.Top, append(tabs, newTab)...)
		if pad := cols - lipgloss.Width(bar) - brandW; pad > 0 {
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
		if end < len(tabs) && used+widths[end] <= cols-newTabW-brandW {
			used += widths[end]
			end++
			grew = true
		}
		if start > 0 && used+widths[start-1] <= cols-newTabW-brandW {
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
	for used+reserved > cols-newTabW-brandW && end-start > 1 {
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
	x := 0
	if needLeft {
		parts = append(parts, left)
		x += leftW
	}
	for i := start; i < end; i++ {
		parts = append(parts, tabs[i])
		if i < len(sessions) {
			s.sessionHitboxes = append(s.sessionHitboxes, mouseHitbox{Bounds: rect{X: geom.TabBar.X + x, Y: geom.TabBar.Y, Width: widths[i], Height: 1}, Index: sessions[i].Index(), Action: hitboxSession})
		}
		x += widths[i]
	}
	if needRight {
		parts = append(parts, right)
		x += rightW
	}
	s.newSessionHitbox = mouseHitbox{Bounds: rect{X: geom.TabBar.X + x, Y: geom.TabBar.Y, Width: newTabW, Height: 1}, Action: hitboxNewSession}
	s.hasNewSessionHitbox = true
	parts = append(parts, newTab)
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	if pad := cols - lipgloss.Width(bar) - brandW; pad > 0 {
		bar += tabBarStyle.Render(strings.Repeat(" ", pad))
	}
	bar += brand
	// If even that overflows (a single tab wider than the screen), hard
	// truncate so we never wrap the bar onto a second row.
	if lipgloss.Width(bar) > cols {
		bar = ansi.Truncate(bar, cols, "")
	}
	return bar
}

func (m Model) renderPane() string {
	s := m.s
	idx := s.manager.FocusedIndex()
	geom := s.geometry()
	paneCols, paneRows := geom.Pane.Width, geom.Pane.Height
	var pane string
	if vp, ok := s.viewports[idx]; ok {
		pane = s.renderPaneContent(idx, vp, paneCols, paneRows, s.scrollbackMode[idx])
		if !s.mouseCapture {
			pane = s.overlaySelection(pane, paneCols, paneRows)
		}
		pane = s.overlaySearch(pane, paneCols, paneRows)
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
	case modeFilePicker:
		return m.overlayBox(pane, m.renderFilePickerModal())
	case modeSelecting:
		return m.overlayBox(pane, m.renderSessionSelectorModal())
	case modeConnections:
		return m.overlayBox(pane, m.renderConnectionsModal())
	case modeSettings:
		return m.overlayBox(pane, m.renderSettingsModal())
	case modeQuitConfirm:
		return m.overlayBox(pane, m.renderQuitConfirmModal())
	case modeDeleteConfirm:
		return m.overlayBox(pane, m.renderDeleteConfirmModal())
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
func (s *state) renderPaneContent(idx int, vp *viewport.Model, paneCols, paneRows int, wrap bool) string {
	content := ""
	var wrappedRows []string
	if wrap {
		if cache, ok := s.scrollbackCache[idx]; ok && cache.cols == paneCols {
			content = cache.content
			wrappedRows = cache.rows
		} else {
			content = vp.GetContent()
		}
	} else {
		content = vp.GetContent()
	}
	yoff := vp.YOffset()
	if c := &s.paneCache; c.valid && c.wrap == wrap && c.cols == paneCols &&
		c.rows == paneRows && c.yoff == yoff && c.content == content {
		return c.out
	}
	var out string
	if wrappedRows != nil {
		out = renderPaneRows(wrappedRows, paneCols, paneRows, yoff)
	} else {
		out = renderPaneContentUncached(content, paneCols, paneRows, wrap, yoff)
	}
	s.paneCache = paneCache{
		valid: true, content: content, yoff: yoff,
		cols: paneCols, rows: paneRows, wrap: wrap, out: out,
	}
	return out
}

func renderPaneContentUncached(content string, paneCols, paneRows int, wrap bool, yoff int) string {
	if content == "" {
		return blankPane(paneCols, paneRows)
	}
	all := strings.Split(content, "\n")
	if wrap {
		// In scrollback mode the content is the full logical history, whose
		// lines can be wider than the pane. The viewport computes YOffset in
		// *soft-wrapped* row space (see viewport.calculateLine), so we must
		// expand each logical line into the same wrapped rows before applying
		// YOffset — otherwise the offset indexes logical lines while the scroll
		// math counts wrapped rows, and the two drift apart, duplicating and
		// dropping rows as the user scrolls. This mirrors viewport.softWrap
		// exactly (fixed paneCols chunks via ansi.Cut). We only do this for
		// scrollback; the live/alt-screen path keeps its strict no-wrap
		// behavior so cell-accurate grids (btop dialogs) never shift.
		all = softWrapRows(all, paneCols)
	}
	return renderPaneRows(all, paneCols, paneRows, yoff)
}

func renderPaneRows(all []string, paneCols, paneRows, yoff int) string {
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

// softWrapRows expands logical lines into fixed-width wrapped rows exactly the
// way the viewport's SoftWrap renderer does (chunks of maxWidth via ansi.Cut),
// so a caller windowing by the viewport's YOffset stays row-aligned.
func softWrapRows(lines []string, maxWidth int) []string {
	rows, _ := softWrapRowsWithPlain(lines, maxWidth)
	return rows
}

func softWrapRowsWithPlain(lines []string, maxWidth int) ([]string, []session.BufferLine) {
	if maxWidth <= 0 {
		plain := make([]session.BufferLine, len(lines))
		for i, line := range lines {
			plain[i].Text = strings.TrimRight(ansi.Strip(line), " ")
		}
		return lines, plain
	}
	out := make([]string, 0, len(lines))
	plain := make([]session.BufferLine, 0, len(lines))
	for _, line := range lines {
		w := ansi.StringWidth(line)
		if w <= maxWidth {
			out = append(out, line)
			plain = append(plain, session.BufferLine{Text: strings.TrimRight(ansi.Strip(line), " ")})
			continue
		}
		for idx := 0; idx < w; idx += maxWidth {
			row := ansi.Cut(line, idx, maxWidth+idx)
			softWrap := idx+maxWidth < w
			text := ansi.Strip(row)
			if !softWrap {
				text = strings.TrimRight(text, " ")
			}
			out = append(out, row)
			plain = append(plain, session.BufferLine{
				Text:     text,
				SoftWrap: softWrap,
			})
		}
	}
	return out, plain
}

// anchorViewportToCursor scrolls vp so the vt cursor row is visible inside
// the pane. This is necessary when the vt screen is taller than the pane —
// e.g. when a browser xterm resized the PTY to more rows than the TUI shows.
// We keep the cursor in the bottom-most region without ever putting it past
// the visible area, which makes the shell prompt and its output visible
// regardless of where in the vt grid the cursor currently sits.
func anchorViewportToCursor(vp *viewport.Model, sess *session.Session) {
	cur := sess.Screen().Cursor()
	h := vp.Height()
	if h <= 0 {
		vp.GotoBottom()
		return
	}
	desired := cur.Y - h + 1
	if desired < 0 {
		desired = 0
	}
	total := vp.TotalLineCount()
	maxOff := total - h
	if maxOff < 0 {
		maxOff = 0
	}
	if desired > maxOff {
		desired = maxOff
	}
	vp.SetYOffset(desired)
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
	geom := s.geometry()
	cols, rows := geom.Pane.Width, geom.Pane.Height
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)
	left := max(0, (cols-boxWidth)/2)
	top := max(0, (rows-boxHeight)/2)
	return overlayBoxAt(pane, box, left, top, cols, rows)
}

// overlayBoxAt paints box at a pane-relative position. Callers that anchor an
// overlay to mouse coordinates must clamp their bounds before using it.
func overlayBoxAt(pane, box string, left, top, cols, rows int) string {
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
	if s.exitError != "" {
		rows = append(rows, "", "Error:")
		rows = append(rows, wrapText(s.exitError, width, 1)...)
	}
	return padBox(rows, width)
}

func padBox(rows []string, width int) string {
	return padBoxWithStyle(rows, width, helpModalStyle)
}

func padBoxWithStyle(rows []string, width int, style lipgloss.Style) string {
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
	return style.Render(strings.Join(rows, "\n"))
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
		"Ctrl+Alt+O           connections modal",
		"Ctrl+Alt+E           rename connection",
		"Ctrl+Alt+C           quick-create connection",
		"Ctrl+Alt+[ / ]       previous/next connection",
		"Ctrl+Alt+P           save layout to --config file",
		"Ctrl+Alt+Left/Right  previous/next session",
		"Ctrl+Alt+1..9        jump to session",
		"Ctrl+Alt+PgUp/PgDown page scrollback",
		"Ctrl+Alt+Up/Down     line scrollback",
		"Ctrl+Alt+Home/End    top/bottom of scrollback",
		"/ (in scrollback)    search buffer; n / N next/prev match",
		": (in scrollback)    jump to line number",
		"Ctrl+Alt+M           toggle mouse mode (select ↔ app)",
		"Ctrl+Alt+Z           force active session resize",
		"Wheel (select mode)  scroll the scrollback buffer",
		"Right-click pane     copy selection, exit scrollback",
		"Right-click tab      focus/rename/move/remove menu",
		"Shift+drag           native terminal selection / copy",
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
	geom := s.geometry()
	width := geom.Pane.Width * 80 / 100
	if width > 76 {
		width = 76
	}
	if width < 30 {
		width = 30
	}

	listRows := s.sessionSelectorListRows()

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

	rows := []string{"Sessions"}
	if strings.TrimSpace(s.selectFilter) != "" || s.selectFiltering {
		filter := renderWithCursor(s.selectFilter, s.selectFilterCursor)
		if !s.selectFiltering {
			filter = s.selectFilter
		}
		rows = append(rows, scrollIndicatorStyle.Render("Filter: "+truncate(filter, width-10)))
	}
	rows = append(rows, "")
	if s.selectRenaming {
		rows = append(rows, "Rename: "+renderWithCursor(s.renameText, s.renameCursor), "")
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
			prefix := fmt.Sprintf("[%d] %s  %s", sess.Index()+1, sess.Title(), state)
			status, hasAgent := s.agentStatus(sess)
			var line string
			if i == s.selectCursor {
				style := selectorActiveStyle
				marker := "▶ "
				if s.selectMoving {
					style = selectorMovingStyle
					marker = "↕ "
				}
				if hasAgent {
					line = s.renderAgentContainer(style, marker+prefix+" · ", "", status, 1)
				} else {
					line = style.Render(marker + prefix)
				}
			} else {
				line = "  " + prefix
				if hasAgent {
					line += " · " + s.renderAgentLabel(lipgloss.NewStyle(), status, 1)
				}
			}
			if ansi.StringWidth(line) > width {
				line = ansi.Truncate(line, width, "")
			}
			rows = append(rows, line)
		}
		// Pad to listRows so footer position stays fixed.
		for i := end - s.selectScroll; i < listRows; i++ {
			rows = append(rows, "")
		}
	}

	footer := "↑/↓ select   Enter focus   N new   R rename   M move   F filter   Del/X remove   Esc cancel"
	if s.selectMoving {
		footer = "Move: ↑/↓ reorder   M/Esc stop moving"
	}
	if s.selectFiltering {
		footer = "Filter: type pattern   Enter apply   Esc actions   Ctrl+U clear"
	}
	if s.selectRenaming {
		footer = "Rename: Enter save   Esc cancel   Backspace edits"
	}
	if len(matches) > listRows {
		footer = fmt.Sprintf("%d/%d   %s", s.selectCursor+1, len(matches), footer)
	}
	rows = append(rows, "", footer)

	return padBox(rows, width)
}

func (m Model) renderStatusBar() string {
	s := m.s
	geom := s.geometry()
	cols, rows := geom.Pane.Width, geom.Pane.Height
	s.connectionHitboxes = nil
	s.hasHelpHitbox = false
	s.hasConnectionsHitbox = false
	mouseTag := "mouse:select"
	if s.mouseCapture {
		mouseTag = "mouse:app"
	}
	serverPrefix := statusKeyStyle.Render(fmt.Sprintf(" server:%s │ ", s.serverName))
	connectionsLabel := statusKeyStyle.Render("conn ")
	if geom.ConnectionRail.Width > 0 {
		if active := s.activeConnection(); active != nil {
			connectionsLabel = statusKeyStyle.Render("conn:" + truncate(active.name, 12) + " ")
		}
	}
	prefix := serverPrefix + connectionsLabel
	connStart := lipgloss.Width(serverPrefix)
	s.connectionsHitbox = mouseHitbox{Bounds: rect{X: geom.StatusBar.X + connStart, Y: geom.StatusBar.Y, Width: lipgloss.Width(connectionsLabel), Height: 1}, Action: hitboxConnections}
	s.hasConnectionsHitbox = true
	pills := ""
	if geom.ConnectionRail.Width == 0 {
		pills = s.renderConnectionPills()
	}
	prefixWidth := lipgloss.Width(prefix)
	for i := range s.connectionHitboxes {
		s.connectionHitboxes[i].Bounds.X += prefixWidth
	}
	status := prefix + pills + statusKeyStyle.Render(fmt.Sprintf(" │ %dx%d │ clients:%d │ %s ", cols, rows, s.localClients+1, mouseTag))
	if s.mode == modeRenaming {
		help := helpStyle.Render(" Rename: " + renderWithCursor(s.renameText, s.renameCursor) + "  Enter save  Esc cancel")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeSelecting {
		help := helpStyle.Render(" Sessions — see modal")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeConnections {
		help := helpStyle.Render(" Connections — see modal")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeSettings {
		help := helpStyle.Render(" Settings — arrows change, Esc close")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeHelp {
		help := helpStyle.Render(" Help: Esc/Enter close")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeQuitConfirm {
		help := helpStyle.Render(" Confirm quit — Y/Enter quit, N/Esc cancel")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeExitPrompt {
		help := helpStyle.Render(" Session exited — choose action in modal")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	if s.mode == modeScrollSearch {
		prefix := "/"
		if s.search.lineJump {
			prefix = ":"
		}
		help := helpStyle.Render(" " + prefix + renderWithCursor(s.search.input, len([]rune(s.search.input))) + "  Enter go  Esc cancel")
		left := status
		if pad := geom.StatusBar.Width - lipgloss.Width(left) - lipgloss.Width(help); pad > 0 {
			left += statusBarStyle.Render(strings.Repeat(" ", pad))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, help)
	}
	help := helpStyle.Render(" Alt+` help")
	if s.layoutFallback {
		help = helpStyle.Render(" left layout temporarily using bottom (terminal too narrow) ")
	}
	if s.statusMsg != "" {
		help = helpStyle.Render(" " + s.statusMsg + " ")
	} else {
		helpStart := lipgloss.Width(status)
		s.helpHitbox = mouseHitbox{Bounds: rect{X: geom.StatusBar.X + helpStart, Y: geom.StatusBar.Y, Width: lipgloss.Width(help), Height: 1}, Action: hitboxHelp}
		s.hasHelpHitbox = true
	}
	left := status + help
	if pad := geom.StatusBar.Width - lipgloss.Width(left); pad > 0 {
		left += statusBarStyle.Render(strings.Repeat(" ", pad))
	}
	return left
}
