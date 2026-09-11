package transport

import (
	"strings"
	"testing"
)

func TestWebTerminalDefaultsToSteadyCursor(t *testing.T) {
	html := indexHTML("")
	if !strings.Contains(html, "cursorBlink:false") {
		t.Fatal("web terminal does not default to a steady cursor")
	}
}

func TestCollapsedRailKeepsNeutralBackground(t *testing.T) {
	html := indexHTML("")
	const selector = "body.rail-collapsed #rail-header{"
	_, rest, ok := strings.Cut(html, selector)
	if !ok {
		t.Fatal("collapsed rail header rule is missing")
	}
	rule, _, ok := strings.Cut(rest, "}")
	if !ok || !strings.Contains(rule, "background:var(--row-hover)") {
		t.Fatal("collapsed rail must retain its neutral hover color when the pointer or focus leaves it")
	}
	if strings.Contains(rule, "--accent-pink") {
		t.Fatal("collapsed rail must not use the expanded header's pink background")
	}
}
