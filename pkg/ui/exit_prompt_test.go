package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"multicrum/pkg/session"
)

func TestRespawnFailureKeepsVerticalExitPrompt(t *testing.T) {
	m, _ := mouseTestModel(t, 1)
	m.SetConnectionLayout("left")
	m.s.mode = modeExitPrompt
	m.s.exitPromptID = 99
	m.s.exitChoice = 0

	if cmd := m.s.resolveExitPrompt(*m); cmd != nil {
		t.Fatalf("respawn failure command = %v, want nil", cmd)
	}
	if m.s.mode != modeExitPrompt {
		t.Fatalf("mode = %v, want exit prompt", m.s.mode)
	}
	if !strings.Contains(m.s.exitError, "respawn failed") {
		t.Fatalf("exit error = %q", m.s.exitError)
	}
	frame := ansi.Strip(m.viewString())
	if !strings.Contains(frame, "Multicrum") ||
		!strings.Contains(frame, "Session exited") ||
		!strings.Contains(frame, "respawn failed") {
		t.Fatalf("vertical frame did not retain rail and exit prompt:\n%s", frame)
	}
}

func TestVerticalExitUsesRendererAwareClear(t *testing.T) {
	m, _ := mouseTestModel(t, 1)
	m.SetConnectionLayout("left")

	_, cmd := m.Update(connectionExitMsg{
		Conn: m.s.connections[0],
		Msg:  session.ExitMsg{Index: 0},
	})
	if cmd == nil {
		t.Fatal("vertical exit did not request a clear-screen redraw")
	}

	if _, raw := cmd().(tea.RawMsg); raw {
		t.Fatal("vertical exit used raw erase, which leaves the renderer cache stale")
	}
	if m.s.mode != modeExitPrompt {
		t.Fatalf("mode = %v, want exit prompt", m.s.mode)
	}
}

func TestControlledExitDoesNotOpenPrompt(t *testing.T) {
	m, _ := mouseTestModel(t, 1)
	m.SetConnectionLayout("left")
	sess := m.s.manager.Focused()
	sessionID, generation, _, _ := sess.RuntimeSnapshot()
	m.s.suppressExit(sessionID, generation)

	_, cmd := m.Update(connectionExitMsg{
		Conn: m.s.connections[0],
		Msg: session.ExitMsg{
			Index: 0, SessionID: sessionID, Generation: generation,
		},
	})
	if cmd != nil {
		t.Fatalf("controlled exit command = %v, want nil", cmd)
	}
	if m.s.mode != modeNormal {
		t.Fatalf("mode = %v, want normal", m.s.mode)
	}
}

func TestRemovedSessionExitIsIgnoredAfterReindex(t *testing.T) {
	m, _ := mouseTestModel(t, 2)
	removed := m.s.manager.Sessions()[0]
	sessionID, generation, _, _ := removed.RuntimeSnapshot()
	m.s.manager.Remove(0)

	_, _ = m.Update(connectionExitMsg{
		Conn: m.s.connections[0],
		Msg: session.ExitMsg{
			Index: 0, SessionID: sessionID, Generation: generation,
		},
	})
	if m.s.mode != modeNormal {
		t.Fatalf("stale exit opened mode %v", m.s.mode)
	}
}

func TestEscapeDoesNotDismissExitPrompt(t *testing.T) {
	m, _ := mouseTestModel(t, 1)
	m.s.mode = modeExitPrompt
	m.s.exitError = "respawn failed"

	if cmd := m.s.handleExitPromptKey(*m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})); cmd != nil {
		t.Fatalf("Escape command = %v, want nil", cmd)
	}
	if m.s.mode != modeExitPrompt || m.s.exitError != "respawn failed" {
		t.Fatalf("Escape dismissed prompt: mode=%v error=%q", m.s.mode, m.s.exitError)
	}
}

func TestExitPromptAllowsMouseSessionSwitch(t *testing.T) {
	m, _ := mouseTestModel(t, 2)
	m.SetConnectionLayout("left")
	m.s.manager.Focus(0)
	m.s.mode = modeExitPrompt
	m.s.exitPromptID = 0
	_ = m.renderTabBar()
	target := m.s.sessionHitboxes[1]

	_, _ = m.Update(tea.MouseClickMsg(tea.Mouse{
		X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseLeft,
	}))
	_, _ = m.Update(tea.MouseReleaseMsg(tea.Mouse{
		X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseLeft,
	}))

	if m.s.manager.FocusedIndex() != 1 || m.s.mode != modeNormal {
		t.Fatalf("mouse session switch focused=%d mode=%v", m.s.manager.FocusedIndex(), m.s.mode)
	}
}

func TestExitPromptAllowsMouseConnectionSwitch(t *testing.T) {
	m, _ := mouseTestModel(t, 1)
	second := m.s.addConnection("work")
	second.manager = contextMenuTestManager(t, 64, 23, 1)
	defer second.manager.CloseAll()
	m.s.activeConn = 0
	m.s.syncActiveConnectionFields()
	m.SetConnectionLayout("left")
	m.s.mode = modeExitPrompt
	m.s.exitPromptID = 0
	_ = m.renderConnectionRail(m.s.geometry())
	target := m.s.connectionHitboxes[1]

	_, _ = m.Update(tea.MouseClickMsg(tea.Mouse{
		X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseLeft,
	}))
	_, _ = m.Update(tea.MouseReleaseMsg(tea.Mouse{
		X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseLeft,
	}))

	if m.s.activeConn != 1 || m.s.mode != modeNormal {
		t.Fatalf("mouse connection switch active=%d mode=%v", m.s.activeConn, m.s.mode)
	}
}
