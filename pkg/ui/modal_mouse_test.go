package ui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"multicrum/pkg/session"
)

func mouseTestModel(t *testing.T, sessions int) (*Model, []*session.Session) {
	t.Helper()
	m := NewModel([]string{"sh"}, 100, 30)
	manager := session.NewManager(100, 28, nil, nil)
	var created []*session.Session
	for i := 0; i < sessions; i++ {
		sess, err := manager.New([]string{"sh"})
		if err != nil {
			manager.CloseAll()
			t.Fatalf("new session %d: %v", i, err)
		}
		sess.SetTitle("session-" + string(rune('a'+i)))
		created = append(created, sess)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	t.Cleanup(func() { _ = manager.CloseAll() })
	return m, created
}

func modalClick(m *Model, x, y int) mouseEvent {
	geometry := m.centeredModalGeometry(m.currentModalBox())
	return mouseEvent{
		X: geometry.Content.X + x, Y: geometry.Content.Y + y,
		Button: tea.MouseLeft, Action: mousePress,
	}
}

func clickModalLabel(t *testing.T, m *Model, row int, text, label string) tea.Cmd {
	t.Helper()
	x := strings.Index(text, label)
	if x < 0 {
		t.Fatalf("label %q not found in %q", label, text)
	}
	return m.s.handleModalMouse(*m, modalClick(m, x, row))
}

func TestCenteredModalButtonsDispatchKeyboardHandlers(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		m, _ := mouseTestModel(t, 1)
		m.s.height = 40
		m.s.mode = modeHelp
		geometry := m.centeredModalGeometry(m.currentModalBox())
		m.s.handleModalMouse(*m, modalClick(m, 0, geometry.Content.Height-1))
		if m.s.mode != modeNormal {
			t.Fatalf("help close click left mode %v", m.s.mode)
		}
	})

	t.Run("rename", func(t *testing.T) {
		m, _ := mouseTestModel(t, 1)
		m.s.mode = modeRenaming
		m.s.renameText = "changed"
		clickModalLabel(t, m, 5, "Enter save   Esc cancel", "Enter save")
		if got := m.s.manager.Focused().Title(); got != "changed" || m.s.mode != modeNormal {
			t.Fatalf("rename click title=%q mode=%v", got, m.s.mode)
		}
	})

	t.Run("exit", func(t *testing.T) {
		m, _ := mouseTestModel(t, 2)
		m.s.mode = modeExitPrompt
		m.s.exitPromptID = 0
		first := lipgloss.Width(exitChoiceActiveStyle.Render("[ Respawn ]"))
		m.s.handleModalMouse(*m, modalClick(m, first+3, 5))
		if m.s.manager.Len() != 1 || m.s.mode != modeNormal {
			t.Fatalf("remove click len=%d mode=%v", m.s.manager.Len(), m.s.mode)
		}
	})

	t.Run("new session", func(t *testing.T) {
		m, _ := mouseTestModel(t, 1)
		m.s.openNewSessionModal([]string{"sh"})
		first := lipgloss.Width(exitChoiceActiveStyle.Render("[ Same as current/default ]"))
		m.s.handleModalMouse(*m, modalClick(m, first+3, 2))
		if m.s.newSession.choice != 1 {
			t.Fatalf("local choice click = %d, want 1", m.s.newSession.choice)
		}
		geometry := m.centeredModalGeometry(m.currentModalBox())
		clickModalLabel(t, m, geometry.Content.Height-1,
			"Enter start   Esc cancel   ↑/↓ choose   Tab fields   1/2/3 choose", "Esc cancel")
		if m.s.mode != modeNormal {
			t.Fatalf("new-session cancel left mode %v", m.s.mode)
		}
	})

	t.Run("session selector", func(t *testing.T) {
		m, _ := mouseTestModel(t, 2)
		m.s.openSessionSelector()
		geometry := m.centeredModalGeometry(m.currentModalBox())
		footer := "↑/↓ select   Enter focus   N new   R rename   M move   F filter   Del/X remove   Esc cancel"
		clickModalLabel(t, m, geometry.Content.Height-1, footer, "F filter")
		if !m.s.selectFiltering {
			t.Fatal("session filter action was not dispatched")
		}
	})

	t.Run("connections selector", func(t *testing.T) {
		m, _ := mouseTestModel(t, 1)
		m.s.connections = append(m.s.connections, &connectionState{
			name: "work", manager: session.NewManager(100, 28, nil, nil),
			viewports: make(map[int]*viewport.Model), altScreens: make(map[int]bool), scrollbackMode: make(map[int]bool),
		})
		t.Cleanup(func() { _ = m.s.connections[1].manager.CloseAll() })
		m.s.openConnectionsModal()
		geometry := m.centeredModalGeometry(m.currentModalBox())
		footer := "↑/↓ select   Enter focus   N new   R rename   M move   L layout   F filter   Del/X remove   Esc cancel"
		clickModalLabel(t, m, geometry.Content.Height-1, footer, "L layout")
		if m.s.connectionLayout != connectionLayoutLeft {
			t.Fatalf("layout click = %q, want left", m.s.connectionLayout)
		}
	})

	t.Run("quit", func(t *testing.T) {
		m, _ := mouseTestModel(t, 1)
		m.s.mode = modeQuitConfirm
		m.s.exitChoice = 0
		yesWidth := lipgloss.Width(exitChoiceActiveStyle.Render("[ Yes ]"))
		cmd := m.s.handleModalMouse(*m, modalClick(m, yesWidth+3, 5))
		if cmd != nil || m.s.mode != modeNormal || m.s.quitting {
			t.Fatalf("quit No click cmd=%v mode=%v quitting=%v", cmd, m.s.mode, m.s.quitting)
		}
	})

	t.Run("delete", func(t *testing.T) {
		m, _ := mouseTestModel(t, 2)
		m.s.mode = modeDeleteConfirm
		m.s.deleteReturn = modeSelecting
		m.s.deleteChoice = 0
		yesWidth := lipgloss.Width(exitChoiceActiveStyle.Render("[ Yes ]"))
		m.s.handleModalMouse(*m, modalClick(m, yesWidth+3, 4))
		if m.s.mode != modeSelecting || m.s.manager.Len() != 2 {
			t.Fatalf("delete No click mode=%v len=%d", m.s.mode, m.s.manager.Len())
		}
	})
}

func TestModalOutsideClickDoesNotReachTabs(t *testing.T) {
	m, _ := mouseTestModel(t, 2)
	m.s.manager.Focus(1)
	_ = m.renderTabBar()
	target := m.s.sessionHitboxes[0]
	m.s.mode = modeHelp
	_, _ = m.Update(tea.MouseClickMsg(tea.Mouse{
		X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseLeft,
	}))
	if m.s.mode != modeHelp || m.s.manager.FocusedIndex() != 1 {
		t.Fatalf("outside modal click mode=%v focused=%d", m.s.mode, m.s.manager.FocusedIndex())
	}
}

func TestModalRowClickDoesNotRetargetRename(t *testing.T) {
	m, _ := mouseTestModel(t, 2)
	m.s.openSessionSelector()
	m.s.selectCursor = 0
	m.s.selectRenaming = true
	m.s.renameText = "renamed"

	row := modalClick(m, 0, m.s.sessionSelectorListStart()+1)
	m.s.handleModalMouse(*m, row)
	row.Action = mouseRelease
	m.s.handleModalMouse(*m, row)
	if m.s.selectCursor != 0 || !m.s.selectRenaming {
		t.Fatalf("rename target changed: cursor=%d renaming=%v", m.s.selectCursor, m.s.selectRenaming)
	}
}

func TestAbortedModalRowDragDoesNotActivate(t *testing.T) {
	m, _ := mouseTestModel(t, 2)
	m.s.openSessionSelector()
	row := modalClick(m, 0, m.s.sessionSelectorListStart())
	m.s.handleModalMouse(*m, row)
	row.Y--
	row.Action = mouseRelease
	m.s.handleModalMouse(*m, row)
	if m.s.mode != modeSelecting {
		t.Fatalf("release outside row activated it: mode=%v", m.s.mode)
	}
}

func TestMainMouseDragReordersSessionsAndConnections(t *testing.T) {
	t.Run("session tabs", func(t *testing.T) {
		m, sessions := mouseTestModel(t, 3)
		vps := []*viewport.Model{{}, {}, {}}
		for index, vp := range vps {
			m.s.viewports[index] = vp
		}
		_ = m.renderTabBar()
		first, last := m.s.sessionHitboxes[0], m.s.sessionHitboxes[2]
		m.s.handleMouseScopeClick(*m, mouseEvent{X: first.Bounds.X, Y: first.Bounds.Y, Button: tea.MouseLeft, Action: mousePress})
		m.s.handleMouseScopeClick(*m, mouseEvent{X: last.Bounds.X, Y: last.Bounds.Y, Button: tea.MouseLeft, Action: mouseMotion})
		m.s.handleMouseScopeClick(*m, mouseEvent{X: last.Bounds.X, Y: last.Bounds.Y, Button: tea.MouseLeft, Action: mouseRelease})
		got := m.s.manager.Sessions()
		if got[0] != sessions[1] || got[1] != sessions[2] || got[2] != sessions[0] {
			t.Fatalf("session order = %#v", got)
		}
		if m.s.viewports[0] != vps[1] || m.s.viewports[1] != vps[2] || m.s.viewports[2] != vps[0] {
			t.Fatal("session viewport indexes were not reordered with sessions")
		}

		_ = m.renderTabBar()
		target := m.s.sessionHitboxes[0]
		m.s.handleMouseScopeClick(*m, mouseEvent{X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseLeft, Action: mousePress})
		m.s.handleMouseScopeClick(*m, mouseEvent{X: target.Bounds.X, Y: target.Bounds.Y, Button: tea.MouseLeft, Action: mouseRelease})
		if m.s.manager.Focused() != sessions[1] {
			t.Fatal("simple session-tab click did not focus the clicked session")
		}
	})

	for _, layout := range []connectionLayout{connectionLayoutBottom, connectionLayoutLeft} {
		t.Run(string(layout), func(t *testing.T) {
			m, _ := mouseTestModel(t, 1)
			connections := []*connectionState{
				m.s.connections[0],
				{name: "two", manager: session.NewManager(100, 28, nil, nil), viewports: map[int]*viewport.Model{}, altScreens: map[int]bool{}, scrollbackMode: map[int]bool{}},
				{name: "three", manager: session.NewManager(100, 28, nil, nil), viewports: map[int]*viewport.Model{}, altScreens: map[int]bool{}, scrollbackMode: map[int]bool{}},
			}
			firstConn, secondConn, thirdConn := connections[0], connections[1], connections[2]
			m.s.connections = connections
			t.Cleanup(func() {
				_ = secondConn.manager.CloseAll()
				_ = thirdConn.manager.CloseAll()
			})
			m.s.connectionLayout = layout
			if layout == connectionLayoutLeft {
				_ = m.renderConnectionRail(m.s.geometry())
			} else {
				_ = m.renderStatusBar()
			}
			first, last := m.s.connectionHitboxes[0], m.s.connectionHitboxes[2]
			m.s.handleMouseScopeClick(*m, mouseEvent{X: first.Bounds.X, Y: first.Bounds.Y, Button: tea.MouseLeft, Action: mousePress})
			m.s.handleMouseScopeClick(*m, mouseEvent{X: last.Bounds.X, Y: last.Bounds.Y, Button: tea.MouseLeft, Action: mouseMotion})
			moved := m.s.mouseDrag.moved
			m.s.handleMouseScopeClick(*m, mouseEvent{X: last.Bounds.X, Y: last.Bounds.Y, Button: tea.MouseLeft, Action: mouseRelease})
			if m.s.connections[0] != secondConn || m.s.connections[1] != thirdConn || m.s.connections[2] != firstConn {
				t.Fatalf("%s connection order was not dragged (moved=%v indexes=%d,%d,%d)", layout, moved,
					connectionIndex(m.s.connections, firstConn), connectionIndex(m.s.connections, secondConn), connectionIndex(m.s.connections, thirdConn))
			}
			if m.s.activeConnection() != firstConn {
				t.Fatalf("%s active connection identity changed", layout)
			}
		})
	}
}

func TestSelectorMouseDragReordersRowsWithoutActivating(t *testing.T) {
	t.Run("sessions with headers and scroll", func(t *testing.T) {
		m, sessions := mouseTestModel(t, 8)
		m.s.height = 12
		m.s.openSessionSelector()
		m.s.selectFilter = "session"
		m.s.selectCursor = 4
		m.s.selectScroll = 2
		_ = m.currentModalBox()
		start := m.s.sessionSelectorListStart()
		first := modalClick(m, 0, start)
		last := modalClick(m, 0, start+2)
		m.s.handleModalMouse(*m, first)
		last.Action = mouseMotion
		m.s.handleModalMouse(*m, last)
		last.Action = mouseRelease
		m.s.handleModalMouse(*m, last)
		if m.s.mode != modeSelecting {
			t.Fatalf("session drag activated row and closed modal: %v", m.s.mode)
		}
		got := m.s.manager.Sessions()
		if got[4] != sessions[2] {
			t.Fatalf("dragged session identity ended at %d, want 4", sessionIndex(m.s.manager, sessions[2]))
		}
	})

	t.Run("connections with filter header", func(t *testing.T) {
		m, _ := mouseTestModel(t, 1)
		connections := []*connectionState{
			m.s.connections[0],
			{name: "conn-two", manager: session.NewManager(100, 28, nil, nil), viewports: map[int]*viewport.Model{}, altScreens: map[int]bool{}, scrollbackMode: map[int]bool{}},
			{name: "conn-three", manager: session.NewManager(100, 28, nil, nil), viewports: map[int]*viewport.Model{}, altScreens: map[int]bool{}, scrollbackMode: map[int]bool{}},
		}
		firstConn, secondConn, thirdConn := connections[0], connections[1], connections[2]
		firstConn.name = "conn-one"
		m.s.connections = connections
		t.Cleanup(func() {
			_ = secondConn.manager.CloseAll()
			_ = thirdConn.manager.CloseAll()
		})
		m.s.openConnectionsModal()
		m.s.connFilter = "conn"
		start := m.s.connectionsListStart()
		first := modalClick(m, 0, start)
		last := modalClick(m, 0, start+2)
		m.s.handleModalMouse(*m, first)
		last.Action = mouseMotion
		m.s.handleModalMouse(*m, last)
		moved := m.s.mouseDrag.moved
		last.Action = mouseRelease
		m.s.handleModalMouse(*m, last)
		if m.s.mode != modeConnections {
			t.Fatalf("connection drag activated row and closed modal: %v (moved=%v start=%d)", m.s.mode, moved, start)
		}
		if m.s.connections[2] != firstConn {
			t.Fatalf("dragged connection identity ended at %d, want 2", connectionIndex(m.s.connections, firstConn))
		}
	})
}

func TestSelectorRowClicksActivateOnRelease(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		m, sessions := mouseTestModel(t, 2)
		m.s.openSessionSelector()
		row := modalClick(m, 0, m.s.sessionSelectorListStart())
		m.s.handleModalMouse(*m, row)
		row.Action = mouseRelease
		m.s.handleModalMouse(*m, row)
		if m.s.mode != modeNormal || m.s.manager.Focused() != sessions[0] {
			t.Fatalf("session row click mode=%v focused=%d", m.s.mode, m.s.manager.FocusedIndex())
		}
	})

	t.Run("connection", func(t *testing.T) {
		m, _ := mouseTestModel(t, 1)
		second := &connectionState{
			name: "second", manager: session.NewManager(100, 28, nil, nil),
			viewports: map[int]*viewport.Model{}, altScreens: map[int]bool{}, scrollbackMode: map[int]bool{},
		}
		m.s.connections = append(m.s.connections, second)
		t.Cleanup(func() { _ = second.manager.CloseAll() })
		m.s.openConnectionsModal()
		row := modalClick(m, 0, m.s.connectionsListStart()+1)
		m.s.handleModalMouse(*m, row)
		row.Action = mouseRelease
		m.s.handleModalMouse(*m, row)
		if m.s.mode != modeNormal || m.s.activeConnection() != second {
			t.Fatalf("connection row click mode=%v active=%d", m.s.mode, m.s.activeConn)
		}
	})
}
