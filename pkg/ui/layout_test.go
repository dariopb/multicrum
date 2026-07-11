package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"multicrum/pkg/config"
)

func TestLayoutGeometry(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	bottom := m.s.geometry()
	if bottom.ConnectionRail.Width != 0 || bottom.TabBar != (rect{Width: 80, Height: 1}) ||
		bottom.Pane != (rect{X: 0, Y: 1, Width: 80, Height: 22}) ||
		bottom.StatusBar != (rect{X: 0, Y: 23, Width: 80, Height: 1}) {
		t.Fatalf("bottom geometry = %#v", bottom)
	}

	m.SetConnectionLayout("left")
	left := m.s.geometry()
	if left.ConnectionRail != (rect{Width: 15, Height: 24}) ||
		left.ConnectionDivider != (rect{X: 15, Width: 1, Height: 24}) ||
		left.TabBar != (rect{X: 16, Width: 64, Height: 1}) ||
		left.Pane != (rect{X: 16, Y: 1, Width: 64, Height: 23}) ||
		left.StatusBar != (rect{}) {
		t.Fatalf("left geometry = %#v", left)
	}
	if x, y, ok := left.ScreenToPane(17, 4); !ok || x != 1 || y != 3 {
		t.Fatalf("ScreenToPane = (%d, %d, %v), want (1, 3, true)", x, y, ok)
	}
	if x, y := left.PaneToScreen(1, 3); x != 17 || y != 4 {
		t.Fatalf("PaneToScreen = (%d, %d), want (17, 4)", x, y)
	}

	m.s.width = 25
	narrow := m.s.geometry()
	if narrow.ConnectionRail.Width != 14 || narrow.Pane.Width != 10 || m.s.connectionLayout != connectionLayoutLeft {
		t.Fatalf("narrow fallback = %#v, configured layout = %q", narrow, m.s.connectionLayout)
	}
	m.s.width = 18
	tooNarrow := m.s.geometry()
	if tooNarrow.ConnectionRail.Width != 0 || tooNarrow.Pane.Width != 18 {
		t.Fatalf("too-narrow fallback = %#v", tooNarrow)
	}
}

func TestLeftRailRenderingAndHitboxes(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 10)
	m.SetConnectionLayout("left")
	m.s.addConnection("work")
	m.s.activeConn = 1
	geom := m.s.geometry()
	rows := m.renderConnectionRail(geom)
	if got := ansi.Strip(rows[0]); got != "Multicrum      " {
		t.Fatalf("rail title = %q", got)
	}
	if got := ansi.Strip(rows[1]); got != "server: default" {
		t.Fatalf("rail server = %q", got)
	}
	if got := ansi.Strip(rows[2]); got != strings.Repeat(" ", connectionRailWidth) {
		t.Fatalf("rail blank row = %q", got)
	}
	footer := rows[len(rows)-1]
	if got := ansi.Strip(footer); got != "New       Alt+`" {
		t.Fatalf("rail footer = %q", got)
	}
	if strings.Contains(footer, "\x1b[48;5;236m") {
		t.Fatal("rail footer actions must not use the status-bar background")
	}
	if got := ansi.Strip(rows[3]); got != "[1] default    " {
		t.Fatalf("first rail entry = %q", got)
	}
	if got := ansi.Strip(rows[5]); got != "[2] work       " {
		t.Fatalf("active rail entry = %q", got)
	}
	if !strings.Contains(rows[5], "\x1b[") || !strings.Contains(rows[6], "\x1b[") {
		t.Fatal("active rail style does not cover both rows")
	}
	if len(m.s.connectionHitboxes) != 2 {
		t.Fatalf("rail hitboxes = %#v", m.s.connectionHitboxes)
	}
	box := m.s.connectionHitboxes[1]
	if box.Bounds != (rect{X: 0, Y: 5, Width: 15, Height: 2}) {
		t.Fatalf("active rail hitbox = %#v", box)
	}
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{X: 1, Y: 6, Button: tea.MouseRight, Action: mousePress}); !handled ||
		m.s.mode != modeContextMenu || m.s.contextMenu.target != 1 {
		t.Fatalf("rail right click mode=%v menu=%#v", m.s.mode, m.s.contextMenu)
	}
}

func TestLeftLayoutConnectionNavigation(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	first := contextMenuTestManager(t, 80, 22, 1)
	second := contextMenuTestManager(t, 80, 22, 1)
	defer first.CloseAll()
	defer second.CloseAll()
	m.s.connections[0].manager = first
	m.SetConnectionLayout("left")
	m.s.addConnection("work").manager = second
	m.s.activeConn = 0
	m.s.syncActiveConnectionFields()

	handled, _ := m.s.handleGlobalShortcut(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown, Mod: tea.ModCtrl | tea.ModAlt}))
	if !handled || m.s.activeConn != 1 {
		t.Fatalf("Ctrl+Alt+Down handled=%v active connection=%d, want true and 1", handled, m.s.activeConn)
	}
	handled, _ = m.s.handleGlobalShortcut(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModCtrl | tea.ModAlt}))
	if !handled || m.s.activeConn != 0 {
		t.Fatalf("Ctrl+Alt+Up handled=%v active connection=%d, want true and 0", handled, m.s.activeConn)
	}
}

func TestLeftLayoutComposesContinuousDivider(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := contextMenuTestManager(t, 80, 22, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager
	m.SetConnectionLayout("left")
	m.s.applyGeometry()

	rows := strings.Split(ansi.Strip(m.viewString()), "\n")
	if len(rows) != 24 {
		t.Fatalf("frame rows = %d, want 24", len(rows))
	}

	for y, row := range rows {
		runes := []rune(row)
		if len(runes) != 80 {
			t.Fatalf("row %d width = %d, want 80: %q", y, len(runes), row)
		}
		if runes[connectionRailWidth] != '│' {
			t.Fatalf("row %d divider = %q, want │", y, runes[connectionRailWidth])
		}
		if y == 0 && strings.Contains(string(runes[connectionRailWidth+connectionRailDividerWidth:]), "multicrum") {
			t.Fatal("left-mode tab bar must not duplicate the rail's Multicrum title")
		}
	}
}

func TestLeftLayoutHelpBarClick(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := contextMenuTestManager(t, 80, 22, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager
	m.SetConnectionLayout("left")

	_ = m.viewString()
	if !m.s.hasNewConnectionHitbox {
		t.Fatal("vertical new-connection hitbox was not recorded")
	}
	if !m.s.hasHelpHitbox {
		t.Fatal("vertical help hitbox was not recorded")
	}
	if m.s.helpHitbox.Bounds.Y != 23 {
		t.Fatalf("vertical help hitbox row = %d, want 23", m.s.helpHitbox.Bounds.Y)
	}
	if m.s.helpHitbox.Bounds.X+m.s.helpHitbox.Bounds.Width != connectionRailWidth {
		t.Fatalf("vertical help must be right-aligned: %#v", m.s.helpHitbox.Bounds)
	}
	if m.s.newConnectionHitbox.Bounds.X != 0 || m.s.newConnectionHitbox.Bounds.Y != 23 {
		t.Fatalf("vertical New must be left-aligned in rail: %#v", m.s.newConnectionHitbox.Bounds)
	}
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: m.s.helpHitbox.Bounds.X, Y: m.s.helpHitbox.Bounds.Y, Button: tea.MouseLeft, Action: mousePress,
	}); !handled || m.s.mode != modeHelp {
		t.Fatalf("vertical help click handled=%v mode=%v, want true/%v", handled, m.s.mode, modeHelp)
	}

	m.s.mode = modeNormal
	initialConnections := len(m.s.connections)
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: m.s.newConnectionHitbox.Bounds.X, Y: m.s.newConnectionHitbox.Bounds.Y, Button: tea.MouseLeft, Action: mousePress,
	}); !handled || len(m.s.connections) != initialConnections+1 {
		t.Fatalf("vertical new-connection click handled=%v connections=%d, want true/%d", handled, len(m.s.connections), initialConnections+1)
	}
	defer m.s.connections[len(m.s.connections)-1].manager.CloseAll()
}

func TestLeftLayoutMouseCoordinatesArePaneRelative(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.SetConnectionLayout("left")
	x, y, ok := m.s.geometry().ScreenToPane(17, 4)
	if !ok {
		t.Fatal("screen coordinate should be in pane")
	}
	got := string(encodeMouseSGR(mouseEvent{X: x, Y: y, Button: 1, Action: mousePress}))
	if got != "\x1b[<0;2;4M" {
		t.Fatalf("SGR = %q, want pane-relative coordinates", got)
	}
}

func TestLeftLayoutDividerDragResizesRail(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	manager := contextMenuTestManager(t, 80, 22, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager
	m.SetConnectionLayout("left")

	divider := m.s.geometry().ConnectionDivider
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: divider.X, Y: 5, Button: tea.MouseLeft, Action: mousePress,
	}); !handled {
		t.Fatal("divider press was not handled")
	}
	if handled, _ := m.s.handleMouseScopeClick(*m, mouseEvent{
		X: 22, Y: 5, Button: tea.MouseLeft, Action: mouseMotion,
	}); !handled {
		t.Fatal("divider drag was not handled")
	}
	if got := m.s.geometry().ConnectionRail.Width; got != 22 {
		t.Fatalf("dragged rail width = %d, want 22", got)
	}
	if got := m.s.geometry().Pane.Width; got != 57 {
		t.Fatalf("dragged pane width = %d, want 57", got)
	}
	m.s.handleMouseScopeClick(*m, mouseEvent{X: 22, Y: 5, Action: mouseRelease})
	if m.s.mouseDrag.kind != mouseDragNone {
		t.Fatalf("divider drag remained active: %#v", m.s.mouseDrag)
	}
}

func TestConnectionLayoutConfigToggleAndSave(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.SetConfigConnections(&config.Config{ConnectionLayout: "left", ConnectionRailWidth: 22})
	if m.s.connectionLayout != connectionLayoutLeft || m.s.geometry().Pane.Width != 57 {
		t.Fatalf("config layout was not applied before render: %q %#v", m.s.connectionLayout, m.s.geometry())
	}
	m.s.handleConnectionsKey(*m, tea.KeyPressMsg(tea.Key{Code: 'l', Text: "l"}))
	if m.s.connectionLayout != connectionLayoutBottom {
		t.Fatalf("L did not toggle layout to bottom: %q", m.s.connectionLayout)
	}
	m.s.handleConnectionsKey(*m, tea.KeyPressMsg(tea.Key{Code: 'L', Text: "L"}))
	if m.s.connectionLayout != connectionLayoutLeft {
		t.Fatalf("L did not toggle layout to left: %q", m.s.connectionLayout)
	}

	path := "layout-connection-layout-test.yaml"
	defer os.Remove(path)
	m.SetConfigPath(path)
	m.s.saveLayout()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load saved layout: %v", err)
	}
	if cfg.ConnectionLayout != "left" {
		t.Fatalf("saved layout = %q, want left", cfg.ConnectionLayout)
	}
	if cfg.ConnectionRailWidth != 22 {
		t.Fatalf("saved rail width = %d, want 22", cfg.ConnectionRailWidth)
	}
}
