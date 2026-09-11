package session

import (
	"errors"
	"fmt"
	"sync"

	"multicrum/pkg/diagnostics"
	"multicrum/pkg/ssh_client"
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

	sshClient     *ssh_client.Client
	agentEndpoint string
	trace         *diagnostics.Recorder
}

// NewManager creates a SessionManager with initial terminal dimensions.
func NewManager(cols, rows int, sendOutput func(OutputMsg), sendExit func(ExitMsg)) *SessionManager {
	return NewManagerWithSSH(cols, rows, sendOutput, sendExit, nil)
}

// NewManagerWithSSH creates a SessionManager that starts SSH-backed sessions
// when sshClient is non-nil, otherwise local PTY/ConPTY sessions.
func NewManagerWithSSH(cols, rows int, sendOutput func(OutputMsg), sendExit func(ExitMsg), sshClient *ssh_client.Client) *SessionManager {
	return &SessionManager{
		cols:       cols,
		rows:       rows,
		SendOutput: sendOutput,
		SendExit:   sendExit,
		sshClient:  sshClient,
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
		s.setSendOutput(fn)
	}
}

func (m *SessionManager) SetSendExit(fn func(ExitMsg)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SendExit = fn
	for _, s := range m.sessions {
		s.setSendExit(fn)
	}
}

func (m *SessionManager) SetAgentStateEndpoint(endpoint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentEndpoint = endpoint
	for _, s := range m.sessions {
		s.mu.Lock()
		s.agentEndpoint = endpoint
		s.mu.Unlock()
	}
}

// New creates, starts, and appends a new session.
func (m *SessionManager) New(cmd []string) (*Session, error) {
	return m.newWithOptions(cmd, m.sshClient, "")
}

// NewInDir starts a local session in workDir when it is usable. An invalid
// directory is ignored so stale saved layouts never prevent session startup.
func (m *SessionManager) NewInDir(cmd []string, workDir string) (*Session, error) {
	return m.newWithOptions(cmd, m.sshClient, workDir)
}

// NewConfigured starts a session with explicit backend, directory, and
// environment settings.
func (m *SessionManager) NewConfigured(cmd []string, sshClient *ssh_client.Client, workDir string, env []string) (*Session, error) {
	m.mu.Lock()
	idx := len(m.sessions)
	s, err := newSession(idx, cmd, m.cols, m.rows, sshClient)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("new session: %w", err)
	}
	s.workDir = workDir
	s.extraEnv = append([]string(nil), env...)
	s.agentEndpoint = m.agentEndpoint
	s.setDiagnostics(m.trace)
	s.setCallbacks(m.SendOutput, m.SendExit)
	m.sessions = append(m.sessions, s)
	m.updateTerminalRepliesLocked()
	cols, rows := m.cols, m.rows
	m.mu.Unlock()

	if err := s.Start(cols, rows); err != nil {
		m.mu.Lock()
		for i, existing := range m.sessions {
			if existing == s {
				m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
				break
			}
		}
		for i, existing := range m.sessions {
			existing.setIndex(i)
		}
		m.updateTerminalRepliesLocked()
		m.mu.Unlock()
		_ = s.Close()
		return nil, fmt.Errorf("start session: %w", err)
	}
	return s, nil
}

// NewWithSSH creates, starts, and appends a new session using sshClient when
// non-nil, otherwise using the local PTY/ConPTY backend.
func (m *SessionManager) NewWithSSH(cmd []string, sshClient *ssh_client.Client) (*Session, error) {
	return m.newWithOptions(cmd, sshClient, "")
}

func (m *SessionManager) newWithOptions(cmd []string, sshClient *ssh_client.Client, workDir string) (*Session, error) {
	m.mu.Lock()
	idx := len(m.sessions)
	s, err := newSession(idx, cmd, m.cols, m.rows, sshClient)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("new session: %w", err)
	}
	s.workDir = workDir
	s.agentEndpoint = m.agentEndpoint
	s.setDiagnostics(m.trace)
	s.SendOutput = m.SendOutput
	s.SendExit = m.SendExit
	m.sessions = append(m.sessions, s)
	m.focused = idx
	m.updateTerminalRepliesLocked()
	m.mu.Unlock()

	if err := s.Start(m.cols, m.rows); err != nil {
		m.mu.Lock()
		for i, existing := range m.sessions {
			if existing == s {
				m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
				break
			}
		}
		for i, existing := range m.sessions {
			existing.setIndex(i)
		}
		if len(m.sessions) == 0 {
			m.focused = 0
		} else if idx > 0 {
			m.focused = idx - 1
		} else {
			m.focused = 0
		}
		m.updateTerminalRepliesLocked()
		m.mu.Unlock()
		_ = s.Close()
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
		m.updateTerminalRepliesLocked()
	}
}

func (m *SessionManager) updateTerminalRepliesLocked() {
	for _, s := range m.sessions {
		// Every session owns an independent emulator and PTY, so background
		// applications must receive their own CPR/DSR replies too. Tying
		// replies to focus makes applications started in another session time
		// out while probing terminal capabilities.
		s.Screen().SetTerminalReplies(true)
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
	if len(m.sessions) <= 1 || index < 0 || index >= len(m.sessions) {
		m.mu.Unlock()
		return
	}
	killed := m.sessions[index]
	m.sessions = append(m.sessions[:index], m.sessions[index+1:]...)
	// Re-index remaining sessions.
	for i, s := range m.sessions {
		s.setIndex(i)
	}
	if m.focused >= len(m.sessions) && m.focused > 0 {
		m.focused = len(m.sessions) - 1
	}
	m.updateTerminalRepliesLocked()
	m.mu.Unlock()
	_ = killed.Close()
}

// Remove stops and removes a session, including the final session. Automation
// callers use this explicit operation; interactive UI safeguards remain in Kill.
func (m *SessionManager) Remove(index int) bool {
	m.mu.Lock()
	if index < 0 || index >= len(m.sessions) {
		m.mu.Unlock()
		return false
	}
	removed := m.sessions[index]
	m.sessions = append(m.sessions[:index], m.sessions[index+1:]...)
	for i, s := range m.sessions {
		s.setIndex(i)
	}
	if len(m.sessions) == 0 {
		m.focused = 0
	} else if m.focused >= len(m.sessions) {
		m.focused = len(m.sessions) - 1
	}
	m.updateTerminalRepliesLocked()
	m.mu.Unlock()
	_ = removed.Close()
	return true
}

// Detach removes a session from this manager without stopping its PTY.
func (m *SessionManager) Detach(index int) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.sessions) {
		return nil
	}
	detached := m.sessions[index]
	m.sessions = append(m.sessions[:index], m.sessions[index+1:]...)
	for i, s := range m.sessions {
		s.setIndex(i)
	}
	if len(m.sessions) == 0 {
		m.focused = 0
	} else if m.focused >= len(m.sessions) {
		m.focused = len(m.sessions) - 1
	}
	m.updateTerminalRepliesLocked()
	return detached
}

// Adopt inserts a running detached session into this manager.
func (m *SessionManager) Adopt(s *Session, position int) {
	if s == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if position < 0 || position > len(m.sessions) {
		position = len(m.sessions)
	}
	s.SendOutput = m.SendOutput
	s.SendExit = m.SendExit
	m.sessions = append(m.sessions, nil)
	copy(m.sessions[position+1:], m.sessions[position:])
	m.sessions[position] = s
	for i, existing := range m.sessions {
		existing.setIndex(i)
	}
	m.updateTerminalRepliesLocked()
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
		if s.Index() == id {
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

// CloseAll stops every session and clears the manager state.
func (m *SessionManager) CloseAll() error {
	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.sessions = nil
	m.focused = 0
	m.updateTerminalRepliesLocked()
	m.mu.Unlock()

	var errs []error
	for _, s := range sessions {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ByID returns the session with the given index (or nil).
func (m *SessionManager) ByID(id int) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.Index() == id {
			return s
		}
	}
	return nil
}

// Move relocates the session currently at index `from` to position `to` in
// the ordered session list. All sessions in the affected range are
// reindexed, and the focused index is updated so it still points at the
// same session it pointed to before the move.
func (m *SessionManager) Move(from, to int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.sessions)
	if n <= 1 || from < 0 || from >= n {
		return
	}
	if to < 0 {
		to = 0
	}
	if to >= n {
		to = n - 1
	}
	if from == to {
		return
	}
	focusedSess := m.sessions[m.focused]
	s := m.sessions[from]
	m.sessions = append(m.sessions[:from], m.sessions[from+1:]...)
	m.sessions = append(m.sessions[:to], append([]*Session{s}, m.sessions[to:]...)...)
	for i, sess := range m.sessions {
		sess.setIndex(i)
		if sess == focusedSess {
			m.focused = i
		}
	}
	m.updateTerminalRepliesLocked()
}
