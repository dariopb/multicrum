package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func (s *state) renderConnectionPills() string {
	geom := s.geometry()
	if len(s.connections) == 0 {
		pill := connActiveStyle.Render("default")
		s.connectionHitboxes = append(s.connectionHitboxes, mouseHitbox{Bounds: rect{X: geom.StatusBar.X, Y: geom.StatusBar.Y, Width: lipgloss.Width(pill), Height: 1}, Index: 0, Action: hitboxConnection})
		return pill
	}
	parts := make([]string, 0, len(s.connections))
	x := 0
	for i, conn := range s.connections {
		label := fmt.Sprintf("[%d] %s", i+1, conn.name)
		pill := connInactiveStyle.Render(label)
		if i == s.activeConn {
			pill = connActiveStyle.Render(label)
		}
		parts = append(parts, pill)
		s.connectionHitboxes = append(s.connectionHitboxes, mouseHitbox{Bounds: rect{X: geom.StatusBar.X + x, Y: geom.StatusBar.Y, Width: lipgloss.Width(pill), Height: 1}, Index: i, Action: hitboxConnection})
		x += lipgloss.Width(pill)
	}
	return strings.Join(parts, "")
}

// renderConnectionRail renders fixed two-row connection controls in the left
// layout. It intentionally records only visible controls, so clipped entries
// cannot receive stale mouse events.
func (m Model) renderConnectionRail(geom layoutGeometry) []string {
	s := m.s
	rows := make([]string, geom.ConnectionRail.Height)
	blank := railInactiveStyle.Render(strings.Repeat(" ", geom.ConnectionRail.Width))
	for i := range rows {
		rows[i] = blank
	}
	if len(rows) == 0 {
		return rows
	}
	rows[0] = railBrandStyle.Render(padLine("Multicrum", geom.ConnectionRail.Width))
	s.appMenuHitbox = mouseHitbox{
		Bounds: rect{X: geom.ConnectionRail.X, Y: geom.ConnectionRail.Y, Width: lipgloss.Width("Multicrum"), Height: 1},
		Action: hitboxAppMenu,
	}
	s.hasAppMenuHitbox = true
	if len(rows) > 1 {
		rows[1] = railInactiveStyle.Render(padLine("server: "+s.serverName, geom.ConnectionRail.Width))
	}
	s.connectionHitboxes = nil
	s.hasNewConnectionHitbox = false
	s.hasHelpHitbox = false
	if len(rows) < 3 {
		return rows
	}
	footerY := len(rows) - 1
	newConnection := railInactiveStyle.Render("New")
	help := railInactiveStyle.Render("Alt+`")
	gap := geom.ConnectionRail.Width - lipgloss.Width(newConnection) - lipgloss.Width(help)
	if gap < 0 {
		gap = 0
	}
	rows[footerY] = newConnection + railInactiveStyle.Render(strings.Repeat(" ", gap)) + help
	s.newConnectionHitbox = mouseHitbox{
		Bounds: rect{X: geom.ConnectionRail.X, Y: footerY, Width: lipgloss.Width(newConnection), Height: 1},
		Action: hitboxNewConnection,
	}
	s.hasNewConnectionHitbox = true
	s.helpHitbox = mouseHitbox{
		Bounds: rect{X: geom.ConnectionRail.X + geom.ConnectionRail.Width - lipgloss.Width(help), Y: footerY, Width: lipgloss.Width(help), Height: 1},
		Action: hitboxHelp,
	}
	s.hasHelpHitbox = true
	capacity := (len(rows) - 4) / 2
	if capacity <= 0 {
		return rows
	}
	start, end := 0, len(s.connections)
	if end-start > capacity {
		start = s.activeConn - capacity/2
		if start < 0 {
			start = 0
		}
		if start+capacity > len(s.connections) {
			start = len(s.connections) - capacity
		}
		end = start + capacity
	}
	for display, index := 0, start; index < end; display, index = display+1, index+1 {
		y := 3 + display*2
		if y+1 >= footerY {
			break
		}
		conn := s.connections[index]
		sessionCount := 0
		if conn.manager != nil {
			sessionCount = conn.manager.Len()
		}
		style := railInactiveStyle
		if index == s.activeConn {
			style = railActiveStyle
		}
		label := truncate(fmt.Sprintf("[%d] %s", index+1, conn.name), geom.ConnectionRail.Width)
		count := truncate(fmt.Sprintf("    %d sessions", sessionCount), geom.ConnectionRail.Width)
		if index == start && start > 0 {
			label = "↑ " + truncate(label, geom.ConnectionRail.Width-2)
		}
		if index == end-1 && end < len(s.connections) {
			count = "↓ " + truncate(count, geom.ConnectionRail.Width-2)
		}
		rows[y] = style.Render(padLine(label, geom.ConnectionRail.Width))
		rows[y+1] = style.Render(padLine(count, geom.ConnectionRail.Width))
		s.connectionHitboxes = append(s.connectionHitboxes, mouseHitbox{
			Bounds: rect{X: geom.ConnectionRail.X, Y: y, Width: geom.ConnectionRail.Width, Height: 2},
			Index:  index, Action: hitboxConnection,
		})
	}
	return rows
}
