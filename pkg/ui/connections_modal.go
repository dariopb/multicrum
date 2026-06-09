package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (s *state) openConnectionsModal() {
	s.mode = modeConnections
	s.connCursor = s.activeConn
	s.connRename = ""
	s.connRenameCursor = 0
	s.connRenaming = false
	s.connFiltering = false
	s.connMoving = false
}

func (s *state) handleConnectionsKey(m Model, msg tea.KeyPressMsg) tea.Cmd {
	key := msg.Key()
	if s.connRenaming {
		s.handleConnectionRenameKey(msg)
		return nil
	}
	if s.connFiltering {
		s.handleConnectionFilterKey(msg)
		return nil
	}
	switch key.Code {
	case tea.KeyEscape:
		if s.connMoving {
			s.connMoving = false
			return nil
		}
		s.mode = modeNormal
		return nil
	case tea.KeyUp:
		matches := s.filteredConnections()
		if s.connMoving {
			s.moveConnectionInFiltered(-1)
			return nil
		}
		if len(matches) > 0 {
			s.connCursor = (s.connCursor - 1 + len(matches)) % len(matches)
		}
		return nil
	case tea.KeyDown:
		matches := s.filteredConnections()
		if s.connMoving {
			s.moveConnectionInFiltered(1)
			return nil
		}
		if len(matches) > 0 {
			s.connCursor = (s.connCursor + 1) % len(matches)
		}
		return nil
	case tea.KeyEnter:
		matches := s.filteredConnections()
		if len(matches) > 0 {
			s.mode = modeNormal
			s.focusConnection(matches[s.connCursor])
		}
		return nil
	case tea.KeyDelete:
		s.confirmDeleteConnectionAtCursor()
		return nil
	}
	switch msg.String() {
	case "n", "N":
		s.quickAddConnection(m)
		s.connCursor = s.filteredCursorForIndex(s.activeConn)
		return nil
	case "r", "R":
		matches := s.filteredConnections()
		if len(matches) > 0 {
			s.connRenaming = true
			s.connRename = s.connections[matches[s.connCursor]].name
			s.connRenameCursor = len([]rune(s.connRename))
		}
		return nil
	case "f", "F":
		s.connFiltering = true
		s.connFilterCursor = len([]rune(s.connFilter))
		return nil
	case "m", "M":
		s.connMoving = !s.connMoving
		return nil
	case "x", "X", "d", "D":
		s.confirmDeleteConnectionAtCursor()
		return nil
	}
	return nil
}

func (s *state) quickAddConnection(m Model) {
	name := fmt.Sprintf("conn-%d", len(s.connections)+1)
	conn := s.createConnectionWithDefaultSession(name, m)
	for i, c := range s.connections {
		if c == conn {
			s.focusConnection(i)
			return
		}
	}
}

func (s *state) handleConnectionRenameKey(msg tea.KeyPressMsg) {
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case 'h':
			s.connRename, s.connRenameCursor = backspaceAt(s.connRename, s.connRenameCursor)
			return
		case 'u':
			s.connRename = ""
			s.connRenameCursor = 0
			return
		case 'a':
			s.connRenameCursor = 0
			return
		case 'e':
			s.connRenameCursor = len([]rune(s.connRename))
			return
		}
	}
	if key.Text != "" {
		s.connRename, s.connRenameCursor = insertAt(s.connRename, s.connRenameCursor, key.Text)
		return
	}
	switch key.Code {
	case tea.KeyEnter:
		matches := s.filteredConnections()
		if strings.TrimSpace(s.connRename) != "" && len(matches) > 0 {
			s.renameConnection(matches[s.connCursor], strings.TrimSpace(s.connRename))
		}
		s.connRenaming = false
		s.connRename = ""
		s.connRenameCursor = 0
	case tea.KeyEscape:
		s.connRenaming = false
		s.connRename = ""
		s.connRenameCursor = 0
	case tea.KeyBackspace:
		s.connRename, s.connRenameCursor = backspaceAt(s.connRename, s.connRenameCursor)
	case tea.KeyDelete:
		s.connRename, s.connRenameCursor = deleteAt(s.connRename, s.connRenameCursor)
	case tea.KeyLeft:
		if s.connRenameCursor > 0 {
			s.connRenameCursor--
		}
	case tea.KeyRight:
		if s.connRenameCursor < len([]rune(s.connRename)) {
			s.connRenameCursor++
		}
	case tea.KeyHome:
		s.connRenameCursor = 0
	case tea.KeyEnd:
		s.connRenameCursor = len([]rune(s.connRename))
	}
}

func (s *state) handleConnectionFilterKey(msg tea.KeyPressMsg) {
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case 'h':
			s.connFilter, s.connFilterCursor = backspaceAt(s.connFilter, s.connFilterCursor)
			s.connCursor = 0
			return
		case 'u':
			s.connFilter = ""
			s.connFilterCursor = 0
			s.connCursor = 0
			return
		case 'a':
			s.connFilterCursor = 0
			return
		case 'e':
			s.connFilterCursor = len([]rune(s.connFilter))
			return
		}
	}
	if key.Text != "" {
		s.connFilter, s.connFilterCursor = insertAt(s.connFilter, s.connFilterCursor, key.Text)
		s.connCursor = 0
		return
	}
	switch key.Code {
	case tea.KeyEnter:
		s.connFiltering = false
		s.connCursor = min(s.connCursor, max(0, len(s.filteredConnections())-1))
	case tea.KeyEscape:
		s.connFiltering = false
	case tea.KeyBackspace:
		s.connFilter, s.connFilterCursor = backspaceAt(s.connFilter, s.connFilterCursor)
		s.connCursor = 0
	case tea.KeyDelete:
		s.connFilter, s.connFilterCursor = deleteAt(s.connFilter, s.connFilterCursor)
		s.connCursor = 0
	case tea.KeyLeft:
		if s.connFilterCursor > 0 {
			s.connFilterCursor--
		}
	case tea.KeyRight:
		if s.connFilterCursor < len([]rune(s.connFilter)) {
			s.connFilterCursor++
		}
	case tea.KeyHome:
		s.connFilterCursor = 0
	case tea.KeyEnd:
		s.connFilterCursor = len([]rune(s.connFilter))
	}
}

func (s *state) filteredConnections() []int {
	query := strings.ToLower(strings.TrimSpace(s.connFilter))
	out := make([]int, 0, len(s.connections))
	for i, conn := range s.connections {
		label := fmt.Sprintf("%d %s", i+1, conn.name)
		if query == "" || strings.Contains(strings.ToLower(label), query) {
			out = append(out, i)
		}
	}
	return out
}

func (s *state) filteredCursorForIndex(index int) int {
	matches := s.filteredConnections()
	for i, idx := range matches {
		if idx == index {
			return i
		}
	}
	if len(matches) == 0 {
		return 0
	}
	return min(s.connCursor, len(matches)-1)
}

func (s *state) confirmDeleteConnectionAtCursor() {
	matches := s.filteredConnections()
	if len(matches) == 0 || len(s.connections) <= 1 {
		return
	}
	idx := matches[s.connCursor]
	s.deleteKind = "connection"
	s.deleteIndex = idx
	s.deleteName = s.connections[idx].name
	s.deleteReturn = modeConnections
	s.deleteChoice = 1
	s.mode = modeDeleteConfirm
}

func (s *state) removeConnectionAtCursor() {
	matches := s.filteredConnections()
	if len(matches) == 0 {
		return
	}
	s.removeConnection(matches[s.connCursor])
	matches = s.filteredConnections()
	if s.connCursor >= len(matches) {
		s.connCursor = len(matches) - 1
	}
	if s.connCursor < 0 {
		s.connCursor = 0
	}
}

func (s *state) moveConnectionInFiltered(delta int) {
	matches := s.filteredConnections()
	if len(matches) == 0 || s.connCursor < 0 || s.connCursor >= len(matches) {
		return
	}
	from := matches[s.connCursor]
	to := from + delta
	if to < 0 || to >= len(s.connections) {
		return
	}
	s.moveConnection(from, to)
	s.connCursor = s.filteredCursorForIndex(to)
}

func (m Model) renderConnectionsModal() string {
	s := m.s
	width := 64
	rows := []string{"Connections"}
	if strings.TrimSpace(s.connFilter) != "" || s.connFiltering {
		filter := renderWithCursor(s.connFilter, s.connFilterCursor)
		if !s.connFiltering {
			filter = s.connFilter
		}
		rows = append(rows, "", scrollIndicatorStyle.Render("Filter: "+truncate(filter, width-10)))
	}
	rows = append(rows, "")
	matches := s.filteredConnections()
	if s.connCursor >= len(matches) {
		s.connCursor = max(0, len(matches)-1)
	}
	for row, index := range matches {
		conn := s.connections[index]
		marker := "  "
		if row == s.connCursor {
			marker = "▶ "
		}
		active := ""
		if index == s.activeConn {
			active = " *"
		}
		label := fmt.Sprintf("%s%s (%d sessions)%s", marker, conn.name, conn.manager.Len(), active)
		if row == s.connCursor {
			if s.connMoving {
				label = selectorMovingStyle.Render(truncate(label, width-2))
			} else {
				label = selectorActiveStyle.Render(truncate(label, width-2))
			}
		}
		rows = append(rows, label)
	}
	if len(matches) == 0 {
		rows = append(rows, "  no matching connections")
	}
	if s.connRenaming {
		rows = append(rows, "", "Rename: "+renderWithCursor(s.connRename, s.connRenameCursor))
	}
	footer := "↑/↓ select   Enter focus   N new   R rename   M move   F filter   Del/X remove   Esc cancel"
	if s.connMoving {
		footer = "Move: ↑/↓ reorder   M/Esc stop moving"
	}
	if s.connFiltering {
		footer = "Filter: type pattern   Enter apply   Esc actions   Ctrl+U clear"
	}
	if s.connRenaming {
		footer = "Rename: Enter save   Esc cancel   Backspace edits"
	}
	rows = append(rows, "", footer)
	return padBox(rows, width)
}
