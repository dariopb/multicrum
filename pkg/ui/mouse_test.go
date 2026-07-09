package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/session"
)

// The startup input-mode reset must not disable the button-event (1002),
// any-event (1003) or SGR-extended (1006) mouse modes, because bubbletea's
// renderer enables those via View().MouseMode on the first flush. Resetting
// them here runs after that flush and leaves mouse reporting off until the
// user toggles mouse mode twice.
func TestResetTerminalInputModesKeepsMouseReporting(t *testing.T) {
	raw, ok := resetTerminalInputModes().(tea.RawMsg)
	if !ok {
		t.Fatalf("resetTerminalInputModes did not return a RawMsg")
	}
	seq, ok := raw.Msg.(string)
	if !ok {
		t.Fatalf("RawMsg payload = %T, want string", raw.Msg)
	}
	for _, banned := range []string{
		ansi.ResetModeMouseButtonEvent,
		ansi.ResetModeMouseAnyEvent,
		ansi.ResetModeMouseExtSgr,
	} {
		if strings.Contains(seq, banned) {
			t.Fatalf("startup reset must not disable renderer-owned mouse mode %q", banned)
		}
	}
}

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

// A newly attached client's terminal must receive the mouse-enable sequence
// matching the current capture mode so wheel/selection work without toggling.
func TestMouseEnableSequenceMatchesMode(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)

	m.s.mouseCapture = false
	seq := m.s.mouseEnableSequence()
	if !strings.Contains(seq, ansi.SetModeMouseButtonEvent) || !strings.Contains(seq, ansi.SetModeMouseExtSgr) {
		t.Fatalf("select-mode enable seq %q missing button-event/SGR enable", seq)
	}
	if strings.Contains(seq, ansi.SetModeMouseAnyEvent) {
		t.Fatalf("select-mode enable seq must not enable any-event: %q", seq)
	}

	m.s.mouseCapture = true
	seq = m.s.mouseEnableSequence()
	if !strings.Contains(seq, ansi.SetModeMouseAnyEvent) || !strings.Contains(seq, ansi.SetModeMouseExtSgr) {
		t.Fatalf("app-mode enable seq %q missing any-event/SGR enable", seq)
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

func TestHitboxAt(t *testing.T) {
	boxes := []mouseHitbox{
		{Start: 2, End: 5, Index: 10},
		{Start: 5, End: 9, Index: 11},
	}
	for _, tc := range []struct {
		x     int
		idx   int
		found bool
	}{
		{x: 1, found: false},
		{x: 2, idx: 10, found: true},
		{x: 4, idx: 10, found: true},
		{x: 5, idx: 11, found: true},
		{x: 8, idx: 11, found: true},
		{x: 9, found: false},
	} {
		idx, found := hitboxAt(boxes, tc.x)
		if found != tc.found || idx != tc.idx {
			t.Fatalf("hitboxAt(%d) = %d, %v; want %d, %v", tc.x, idx, found, tc.idx, tc.found)
		}
	}
}

func TestRenderBarsRecordMouseHitboxes(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.s.manager = session.NewManager(80, 22, nil, nil)
	if sess, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	} else {
		sess.SetTitle("one")
	}
	if sess, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	} else {
		sess.SetTitle("two")
	}
	defer m.s.manager.CloseAll()
	m.s.connections = []*connectionState{{name: "default"}, {name: "work"}}
	m.s.activeConn = 1

	_ = m.renderTabBar()
	if len(m.s.sessionHitboxes) != 2 {
		t.Fatalf("session hitboxes = %d, want 2", len(m.s.sessionHitboxes))
	}
	if m.s.sessionHitboxes[0].Index != 0 || m.s.sessionHitboxes[1].Index != 1 {
		t.Fatalf("session hitboxes = %#v, want indexes 0 and 1", m.s.sessionHitboxes)
	}

	_ = m.renderStatusBar()
	if len(m.s.connectionHitboxes) != 2 {
		t.Fatalf("connection hitboxes = %d, want 2", len(m.s.connectionHitboxes))
	}
	if m.s.connectionHitboxes[0].Index != 0 || m.s.connectionHitboxes[1].Index != 1 {
		t.Fatalf("connection hitboxes = %#v, want indexes 0 and 1", m.s.connectionHitboxes)
	}
	if m.s.connectionHitboxes[0].Start <= 0 {
		t.Fatalf("connection hitboxes were not offset by status prefix: %#v", m.s.connectionHitboxes)
	}
}
