package transport

import (
	"strings"
	"testing"
)

func TestAgentUIIncludesStatePresentation(t *testing.T) {
	html := indexHTML("")
	for _, fragment := range []string{
		".agent-state.agent-working{color:#facc15}",
		".agent-state.agent-blocked{color:#f9a8d4}",
		".agent-state.agent-idle{color:#86efac}",
		".agent-provider{opacity:.58}",
		"rectangle: ['⠋','⠙','⠹','⠸','⠼','⠴','⠦','⠧','⠇','⠏']",
		"circle: ['●','◉','◎','○']",
		`data-animate="`,
		`data-spinner="`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("agent UI missing %q", fragment)
		}
	}
}
