package ui

import "multicrum/pkg/diagnostics"

// SetDiagnostics enables metadata-only tracing. Call before Program.Run.
func (m *Model) SetDiagnostics(trace *diagnostics.Recorder) {
	m.s.trace = trace
	for _, conn := range m.s.connections {
		if conn.manager != nil {
			conn.manager.SetDiagnostics(trace)
		}
	}
}
