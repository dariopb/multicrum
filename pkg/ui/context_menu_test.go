package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
		Y:      top + 1, // first row below the top border
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
		X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseRight, Action: mousePress,
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
		X: left, Y: top + 1 + 2, Button: tea.MouseNone, Action: mouseMotion,
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
		X: first.Bounds.X, Y: first.Bounds.Y, Button: tea.MouseRight, Action: mousePress,
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
	target := m.s.connectionHitboxes[1]
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseRight, Action: mousePress,
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

func TestVerticalConnectionContextMenuUsesScreenCoordinates(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := contextMenuTestManager(t, 64, 23, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager
	m.SetConnectionLayout("left")
	_ = m.renderConnectionRail(m.s.geometry())

	target := m.s.connectionHitboxes[0]
	x := target.Bounds.X + 2
	y := target.Bounds.Y + 1
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: x, Y: y, Button: tea.MouseRight, Action: mousePress,
	}); !handled {
		t.Fatal("right-click on vertical connection was not handled")
	}

	left, top, _, _ := m.contextMenuBounds()
	if left != x || top != y {
		t.Fatalf("vertical menu position = (%d,%d), want pointer (%d,%d)", left, top, x, y)
	}
	frame := m.viewString()
	rows := strings.Split(frame, "\n")
	if top >= len(rows) || lipgloss.Width(rows[top]) != m.s.geometry().Screen.Width {
		t.Fatal("full-screen context-menu overlay changed frame geometry")
	}
}

func TestVerticalBrandOpensGlobalActionsMenu(t *testing.T) {
	for _, tc := range []struct {
		name   string
		button tea.MouseButton
	}{
		{name: "left", button: tea.MouseLeft},
		{name: "right", button: tea.MouseRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel([]string{"bash"}, 80, 24)
			manager := contextMenuTestManager(t, 64, 23, 1)
			defer manager.CloseAll()
			m.s.manager = manager
			m.s.connections[0].manager = manager
			m.SetConnectionLayout("left")
			_ = m.renderConnectionRail(m.s.geometry())

			hit := m.s.appMenuHitbox.Bounds
			if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
				X: hit.X + 1, Y: hit.Y, Button: tc.button, Action: mousePress,
			}); !handled {
				t.Fatal("Multicrum click was not handled")
			}
			if m.s.mode != modeContextMenu || m.s.contextMenu.kind != appContextMenu {
				t.Fatalf("brand menu mode=%v menu=%#v", m.s.mode, m.s.contextMenu)
			}
			want := []string{
				"Help", "New Session", "Sessions", "New Connection",
				"Connections", "Toggle Mouse (select)", "Force Resize", "Save Layout", "Detach", "Settings", "Quit",
			}
			if got := m.contextMenuOptions(); !reflect.DeepEqual(got, want) {
				t.Fatalf("global menu options = %#v, want %#v", got, want)
			}

		})
	}
}

func TestGlobalActionsMenuShowsCurrentMouseMode(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.s.contextMenu.kind = appContextMenu
	if got := m.contextMenuOptions()[5]; got != "Toggle Mouse (select)" {
		t.Fatalf("select-mode label = %q", got)
	}
	m.s.mouseCapture = true
	if got := m.contextMenuOptions()[5]; got != "Toggle Mouse (app)" {
		t.Fatalf("app-mode label = %q", got)
	}
}

func TestGlobalActionsMenuDispatchesExistingShortcuts(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := contextMenuTestManager(t, 64, 23, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager
	m.SetConnectionLayout("left")

	m.s.openContextMenu(appContextMenu, -1, 1, 0)
	left, top, _, _ := m.contextMenuBounds()
	toggleMouse := mouseEvent{
		X: left, Y: top + 1 + 5, Button: tea.MouseLeft, Action: mousePress,
	}
	if cmd := m.s.handleContextMenuMouse(*m, toggleMouse); cmd != nil {
		t.Fatalf("toggle mouse command = %v, want nil", cmd)
	}
	if !m.s.mouseCapture || m.s.mode != modeNormal {
		t.Fatalf("toggle mouse left capture=%v mode=%v", m.s.mouseCapture, m.s.mode)
	}

	m.s.openContextMenu(appContextMenu, -1, 1, 0)
	left, top, _, _ = m.contextMenuBounds()
	detached := false
	m.SetDetachHandler(func() { detached = true })
	detach := mouseEvent{
		X: left, Y: top + 1 + appContextMenuDetachOption, Button: tea.MouseLeft, Action: mousePress,
	}
	if cmd := m.s.handleContextMenuMouse(*m, detach); cmd != nil {
		t.Fatalf("detach command = %v, want nil", cmd)
	}
	if !detached || m.s.mode != modeNormal {
		t.Fatalf("detach menu detached=%v mode=%v", detached, m.s.mode)
	}

	m.s.openContextMenu(appContextMenu, -1, 1, 0)
	left, top, _, _ = m.contextMenuBounds()
	settings := mouseEvent{
		X: left, Y: top + 1 + appContextMenuSettingsOption, Button: tea.MouseLeft, Action: mousePress,
	}
	if cmd := m.s.handleContextMenuMouse(*m, settings); cmd != nil {
		t.Fatalf("settings command = %v, want nil", cmd)
	}
	if m.s.mode != modeSettings {
		t.Fatalf("settings menu mode = %v, want settings", m.s.mode)
	}

	m.s.mode = modeNormal
	m.s.openContextMenu(appContextMenu, -1, 1, 0)
	left, top, _, _ = m.contextMenuBounds()
	quit := mouseEvent{
		X: left, Y: top + 1 + 10, Button: tea.MouseLeft, Action: mousePress,
	}
	_ = m.s.handleContextMenuMouse(*m, quit)
	if m.s.mode != modeQuitConfirm {
		t.Fatalf("quit menu mode = %v, want quit confirmation", m.s.mode)
	}
}

func TestGlobalActionsMenuUsesShortcutBindings(t *testing.T) {
	want := map[int]string{
		0:  shortcutHelp,
		1:  shortcutNew,
		2:  shortcutSessions,
		3:  shortcutNewConn,
		4:  shortcutConnections,
		5:  shortcutMouse,
		6:  shortcutForceResize,
		7:  shortcutSaveLayout,
		10: shortcutQuit,
	}
	for option, shortcut := range want {
		if got := appContextMenuKey(option).Keystroke(); got != shortcut {
			t.Errorf("option %d keystroke = %q, want %q", option, got, shortcut)
		}
	}
}

func TestForceResizeShortcutUsesCurrentPaneGeometry(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := contextMenuTestManager(t, 40, 10, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager

	handled, cmd := m.s.handleGlobalShortcut(tea.KeyPressMsg(tea.Key{
		Code: 'z',
		Mod:  tea.ModCtrl | tea.ModAlt,
	}))
	if !handled || cmd != nil {
		t.Fatalf("force resize handled=%v cmd=%v, want true/nil", handled, cmd)
	}
	geom := m.s.geometry()
	want := fmt.Sprintf("forced resize to %dx%d", geom.Pane.Width, geom.Pane.Height)
	if m.s.statusMsg != want {
		t.Fatalf("status = %q, want %q", m.s.statusMsg, want)
	}
}
