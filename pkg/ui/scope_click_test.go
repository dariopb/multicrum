package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/session"
)

func TestScopeClicksActivateHelpAndNewSession(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.s.manager = session.NewManager(80, 22, nil, nil)
	m.s.connections[0].manager = m.s.manager
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	_ = m.renderTabBar()
	if !m.s.hasNewSessionHitbox {
		t.Fatal("new-session hitbox was not recorded")
	}
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: m.s.newSessionHitbox.Start, Y: 0, Button: tea.MouseLeft, Action: mousePress,
	}); !handled || m.s.mode != modeNewSession {
		t.Fatalf("new-session click handled=%v mode=%v, want true/%v", handled, m.s.mode, modeNewSession)
	}

	m.s.mode = modeNormal
	_ = m.renderStatusBar()
	if !m.s.hasHelpHitbox {
		t.Fatal("help hitbox was not recorded")
	}
	_, paneRows := paneSize(m.s.width, m.s.height)
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: m.s.helpHitbox.Start, Y: paneRows + 1, Button: tea.MouseLeft, Action: mousePress,
	}); !handled || m.s.mode != modeHelp {
		t.Fatalf("help click handled=%v mode=%v, want true/%v", handled, m.s.mode, modeHelp)
	}

	m.s.mode = modeNormal
	_ = m.renderStatusBar()
	if !m.s.hasConnectionsHitbox {
		t.Fatal("connections hitbox was not recorded")
	}
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: m.s.connectionsHitbox.Start, Y: paneRows + 1, Button: tea.MouseLeft, Action: mousePress,
	}); !handled || m.s.mode != modeConnections {
		t.Fatalf("connections click handled=%v mode=%v, want true/%v", handled, m.s.mode, modeConnections)
	}
}
