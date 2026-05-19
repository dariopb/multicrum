package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// encodeMouseSGR converts a Bubble Tea mouse event into the SGR (1006) mouse
// protocol byte sequence that child TUIs like btop, htop, lazygit, vim, etc.
// understand. Returns nil when the event has no useful encoding.
//
// Format: CSI < Cb ; Cx ; Cy M  (press/motion)   or   CSI < Cb ; Cx ; Cy m  (release)
// Coordinates are 1-based.
func encodeMouseSGR(ev tea.MouseEvent) []byte {
	cb, ok := mouseButtonCode(ev.Button)
	if !ok {
		return nil
	}
	if ev.Action == tea.MouseActionMotion && ev.Button == tea.MouseButtonNone {
		cb = 35 // pure motion report
	} else if ev.Action == tea.MouseActionMotion {
		cb += 32 // motion-with-button
	}
	if ev.Shift {
		cb |= 4
	}
	if ev.Alt {
		cb |= 8
	}
	if ev.Ctrl {
		cb |= 16
	}
	final := byte('M')
	if ev.Action == tea.MouseActionRelease && !isWheelButton(ev.Button) {
		final = 'm'
	}
	x := ev.X + 1
	y := ev.Y + 1
	return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", cb, x, y, final))
}

func mouseButtonCode(b tea.MouseButton) (int, bool) {
	switch b {
	case tea.MouseButtonNone:
		return 3, true // for motion-only / release
	case tea.MouseButtonLeft:
		return 0, true
	case tea.MouseButtonMiddle:
		return 1, true
	case tea.MouseButtonRight:
		return 2, true
	case tea.MouseButtonWheelUp:
		return 64, true
	case tea.MouseButtonWheelDown:
		return 65, true
	case tea.MouseButtonWheelLeft:
		return 66, true
	case tea.MouseButtonWheelRight:
		return 67, true
	case tea.MouseButtonBackward:
		return 128, true
	case tea.MouseButtonForward:
		return 129, true
	}
	return 0, false
}

func isWheelButton(b tea.MouseButton) bool {
	switch b {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown,
		tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		return true
	}
	return false
}
