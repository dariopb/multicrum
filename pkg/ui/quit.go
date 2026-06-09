package ui

import tea "charm.land/bubbletea/v2"

func (s *state) handleQuitConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Key().Code {
	case tea.KeyLeft, tea.KeyRight, tea.KeyTab:
		s.exitChoice = 1 - s.exitChoice
		return nil
	case tea.KeyEscape:
		s.mode = modeNormal
		return nil
	case tea.KeyEnter:
		if s.exitChoice == 0 {
			return s.shutdownAll()
		}
		s.mode = modeNormal
		return nil
	}
	switch msg.String() {
	case "y", "Y":
		return s.shutdownAll()
	case "n", "N", "q", "Q":
		s.mode = modeNormal
	}
	return nil
}

func (s *state) shutdownAll() tea.Cmd {
	for _, conn := range s.connections {
		if conn.manager == nil {
			continue
		}
		for _, sess := range conn.manager.Sessions() {
			_ = sess.Close()
		}
	}
	return tea.Quit
}

func (m Model) renderQuitConfirmModal() string {
	s := m.s
	yes := "[ Yes ]"
	no := "[ No ]"
	if s.exitChoice == 0 {
		yes = exitChoiceActiveStyle.Render(yes)
		no = exitChoiceInactiveStyle.Render(no)
	} else {
		yes = exitChoiceInactiveStyle.Render(yes)
		no = exitChoiceActiveStyle.Render(no)
	}
	rows := []string{
		"Quit multicrum server?",
		"",
		"This will close all connections and sessions owned by this server.",
		"Attached clients will disconnect.",
		"",
		yes + modalGapStyle.Render("   ") + no,
	}
	return padBox(rows, 66)
}
