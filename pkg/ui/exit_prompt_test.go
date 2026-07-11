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
