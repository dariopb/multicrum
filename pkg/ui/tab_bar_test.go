package ui

import (
	"strings"
	"testing"

	"multicrum/pkg/session"
)

func TestFocusedExitedSessionKeepsActiveTabStyle(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := session.NewManager(80, 22, nil, nil)
	defer manager.CloseAll()
	first, err := manager.New([]string{"sh"})
	if err != nil {
		t.Fatalf("first session: %v", err)
	}
	second, err := manager.New([]string{"sh"})
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	first.SetTitle("one")
	second.SetTitle("two")
	m.s.manager = manager
	m.s.connections[0].manager = manager

	manager.Focus(1)
	if err := second.Close(); err != nil {
		t.Fatalf("close focused session: %v", err)
	}
	bar := m.renderTabBar()
	if want := tabActiveStyle.Render("[2] two ✗"); !strings.Contains(bar, want) {
		t.Fatalf("focused exited tab is not active-styled:\nbar  %q\nwant %q", bar, want)
	}

	manager.Focus(0)
	bar = m.renderTabBar()
	if want := tabExitedStyle.Render("[2] two ✗"); !strings.Contains(bar, want) {
		t.Fatalf("unfocused exited tab lost exited style:\nbar  %q\nwant %q", bar, want)
	}
}
