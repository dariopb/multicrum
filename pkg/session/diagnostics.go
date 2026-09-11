package session

import "multicrum/pkg/diagnostics"

// SetDiagnostics enables metadata-only tracing for current and future sessions.
func (m *SessionManager) SetDiagnostics(trace *diagnostics.Recorder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trace = trace
	for _, s := range m.sessions {
		s.setDiagnostics(trace)
	}
}

func (s *Session) setDiagnostics(trace *diagnostics.Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trace = trace
	s.screen.setDiagnostics(trace, s.runtimeID, s.generation)
}
