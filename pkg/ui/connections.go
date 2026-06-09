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
	s.manager = c.manager
	s.viewports = c.viewports
	s.altScreens = c.altScreens
	s.scrollbackMode = c.scrollbackMode
}

func (s *state) addConnection(name string) *connectionState {
	if name == "" {
		name = fmt.Sprintf("connection-%d", len(s.connections)+1)
	}
	c := &connectionState{
		name:           name,
		viewports:      make(map[int]*viewport.Model),
		altScreens:     make(map[int]bool),
		scrollbackMode: make(map[int]bool),
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
	s.rebindConnectionCallbacks()
	s.notifyMeta()
}

func (s *state) rebindConnectionCallbacks() {
	for i, conn := range s.connections {
		if conn.manager == nil {
			continue
		}
		idx := i
		conn.manager.SetSendOutput(func(msg session.OutputMsg) {
			if s.program != nil {
				s.program.Send(connectionOutputMsg{Conn: idx, Msg: msg})
			}
			if s.wsTransport != nil && idx == s.activeConn {
				s.wsTransport.SendPTY(msg.Index, msg.Data)
			}
		})
		conn.manager.SetSendExit(func(msg session.ExitMsg) {
			if s.program != nil {
				s.program.Send(connectionExitMsg{Conn: idx, Msg: msg})
			}
		})
	}
}

func (s *state) removeConnection(index int) {
	if len(s.connections) <= 1 || index < 0 || index >= len(s.connections) {
		return
	}
	for _, sess := range s.connections[index].manager.Sessions() {
		_ = sess.Close()
	}
	s.connections = append(s.connections[:index], s.connections[index+1:]...)
	if s.activeConn >= len(s.connections) {
		s.activeConn = len(s.connections) - 1
	}
	if s.activeConn < 0 {
		s.activeConn = 0
	}
	s.syncActiveConnectionFields()
	s.rebindConnectionCallbacks()
	s.notifyMeta()
}

func (s *state) createConnectionWithDefaultSession(name string, m Model) *connectionState {
	conn := s.addConnection(name)
	cols, rows := paneSize(s.width, s.height)
	idx := len(s.connections) - 1
	conn.manager = session.NewManagerWithSSH(cols, rows,
		func(msg session.OutputMsg) {
			if s.program != nil {
				s.program.Send(connectionOutputMsg{Conn: idx, Msg: msg})
			}
		},
		func(msg session.ExitMsg) {
			if s.program != nil {
				s.program.Send(connectionExitMsg{Conn: idx, Msg: msg})
			}
		},
		s.sshClient)
	if sess, err := conn.manager.New(m.agentCmd); err == nil && m.agentCmdLine != "" {
		sess.SetCmdLine(m.agentCmdLine)
	}
	s.rebindConnectionCallbacks()
	return conn
}

func (s *state) initManagers(cols, rows int) {
	if len(s.connections) == 0 {
		s.addConnection("default")
	}
	for i, c := range s.connections {
		if c.manager == nil {
			idx := i
			c.manager = session.NewManagerWithSSH(cols, rows,
				func(msg session.OutputMsg) {
					if s.program != nil {
						s.program.Send(connectionOutputMsg{Conn: idx, Msg: msg})
					}
				},
				func(msg session.ExitMsg) {
					if s.program != nil {
						s.program.Send(connectionExitMsg{Conn: idx, Msg: msg})
					}
				},
				s.sshClient)
		}
	}
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
	if cfg == nil || len(cfg.Connections) == 0 {
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
			sessions = append(sessions, startupSession{Title: entry.Title, Cmd: cmd, CmdLine: entry.CmdLine, SSH: entry.SSH})
		}
		entries = append(entries, startupConnection{Name: conn.Name, Sessions: sessions})
	}
	m.SetInitialConnections(entries, cfg.ActiveConnection)
}
