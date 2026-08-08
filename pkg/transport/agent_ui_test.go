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
		`if(provider==='crush') return 'Crush';`,
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
		`--accent-pink:#ff00af`,
		`background:var(--accent-pink);border-bottom`,
		`45%,var(--accent-pink))`,
		`font-family:var(--font);font-size:var(--font-size-base)`,
		`.sess-title{flex:1;font-size:1em`,
		`#modal-footer{margin-top:12px;font-size:.79em`,
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

func TestWebUIIncludesBrowserAndApplicationSettingsTabs(t *testing.T) {
	html := indexHTML("")
	for _, fragment := range []string{
		`id="settings-tab-browser"`,
		`id="settings-tab-app"`,
		`id="settings-browser-panel"`,
		`id="settings-app-panel"`,
		`id="set-app-spinner-style"`,
		`id="set-app-spinner-animation"`,
		`id="set-app-copy-on-release"`,
		`control({action:'setting',setting,value:String(value)})`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("web settings missing %q", fragment)
		}
	}
}

func TestWebUICopiesSelectionOnRelease(t *testing.T) {
	html := indexHTML("")
	for _, fragment := range []string{
		`function copyTerminalSelectionOnRelease(e)`,
		`serverSettings.copyOnRelease === false`,
		`const text = term.getSelection()`,
		`term.clearSelection()`,
		`navigator.clipboard.writeText(text)`,
		`window.addEventListener('mouseup', copyTerminalSelectionOnRelease, {capture:true})`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("web copy-on-release missing %q", fragment)
		}
	}
}

func TestWebUIIncludesForceResizeAction(t *testing.T) {
	html := indexHTML("")
	for _, fragment := range []string{
		`id="m-force-resize"`,
		`Ctrl-Alt-Z`,
		`document.getElementById('m-force-resize').onclick`,
		`onlyCtrlAlt && (e.code==='KeyZ'`,
		`fitAndResize(); term.focus()`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("web force resize missing %q", fragment)
		}
	}
}
