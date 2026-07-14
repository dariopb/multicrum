package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type contextMenuKind int

const (
	sessionContextMenu contextMenuKind = iota
	connectionContextMenu
	appContextMenu
)

type contextMenu struct {
	kind     contextMenuKind
	target   int
	pointerX int
	pointerY int
	hover    int
}

const appContextMenuDetachOption = 7

func (s *state) openContextMenu(kind contextMenuKind, target, pointerX, pointerY int) {
	s.contextMenu = contextMenu{
		kind:     kind,
		target:   target,
		pointerX: pointerX,
		pointerY: pointerY,
		hover:    -1,
	}
	s.mode = modeContextMenu
	s.clearSelection()
}

func (s *state) closeContextMenu() {
	s.contextMenu = contextMenu{}
	s.mode = modeNormal
}

func (m Model) contextMenuLabels() []string {
	labels := m.contextMenuOptions()
	width := 0
	for _, label := range labels {
		if w := lipgloss.Width(label); w > width {
			width = w
		}
	}
	if hover := m.s.contextMenu.hover; hover >= 0 && hover < len(labels) {
		// Pad before styling so the selection background fills the complete
		// content row inside the menu rather than only the label's glyphs.
		labels[hover] = selectorActiveStyle.Render(labels[hover] + strings.Repeat(" ", width-lipgloss.Width(labels[hover])))
	}
	return labels
}

func (m Model) contextMenuOptions() []string {
	if m.s.contextMenu.kind == appContextMenu {
		mouseMode := "select"
		if m.s.mouseCapture {
			mouseMode = "app"
		}
		return []string{
			"Help",
			"New Session",
			"Sessions",
			"New Connection",
			"Connections",
			fmt.Sprintf("Toggle Mouse (%s)", mouseMode),
			"Save Layout",
			"Detach",
			"Quit",
		}
	}
	return []string{"Focus", "Rename", "Move", "Remove"}
}

func (m Model) renderContextMenu() string {
	// Menus use the dialog's border/colors but omit vertical padding so the
	// first and last actions sit directly inside the top/bottom border.
	return padBoxWithStyle(m.contextMenuLabels(), 0, helpModalStyle.Padding(0, 1))
}

// contextMenuBounds uses screen coordinates so menus opened from the vertical
// rail stay at the pointer instead of being translated into the terminal pane.
func (m Model) contextMenuBounds() (left, top, width, height int) {
	geom := m.s.geometry()
	cols, rows := geom.Screen.Width, geom.Screen.Height
	box := m.renderContextMenu()
	width = lipgloss.Width(box)
	height = lipgloss.Height(box)
	left = m.s.contextMenu.pointerX
	top = m.s.contextMenu.pointerY
	if m.s.contextMenu.kind == sessionContextMenu {
		top = geom.TabBar.Y + geom.TabBar.Height
	} else if m.s.contextMenu.kind == appContextMenu {
		top = m.s.contextMenu.pointerY + 1
	} else if geom.ConnectionRail.Width == 0 {
		top = geom.StatusBar.Y - height
	}
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if width > cols {
		width = cols
		left = 0
	} else if left+width > cols {
		left = cols - width
	}
	if height > rows {
		height = rows
		top = 0
	} else if top+height > rows {
		top = rows - height
	}
	return
}

func (m Model) overlayContextMenu(frame string) string {
	left, top, _, _ := m.contextMenuBounds()
	geom := m.s.geometry()
	return overlayBoxAt(frame, m.renderContextMenu(), left, top, geom.Screen.Width, geom.Screen.Height)
}

// handleContextMenuMouse dispatches the clicked menu option by entering the
// corresponding existing keyboard-driven modal state and feeding it the same
// key the user could press there. It intentionally contains no duplicate
// session/connection action implementation.
func (s *state) handleContextMenuMouse(m Model, ev mouseEvent) tea.Cmd {
	if ev.Action == mouseMotion {
		s.contextMenu.hover = m.contextMenuOptionAt(ev)
		return nil
	}
	if ev.Action != mousePress {
		return nil
	}
	if ev.Button == tea.MouseRight {
		s.closeContextMenu()
		// Reuse the normal tab hit-testing path so a right-click on another
		// tab replaces this menu with one targeting the new tab.
		s.handleMouseScopeClick(m, ev)
		return nil
	}
	option := m.contextMenuOptionAt(ev)
	if ev.Button != tea.MouseLeft || option < 0 {
		s.closeContextMenu()
		return nil
	}
	menu := s.contextMenu
	s.contextMenu = contextMenu{}
	if menu.kind == appContextMenu {
		s.mode = modeNormal
		if option == appContextMenuDetachOption {
			if s.detachClient != nil {
				s.detachClient()
			}
			return nil
		}
		_, cmd := s.handleShortcut(m, appContextMenuKey(option))
		return cmd
	}
	if menu.kind == sessionContextMenu {
		s.openSessionSelector()
		s.selectCursor = s.filteredSessionCursorForIndex(menu.target)
		return s.handleSelectKey(m, contextMenuKey(option))
	}

	s.openConnectionsModal()
	s.connCursor = s.filteredCursorForIndex(menu.target)
	return s.handleConnectionsKey(m, contextMenuKey(option))
}

func (m Model) contextMenuOptionAt(ev mouseEvent) int {
	left, top, width, _ := m.contextMenuBounds()
	option := ev.Y - top - 1 // top border
	if ev.X < left || ev.X >= left+width || option < 0 ||
		option >= len(m.contextMenuOptions()) {
		return -1
	}
	return option
}

func appContextMenuKey(option int) tea.KeyPressMsg {
	ctrlAlt := tea.ModCtrl | tea.ModAlt
	switch option {
	case 0:
		return tea.KeyPressMsg(tea.Key{Code: '`', Mod: tea.ModAlt})
	case 1:
		return tea.KeyPressMsg(tea.Key{Code: 't', Mod: ctrlAlt})
	case 2:
		return tea.KeyPressMsg(tea.Key{Code: 's', Mod: ctrlAlt})
	case 3:
		return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: ctrlAlt})
	case 4:
		return tea.KeyPressMsg(tea.Key{Code: 'o', Mod: ctrlAlt})
	case 5:
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt})
	case 6:
		return tea.KeyPressMsg(tea.Key{Code: 'p', Mod: ctrlAlt})
	default:
		return tea.KeyPressMsg(tea.Key{Code: 'q', Mod: ctrlAlt})
	}
}

func contextMenuKey(option int) tea.KeyPressMsg {
	switch option {
	case 0:
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case 1:
		return tea.KeyPressMsg(tea.Key{Code: 'r', Text: "r"})
	case 2:
		return tea.KeyPressMsg(tea.Key{Code: 'm', Text: "m"})
	default:
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete})
	}
}
