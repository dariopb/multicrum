package ui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/session"
)

func contextMenuTestManager(t *testing.T, cols, rows, sessions int) *session.SessionManager {
	t.Helper()
	manager := session.NewManager(cols, rows, nil, nil)
	for i := 0; i < sessions; i++ {
		if _, err := manager.New([]string{"sh"}); err != nil {
			manager.CloseAll()
			t.Fatalf("new session %d: %v", i, err)
		}
	}
	return manager
}

func contextMenuOptionClick(m *Model) mouseEvent {
	left, top, _, _ := m.contextMenuBounds()
	return mouseEvent{
		X:      left,
		Y:      1 + top + 1, // whole-window row: pane origin + top border
		Button: tea.MouseLeft,
		Action: mousePress,
	}
}

func TestSessionTabContextMenuDispatchesFocusKey(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := contextMenuTestManager(t, 80, 22, 2)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager

	_ = m.renderTabBar()
	target := m.s.sessionHitboxes[1]
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: target.Start, Y: 0, Button: tea.MouseRight, Action: mousePress,
	}); !handled {
		t.Fatal("right-click on session tab was not handled")
	}
	if m.s.mode != modeContextMenu || m.s.contextMenu.kind != sessionContextMenu ||
		m.s.contextMenu.target != 1 {
		t.Fatalf("session context menu = %#v, mode = %v", m.s.contextMenu, m.s.mode)
	}

	// The menu needs all-motion reporting while open, then should highlight
	// precisely the option currently under the mouse.
	if got := m.View().MouseMode; got != tea.MouseModeAllMotion {
		t.Fatalf("context menu MouseMode = %v, want AllMotion", got)
	}
	left, top, _, _ := m.contextMenuBounds()
	_ = m.s.handleContextMenuMouse(*m, mouseEvent{
		X: left, Y: 1 + top + 1 + 2, Button: tea.MouseNone, Action: mouseMotion,
	})
	if m.s.contextMenu.hover != 2 {
		t.Fatalf("hover = %d, want Move option 2", m.s.contextMenu.hover)
	}
	if !strings.Contains(m.contextMenuLabels()[2], "\x1b[") {
		t.Fatalf("hovered menu item is not modal-styled: %q", m.contextMenuLabels()[2])
	}

	// A right-click on another tab replaces the open menu instead of merely
	// dismissing it.
	first := m.s.sessionHitboxes[0]
	_ = m.s.handleContextMenuMouse(*m, mouseEvent{
		X: first.Start, Y: 0, Button: tea.MouseRight, Action: mousePress,
	})
	if m.s.mode != modeContextMenu || m.s.contextMenu.target != 0 {
		t.Fatalf("replacement menu = %#v, mode = %v", m.s.contextMenu, m.s.mode)
	}

	_ = m.s.handleContextMenuMouse(*m, contextMenuOptionClick(m))
	if m.s.mode != modeNormal || manager.FocusedIndex() != 0 {
		t.Fatalf("focus menu action left mode=%v focused=%d, want normal/0", m.s.mode, manager.FocusedIndex())
	}
}

func TestConnectionTabContextMenuDispatchesFocusKey(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	first := contextMenuTestManager(t, 80, 22, 1)
	second := contextMenuTestManager(t, 80, 22, 1)
	defer first.CloseAll()
	defer second.CloseAll()

	m.s.connections = []*connectionState{
		{
			name:           "default",
			manager:        first,
			viewports:      make(map[int]*viewport.Model),
			altScreens:     make(map[int]bool),
			scrollbackMode: make(map[int]bool),
		},
		{
			name:           "work",
			manager:        second,
			viewports:      make(map[int]*viewport.Model),
			altScreens:     make(map[int]bool),
			scrollbackMode: make(map[int]bool),
		},
	}
	m.s.activeConn = 0
	m.s.syncActiveConnectionFields()

	_ = m.renderStatusBar()
	_, paneRows := paneSize(m.s.width, m.s.height)
	target := m.s.connectionHitboxes[1]
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: target.Start, Y: paneRows + 1, Button: tea.MouseRight, Action: mousePress,
	}); !handled {
		t.Fatal("right-click on connection tab was not handled")
	}
	if m.s.mode != modeContextMenu || m.s.contextMenu.kind != connectionContextMenu ||
		m.s.contextMenu.target != 1 {
		t.Fatalf("connection context menu = %#v, mode = %v", m.s.contextMenu, m.s.mode)
	}

	_ = m.s.handleContextMenuMouse(*m, contextMenuOptionClick(m))
	if m.s.mode != modeNormal || m.s.activeConn != 1 {
		t.Fatalf("focus menu action left mode=%v active=%d, want normal/1", m.s.mode, m.s.activeConn)
	}
}
