package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// In select mode we must keep mouse reporting on (CellMotion) so the wheel
// and drag-select events reach the app; app mode uses AllMotion to forward
// everything to the child.
func TestViewMouseModeReflectsCaptureMode(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)

	m.s.mouseCapture = false
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("select-mode MouseMode = %v, want CellMotion", got)
	}

	m.s.mouseCapture = true
	if got := m.View().MouseMode; got != tea.MouseModeAllMotion {
		t.Fatalf("capture-mode MouseMode = %v, want AllMotion", got)
	}
}

// A wheel message must carry a wheel button so the select-mode handler routes
// it to scrollback movement rather than selection.
func TestMouseEventFromMsgWheel(t *testing.T) {
	up := mouseEventFromMsg(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if up.Button != tea.MouseWheelUp {
		t.Fatalf("wheel-up button = %v, want MouseWheelUp", up.Button)
	}
	down := mouseEventFromMsg(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if down.Button != tea.MouseWheelDown {
		t.Fatalf("wheel-down button = %v, want MouseWheelDown", down.Button)
	}
}
