package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
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
	Start int
	End   int
	Index int
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

func hitboxAt(boxes []mouseHitbox, x int) (int, bool) {
	for _, box := range boxes {
		if x >= box.Start && x < box.End {
			return box.Index, true
		}
	}
	return 0, false
}

func (s *state) handleMouseScopeClick(ev mouseEvent) bool {
	if ev.Action != mousePress || ev.Button != tea.MouseLeft {
		return false
	}
	if ev.Y == 0 {
		idx, ok := hitboxAt(s.sessionHitboxes, ev.X)
		if !ok {
			return false
		}
		s.mode = modeNormal
		s.clearSelection()
		if s.manager != nil && idx >= 0 && idx < s.manager.Len() && idx != s.manager.FocusedIndex() {
			s.manager.Focus(idx)
			s.refreshFocused()
			s.notifyMeta()
		}
		return true
	}
	_, paneRows := paneSize(s.width, s.height)
	if ev.Y == paneRows+1 {
		idx, ok := hitboxAt(s.connectionHitboxes, ev.X)
		if !ok {
			return false
		}
		s.mode = modeNormal
		s.clearSelection()
		if idx >= 0 && idx < len(s.connections) && idx != s.activeConn {
			s.focusConnection(idx)
		}
		return true
	}
	return false
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
