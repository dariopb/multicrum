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

func TestWebUIUsesResizableConnectionRailWithoutStatusBar(t *testing.T) {
	html := indexHTML("")
	for _, fragment := range []string{
		`id="connection-rail"`,
		`id="rail-connections"`,
		`id="rail-resizer"`,
		`id="rail-collapse"`,
		`function renderConnectionRail()`,
		`className = 'rail-connection'`,
		`className = 'rail-agent-row'`,
		`RAIL_WIDTH_KEY`,
		`RAIL_COLLAPSED_KEY`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("connection rail missing %q", fragment)
		}
	}

	for _, fragment := range []string{`id="statusbar"`, `id="status-main"`, `function updateStatusBar()`} {
		if strings.Contains(html, fragment) {
			t.Fatalf("obsolete status bar remains: %q", fragment)
		}
	}
}

func TestWebUIAppearanceDefaultsAndMobilePinch(t *testing.T) {
	html := indexHTML("")
	for _, fragment := range []string{
		`var d={theme:"dark"`,
		`const DEFAULT_SETTINGS = {theme:'dark'`,
		`#connection-rail{width:220px;min-width:150px;max-width:420px;display:flex;flex-direction:column;flex:0 0 auto;background:var(--terminal-bg)`,
		`--topbar-height:calc(var(--topbar-font-size) + 23px)`,
		`#rail-header{display:flex;align-items:stretch;height:var(--topbar-height);min-height:var(--topbar-height)`,
		`#tabbar{display:flex;align-items:stretch;height:var(--topbar-height);min-height:var(--topbar-height)`,
		`user-scalable=yes`,
		`touch-action:pan-x pan-y pinch-zoom`,
		`id="viewport-root"`,
		`const VIEWPORT_SCALE_KEY = 'multicrum-viewport-scale'`,
		`function moveViewportPinch(e)`,
		`viewportScale = Math.max(.5, Math.min(2`,
		`const viewport = window.visualViewport`,
		`root.style.height = (height / viewportScale)+'px'`,
		`window.visualViewport.addEventListener('resize', handleViewportResize)`,
		`addEventListener('touchmove', moveViewportPinch, {capture:true,passive:false})`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("web appearance or pinch support missing %q", fragment)
		}
	}
}
