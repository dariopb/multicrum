package session

import (
	"fmt"
	"sync"
)

// SessionManager holds all active sessions.
type SessionManager struct {
	mu       sync.Mutex
	sessions []*Session
	focused  int
	cols     int
	rows     int
	// SendOutput is called whenever a session produces output.
	SendOutput func(msg OutputMsg)
	// SendExit is called once when a session's child process exits.
	SendExit func(msg ExitMsg)
}

// NewManager creates a SessionManager with initial terminal dimensions.
func NewManager(cols, rows int, sendOutput func(OutputMsg), sendExit func(ExitMsg)) *SessionManager {
	return &SessionManager{
		cols:       cols,
		rows:       rows,
		SendOutput: sendOutput,
		SendExit:   sendExit,
	}
}

// SendOutputFn returns the current output callback.
func (m *SessionManager) SendOutputFn() func(OutputMsg) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.SendOutput
}

// SetSendOutput replaces the output callback (used to chain WS transport).
func (m *SessionManager) SetSendOutput(fn func(OutputMsg)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SendOutput = fn
	for _, s := range m.sessions {
		s.SendOutput = fn
	}
}

// New creates, starts, and appends a new session.
func (m *SessionManager) New(cmd []string) (*Session, error) {
	m.mu.Lock()
	idx := len(m.sessions)
	s, err := newSession(idx, cmd, m.cols, m.rows)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("new session: %w", err)
	}
	s.SendOutput = m.SendOutput
	s.SendExit = m.SendExit
	m.sessions = append(m.sessions, s)
	m.focused = idx
	m.mu.Unlock()

	if err := s.Start(m.cols, m.rows); err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}
	return s, nil
}

// Focus sets the active session by index.
func (m *SessionManager) Focus(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= 0 && index < len(m.sessions) {
		m.focused = index
	}
}

// Rename updates the display title for a session.
func (m *SessionManager) Rename(index int, title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= 0 && index < len(m.sessions) {
		m.sessions[index].SetTitle(title)
	}
}

// FocusedIndex returns the currently focused session index.
func (m *SessionManager) FocusedIndex() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.focused
}

// Kill stops the session at index and removes it.
func (m *SessionManager) Kill(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) <= 1 || index < 0 || index >= len(m.sessions) {
		return
	}
	_ = m.sessions[index].Close()
	m.sessions = append(m.sessions[:index], m.sessions[index+1:]...)
	// Re-index remaining sessions.
	for i, s := range m.sessions {
		s.index = i
	}
	if m.focused >= len(m.sessions) && m.focused > 0 {
		m.focused = len(m.sessions) - 1
	}
}

// Sessions returns a snapshot of all sessions.
func (m *SessionManager) Sessions() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, len(m.sessions))
	copy(out, m.sessions)
	return out
}

// Focused returns the currently focused session (nil if none).
func (m *SessionManager) Focused() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) == 0 {
		return nil
	}
	return m.sessions[m.focused]
}

// Len returns number of sessions.
func (m *SessionManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// ResizeOne resizes a single session by ID.
func (m *SessionManager) ResizeOne(id, cols, rows int) {
	m.mu.Lock()
	snap := make([]*Session, len(m.sessions))
	copy(snap, m.sessions)
	m.mu.Unlock()
	for _, s := range snap {
		if s.index == id {
			_ = s.Resize(cols, rows)
			return
		}
	}
}

// ResizeAll resizes every session.
func (m *SessionManager) ResizeAll(cols, rows int) {
	m.mu.Lock()
	m.cols = cols
	m.rows = rows
	snap := make([]*Session, len(m.sessions))
	copy(snap, m.sessions)
	m.mu.Unlock()
	for _, s := range snap {
		_ = s.Resize(cols, rows)
	}
}

// Respawn relaunches the child process for the session at index.
func (m *SessionManager) Respawn(index int) error {
	m.mu.Lock()
	if index < 0 || index >= len(m.sessions) {
		m.mu.Unlock()
		return fmt.Errorf("respawn: index out of range")
	}
	s := m.sessions[index]
	cols, rows := m.cols, m.rows
	m.mu.Unlock()
	return s.Respawn(cols, rows)
}

// ByID returns the session with the given index (or nil).
func (m *SessionManager) ByID(id int) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.index == id {
			return s
		}
	}
	return nil
}
