package ui

import (
	"fmt"

	"charm.land/bubbles/v2/viewport"
	"multicrum/pkg/config"
	"multicrum/pkg/session"
)

func (s *state) activeConnection() *connectionState {
	if len(s.connections) == 0 {
		s.addConnection("default")
	}
	if s.activeConn < 0 || s.activeConn >= len(s.connections) {
		s.activeConn = 0
	}
	return s.connections[s.activeConn]
}

func (s *state) syncActiveConnectionFields() {
	c := s.activeConnection()
	if c.scrollbackCache == nil {
		c.scrollbackCache = make(map[int]scrollWrapCache)
	}
	if c.liveLines == nil {
		c.liveLines = make(map[int][]session.BufferLine)
	}
	for _, conn := range s.connections {
		conn.webActive.Store(conn == c)
	}
	s.manager = c.manager
	s.viewports = c.viewports
	s.altScreens = c.altScreens
	s.scrollbackMode = c.scrollbackMode
	s.scrollbackCache = c.scrollbackCache
	s.liveLines = c.liveLines
}

func (s *state) addConnection(name string) *connectionState {
	if name == "" {
		name = fmt.Sprintf("connection-%d", len(s.connections)+1)
	}
	c := &connectionState{
		name:            name,
		viewports:       make(map[int]*viewport.Model),
		altScreens:      make(map[int]bool),
		scrollbackMode:  make(map[int]bool),
		scrollbackCache: make(map[int]scrollWrapCache),
		liveLines:       make(map[int][]session.BufferLine),
	}
	s.connections = append(s.connections, c)
	if len(s.connections) == 1 {
		s.activeConn = 0
		s.syncActiveConnectionFields()
	}
	return c
}

func (s *state) focusConnection(index int) {
	if len(s.connections) == 0 {
		s.addConnection("default")
	}
	if index < 0 {
		index = len(s.connections) - 1
	}
	if index >= len(s.connections) {
		index = 0
	}
	s.activeConn = index
	s.syncActiveConnectionFields()
	s.clearSelection()
	s.refreshFocused()
	s.notifyMeta()
}

func (s *state) focusConnectionByName(name string) bool {
	for i, c := range s.connections {
		if c.name == name {
			s.focusConnection(i)
			return true
		}
	}
	return false
}

func (s *state) renameConnection(index int, name string) {
	if index < 0 || index >= len(s.connections) || name == "" {
		return
	}
	s.connections[index].name = name
	s.notifyMeta()
}

func (s *state) moveConnection(from, to int) {
	if from < 0 || from >= len(s.connections) || to < 0 || to >= len(s.connections) || from == to {
		return
	}
	conn := s.connections[from]
	s.connections = append(s.connections[:from], s.connections[from+1:]...)
	s.connections = append(s.connections[:to], append([]*connectionState{conn}, s.connections[to:]...)...)
	if s.activeConn == from {
		s.activeConn = to
	} else if from < s.activeConn && to >= s.activeConn {
		s.activeConn--
	} else if from > s.activeConn && to <= s.activeConn {
		s.activeConn++
	}
	s.syncActiveConnectionFields()
	s.notifyMeta()
}

func (s *state) rebindConnectionCallbacks() {
	for _, conn := range s.connections {
		s.bindConnectionCallbacks(conn)
	}
}

func (s *state) bindConnectionCallbacks(conn *connectionState) {
	if conn == nil || conn.manager == nil || conn.callbacksBound {
		return
	}
	conn.callbacksBound = true
	conn.manager.SetSendOutput(func(msg session.OutputMsg) {
		// Browsers need every chunk of raw PTY bytes for a faithful replay,
		// so forward those unconditionally (cheap byte copy to the socket).
		if s.wsTransport != nil && conn.webActive.Load() {
			s.wsTransport.SendPTY(msg.Index, msg.Data)
		}
		// Coalesce the Bubble Tea notification: the renderer rebuilds the
		// whole View() on every message, so collapsing a burst of small
		// child writes into a single connectionOutputMsg (until the model
		// consumes it) is what keeps a chatty child from saturating the
		// event loop with throwaway view rebuilds. The bytes are already in
		// the VTScreen, so nothing is dropped.
		if s.program != nil && !conn.outputPending.Swap(true) {
			s.program.Send(connectionOutputMsg{Conn: conn, Msg: msg})
		}
	})
	conn.manager.SetSendExit(func(msg session.ExitMsg) {
		if s.program != nil {
			s.program.Send(connectionExitMsg{Conn: conn, Msg: msg})
		}
	})
}

func (s *state) removeConnection(index int) {
	if len(s.connections) <= 1 || index < 0 || index >= len(s.connections) {
		return
	}
	conn := s.connections[index]
	if conn.manager != nil {
		_ = conn.manager.CloseAll()
	}
	conn.viewports = nil
	conn.altScreens = nil
	conn.scrollbackMode = nil
	conn.scrollbackCache = nil
	conn.liveLines = nil
	s.connections = append(s.connections[:index], s.connections[index+1:]...)
	if s.activeConn >= len(s.connections) {
		s.activeConn = len(s.connections) - 1
	}
	if s.activeConn < 0 {
		s.activeConn = 0
	}
	s.syncActiveConnectionFields()
	s.notifyMeta()
}

func (s *state) createConnectionWithDefaultSession(name string, m Model) *connectionState {
	conn := s.addConnection(name)
	geom := s.geometry()
	conn.manager = session.NewManagerWithSSH(geom.Pane.Width, geom.Pane.Height, nil, nil, s.sshClient)
	// Bind before New starts the read loop so callback installation never
	// races live PTY output.
	s.bindConnectionCallbacks(conn)
	if sess, err := conn.manager.New(m.agentCmd); err == nil && m.agentCmdLine != "" {
		sess.SetCmdLine(m.agentCmdLine)
	}
	return conn
}

func (s *state) initManagers(cols, rows int) {
	if len(s.connections) == 0 {
		s.addConnection("default")
	}
	for _, c := range s.connections {
		if c.manager == nil {
			c.manager = session.NewManagerWithSSH(cols, rows, nil, nil, s.sshClient)
		}
	}
	s.rebindConnectionCallbacks()
	s.syncActiveConnectionFields()
}

func (m *Model) SetInitialConnections(entries []startupConnection, active string) {
	if len(entries) == 0 {
		return
	}
	m.s.connections = nil
	for _, entry := range entries {
		name := entry.Name
		if name == "" {
			name = fmt.Sprintf("connection-%d", len(m.s.connections)+1)
		}
		c := m.s.addConnection(name)
		c.initialCfg = entry.Sessions
	}
	m.s.activeConn = 0
	if active != "" {
		for i, c := range m.s.connections {
			if c.name == active {
				m.s.activeConn = i
				break
			}
		}
	}
	m.s.syncActiveConnectionFields()
}

func (m *Model) AddInitialConnection(name string, sessions []startupSession) {
	if len(m.s.connections) == 1 && m.s.connections[0].name == "default" && len(m.s.connections[0].initialCfg) == 0 && m.s.connections[0].manager == nil {
		m.s.connections = nil
	}
	c := m.s.addConnection(name)
	c.initialCfg = sessions
	m.s.syncActiveConnectionFields()
}

func (m *Model) SetConfigConnections(cfg *config.Config) {
	if cfg == nil {
		return
	}
	m.SetConnectionRailWidth(cfg.ConnectionRailWidth)
	m.SetConnectionLayout(cfg.ConnectionLayout)
	if len(cfg.Connections) == 0 {
		return
	}
	entries := make([]startupConnection, 0, len(cfg.Connections))
	for _, conn := range cfg.Connections {
		sessions := make([]startupSession, 0, len(conn.Sessions))
		for _, entry := range conn.Sessions {
			cmd := entry.Cmd
			if entry.CmdLine != "" {
				cmd = ParseCmdLine(entry.CmdLine)
			}
			sessions = append(sessions, startupSession{Title: entry.Title, Cmd: cmd, CmdLine: entry.CmdLine, Cwd: entry.Cwd, SSH: entry.SSH})
		}
		entries = append(entries, startupConnection{Name: conn.Name, Sessions: sessions})
	}
	m.SetInitialConnections(entries, cfg.ActiveConnection)
}

func (s *state) toggleConnectionLayout() {
	if s.connectionLayout == connectionLayoutLeft {
		s.connectionLayout = connectionLayoutBottom
	} else {
		s.connectionLayout = connectionLayoutLeft
	}
	s.applyGeometry()
}

// applyGeometry synchronizes every local TUI surface with the active layout.
// This is used for both terminal resizes and layout changes, keeping managers,
// viewports, cache, and hit targets in one coordinate system.
func (s *state) applyGeometry() {
	geom := s.geometry()
	s.layoutFallback = s.connectionLayout == connectionLayoutLeft && geom.ConnectionRail.Width == 0
	s.paneCache.valid = false
	s.sessionHitboxes = nil
	s.connectionHitboxes = nil
	s.hasNewSessionHitbox = false
	s.hasNewConnectionHitbox = false
	s.hasAppMenuHitbox = false
	s.hasHelpHitbox = false
	s.hasConnectionsHitbox = false
	for _, conn := range s.connections {
		if conn.manager != nil {
			conn.manager.ResizeAll(geom.Pane.Width, geom.Pane.Height)
		}
		for idx, vp := range conn.viewports {
			vp.SetWidth(geom.Pane.Width)
			vp.SetHeight(geom.Pane.Height)
			conn.viewports[idx] = vp
		}
	}
	if s.manager == nil || s.manager.Len() == 0 {
		return
	}
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	for _, sess := range s.manager.Sessions() {
		if sess.Index() != idx {
			continue
		}
		vp := s.viewports[idx]
		if s.scrollbackMode[idx] && !sess.Screen().IsAltScreen() {
			s.setScrollbackContent(idx, vp, sess.Screen().RenderWithScrollback())
		} else {
			delete(s.scrollbackCache, idx)
			vp.SoftWrap = true
			s.setLiveContent(idx, vp, sess)
			anchorViewportToCursor(vp, sess)
		}
		s.viewports[idx] = vp
		break
	}
}
