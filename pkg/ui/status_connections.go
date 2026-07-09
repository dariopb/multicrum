package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func (s *state) renderConnectionPills() string {
	if len(s.connections) == 0 {
		pill := connActiveStyle.Render("default")
		s.connectionHitboxes = append(s.connectionHitboxes, mouseHitbox{Start: 0, End: lipgloss.Width(pill), Index: 0})
		return pill
	}
	parts := make([]string, 0, len(s.connections))
	x := 0
	for i, conn := range s.connections {
		label := fmt.Sprintf("[%d] %s", i+1, conn.name)
		var pill string
		if i == s.activeConn {
			pill = connActiveStyle.Render(label)
		} else {
			pill = connInactiveStyle.Render(label)
		}
		parts = append(parts, pill)
		s.connectionHitboxes = append(s.connectionHitboxes, mouseHitbox{Start: x, End: x + lipgloss.Width(pill), Index: i})
		x += lipgloss.Width(pill)
	}
	return strings.Join(parts, "")
}
