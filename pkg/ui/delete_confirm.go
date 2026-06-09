package ui

import tea "charm.land/bubbletea/v2"

func (s *state) handleDeleteConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Key().Code {
	case tea.KeyLeft, tea.KeyRight, tea.KeyTab:
		s.deleteChoice = 1 - s.deleteChoice
		return nil
	case tea.KeyEscape:
		s.mode = s.deleteReturn
		return nil
	case tea.KeyEnter:
		if s.deleteChoice == 0 {
			return s.confirmDelete()
		}
		s.mode = s.deleteReturn
		return nil
	}
	switch msg.String() {
	case "y", "Y":
		return s.confirmDelete()
	case "n", "N", "q", "Q":
		s.mode = s.deleteReturn
	}
	return nil
}

func (s *state) confirmDelete() tea.Cmd {
	switch s.deleteKind {
	case "session":
		if s.manager.Len() <= 1 {
			s.deleteKind = "lastSession"
			return s.confirmDelete()
		}
		delete(s.viewports, s.deleteIndex)
		s.manager.Kill(s.deleteIndex)
		s.refreshFocused()
		s.notifyMeta()
		matches := s.filteredSessions()
		if s.selectCursor >= len(matches) {
			s.selectCursor = len(matches) - 1
		}
		if s.selectCursor < 0 {
			s.selectCursor = 0
		}
		s.mode = s.deleteReturn
	case "connection":
		if len(s.connections) <= 1 {
			s.mode = s.deleteReturn
			return nil
		}
		s.removeConnection(s.deleteIndex)
		matches := s.filteredConnections()
		if s.connCursor >= len(matches) {
			s.connCursor = len(matches) - 1
		}
		if s.connCursor < 0 {
			s.connCursor = 0
		}
		s.mode = s.deleteReturn
	case "lastSession":
		if s.manager.Len() != 1 {
			s.mode = s.deleteReturn
			return nil
		}
		if len(s.connections) > 1 {
			s.removeConnection(s.activeConn)
			s.mode = modeNormal
			return nil
		}
		return s.shutdownAll()
	}
	return nil
}

func (m Model) renderDeleteConfirmModal() string {
	s := m.s
	yes := "[ Yes ]"
	no := "[ No ]"
	if s.deleteChoice == 0 {
		yes = exitChoiceActiveStyle.Render(yes)
		no = exitChoiceInactiveStyle.Render(no)
	} else {
		yes = exitChoiceInactiveStyle.Render(yes)
		no = exitChoiceActiveStyle.Render(no)
	}
	kind := s.deleteKind
	prompt := "Delete " + kind + "?"
	if kind == "lastSession" {
		if len(s.connections) > 1 {
			prompt = "Delete last session and connection?"
		} else {
			prompt = "Delete last session and quit multicrum?"
		}
	} else if kind == "" {
		prompt = "Delete item?"
	}
	rows := []string{
		prompt,
		"",
		"Name: " + truncate(s.deleteName, 54),
		"",
		yes + modalGapStyle.Render("   ") + no,
		"",
		"←/→ or Tab choose   Enter confirm   Esc cancel",
	}
	return padBox(rows, 64)
}
