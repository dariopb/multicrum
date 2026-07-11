package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/session"
)

type mouseAction int

const (
	mousePress mouseAction = iota
	mouseRelease
	mouseMotion
)

type mouseEvent struct {
	X      int
	Y      int
	Button tea.MouseButton
	Mod    tea.KeyMod
	Action mouseAction
}

type mouseHitbox struct {
	Bounds rect
	Index  int
	Action hitboxAction
}

type hitboxAction int

const (
	hitboxSession hitboxAction = iota
	hitboxConnection
	hitboxNewSession
	hitboxNewConnection
	hitboxHelp
	hitboxConnections
)

type mouseDragKind int

const (
	mouseDragNone mouseDragKind = iota
	mouseDragSessionTab
	mouseDragConnectionItem
	mouseDragSessionRow
	mouseDragConnectionRow
	mouseDragConnectionDivider
)

type mouseDrag struct {
	kind       mouseDragKind
	session    *session.Session
	connection *connectionState
	moved      bool
}

func mouseEventFromMsg(msg tea.MouseMsg) mouseEvent {
	m := msg.Mouse()
	ev := mouseEvent{X: m.X, Y: m.Y, Button: m.Button, Mod: m.Mod}
	switch msg.(type) {
	case tea.MouseClickMsg:
		ev.Action = mousePress
	case tea.MouseReleaseMsg:
		ev.Action = mouseRelease
	case tea.MouseMotionMsg:
		ev.Action = mouseMotion
	}
	return ev
}

func hitboxAt(boxes []mouseHitbox, x, y int) (mouseHitbox, bool) {
	for _, box := range boxes {
		if (layoutGeometry{}).Contains(box.Bounds, x, y) {
			return box, true
		}
	}
	return mouseHitbox{}, false
}

func (s *state) handleMouseScopeClick(m Model, ev mouseEvent) (bool, tea.Cmd) {
	if s.mouseDrag.kind != mouseDragNone {
		switch ev.Action {
		case mouseMotion:
			s.updateScopeDrag(ev)
		case mouseRelease:
			s.finishScopeDrag(ev)
		}
		return true, nil
	}
	if ev.Action != mousePress {
		return false, nil
	}
	geom := s.geometry()
	if ev.Button == tea.MouseLeft && geom.Contains(geom.ConnectionDivider, ev.X, ev.Y) {
		s.mouseDrag = mouseDrag{kind: mouseDragConnectionDivider}
		return true, nil
	}
	if s.hasNewSessionHitbox && ev.Button == tea.MouseLeft {
		if _, ok := hitboxAt([]mouseHitbox{s.newSessionHitbox}, ev.X, ev.Y); ok {
			return s.handleShortcut(m, tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl | tea.ModAlt}))
		}
	}
	if s.hasNewConnectionHitbox && ev.Button == tea.MouseLeft {
		if _, ok := hitboxAt([]mouseHitbox{s.newConnectionHitbox}, ev.X, ev.Y); ok {
			return s.handleShortcut(m, tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl | tea.ModAlt}))
		}
	}
	if s.hasConnectionsHitbox && ev.Button == tea.MouseLeft {
		if _, ok := hitboxAt([]mouseHitbox{s.connectionsHitbox}, ev.X, ev.Y); ok {
			return s.handleShortcut(m, tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl | tea.ModAlt}))
		}
	}
	if s.hasHelpHitbox && ev.Button == tea.MouseLeft {
		if _, ok := hitboxAt([]mouseHitbox{s.helpHitbox}, ev.X, ev.Y); ok {
			return s.handleShortcut(m, tea.KeyPressMsg(tea.Key{Code: '`', Mod: tea.ModAlt}))
		}
	}
	if box, ok := hitboxAt(s.sessionHitboxes, ev.X, ev.Y); ok {
		if ev.Button == tea.MouseRight {
			s.openContextMenu(sessionContextMenu, box.Index, ev.X, ev.Y)
			return true, nil
		}
		if ev.Button != tea.MouseLeft {
			return false, nil
		}
		s.clearSelection()
		if s.manager != nil {
			s.mouseDrag = mouseDrag{
				kind:    mouseDragSessionTab,
				session: s.manager.ByID(box.Index),
			}
		}
		return true, nil
	}
	if box, ok := hitboxAt(s.connectionHitboxes, ev.X, ev.Y); ok {
		if ev.Button == tea.MouseRight {
			s.openContextMenu(connectionContextMenu, box.Index, ev.X, ev.Y)
			return true, nil
		}
		if ev.Button != tea.MouseLeft {
			return false, nil
		}
		s.clearSelection()
		if box.Index >= 0 && box.Index < len(s.connections) {
			s.mouseDrag = mouseDrag{
				kind:       mouseDragConnectionItem,
				connection: s.connections[box.Index],
			}
		}
		return true, nil
	}
	// The rail is UI chrome, even its blank space; do not let clicks leak to
	// the child terminal or start a selection.
	if s.geometry().Contains(s.geometry().ConnectionRail, ev.X, ev.Y) {
		return true, nil
	}
	return false, nil
}

func (s *state) updateScopeDrag(ev mouseEvent) {
	switch s.mouseDrag.kind {
	case mouseDragConnectionDivider:
		s.resizeConnectionRail(ev.X)
	case mouseDragSessionTab:
		box, ok := hitboxAt(s.sessionHitboxes, ev.X, ev.Y)
		if !ok || s.mouseDrag.session == nil || s.manager == nil {
			return
		}
		target := s.manager.ByID(box.Index)
		from := sessionIndex(s.manager, s.mouseDrag.session)
		to := sessionIndex(s.manager, target)
		if from >= 0 && to >= 0 && from != to {
			s.moveSession(from, to)
			s.mouseDrag.moved = true
		}
	case mouseDragConnectionItem:
		box, ok := hitboxAt(s.connectionHitboxes, ev.X, ev.Y)
		if !ok || s.mouseDrag.connection == nil || box.Index < 0 || box.Index >= len(s.connections) {
			return
		}
		from := connectionIndex(s.connections, s.mouseDrag.connection)
		to := connectionIndex(s.connections, s.connections[box.Index])
		if from >= 0 && to >= 0 && from != to {
			s.moveConnection(from, to)
			s.mouseDrag.moved = true
		}
	}
}

func (s *state) finishScopeDrag(ev mouseEvent) {
	drag := s.mouseDrag
	s.mouseDrag = mouseDrag{}
	if drag.moved {
		return
	}
	switch drag.kind {
	case mouseDragConnectionDivider:
		return
	case mouseDragSessionTab:
		box, ok := hitboxAt(s.sessionHitboxes, ev.X, ev.Y)
		if !ok || s.manager.ByID(box.Index) != drag.session {
			return
		}
		index := sessionIndex(s.manager, drag.session)
		if index >= 0 && index != s.manager.FocusedIndex() {
			s.manager.Focus(index)
			s.refreshFocused()
			s.notifyMeta()
		}
	case mouseDragConnectionItem:
		box, ok := hitboxAt(s.connectionHitboxes, ev.X, ev.Y)
		if !ok || box.Index < 0 || box.Index >= len(s.connections) ||
			s.connections[box.Index] != drag.connection {
			return
		}
		index := connectionIndex(s.connections, drag.connection)
		if index >= 0 && index != s.activeConn {
			s.focusConnection(index)
		}
	}
}

func sessionIndex(manager *session.SessionManager, target *session.Session) int {
	if manager == nil || target == nil {
		return -1
	}
	for index, sess := range manager.Sessions() {
		if sess == target {
			return index
		}
	}
	return -1
}

func connectionIndex(connections []*connectionState, target *connectionState) int {
	for index, conn := range connections {
		if conn == target {
			return index
		}
	}
	return -1
}

// encodeMouseSGR converts a Bubble Tea mouse event into the SGR (1006) mouse
// protocol byte sequence that child TUIs like btop, htop, lazygit, vim, etc.
// understand. Returns nil when the event has no useful encoding.
//
// Format: CSI < Cb ; Cx ; Cy M  (press/motion)   or   CSI < Cb ; Cx ; Cy m  (release)
// Coordinates are 1-based.
func encodeMouseSGR(ev mouseEvent) []byte {
	cb, ok := mouseButtonCode(ev.Button)
	if !ok {
		return nil
	}
	if ev.Action == mouseMotion && ev.Button == tea.MouseNone {
		cb = 35 // pure motion report
	} else if ev.Action == mouseMotion {
		cb += 32 // motion-with-button
	}
	if ev.Mod.Contains(tea.ModShift) {
		cb |= 4
	}
	if ev.Mod.Contains(tea.ModAlt) {
		cb |= 8
	}
	if ev.Mod.Contains(tea.ModCtrl) {
		cb |= 16
	}
	final := byte('M')
	if ev.Action == mouseRelease && !isWheelButton(ev.Button) {
		final = 'm'
	}
	x := ev.X + 1
	y := ev.Y + 1
	return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", cb, x, y, final))
}

func mouseButtonCode(b tea.MouseButton) (int, bool) {
	switch b {
	case tea.MouseNone:
		return 3, true // for motion-only / release
	case tea.MouseLeft:
		return 0, true
	case tea.MouseMiddle:
		return 1, true
	case tea.MouseRight:
		return 2, true
	case tea.MouseWheelUp:
		return 64, true
	case tea.MouseWheelDown:
		return 65, true
	case tea.MouseWheelLeft:
		return 66, true
	case tea.MouseWheelRight:
		return 67, true
	case tea.MouseBackward:
		return 128, true
	case tea.MouseForward:
		return 129, true
	}
	return 0, false
}

func isWheelButton(b tea.MouseButton) bool {
	switch b {
	case tea.MouseWheelUp, tea.MouseWheelDown,
		tea.MouseWheelLeft, tea.MouseWheelRight:
		return true
	}
	return false
}
