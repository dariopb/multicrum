package ui

import (
	"fmt"
	"strings"
)

func (s *state) renderConnectionPills() string {
	if len(s.connections) == 0 {
		return connActiveStyle.Render("default")
	}
	parts := make([]string, 0, len(s.connections))
	for i, conn := range s.connections {
		label := fmt.Sprintf("[%d] %s", i+1, conn.name)
		if i == s.activeConn {
			parts = append(parts, connActiveStyle.Render(label))
		} else {
			parts = append(parts, connInactiveStyle.Render(label))
		}
	}
	return strings.Join(parts, "")
}
