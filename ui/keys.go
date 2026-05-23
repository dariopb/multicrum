package ui

import (
	tea "charm.land/bubbletea/v2"
)

// isCtrlAlt returns true when msg is Ctrl+Alt+<key>. Bubble Tea v2 represents
// modifiers in Key.Mod and the base key in Key.Code.
func isCtrlAlt(msg tea.KeyPressMsg, code rune) bool {
	k := msg.Key()
	return k.Code == code && k.Mod.Contains(tea.ModCtrl|tea.ModAlt)
}

// keyToBytes converts a Bubble Tea KeyPressMsg to the byte sequence that should
// be written to the PTY. Bubble Tea gives us semantic keys ("enter",
// "backspace", …) but the PTY needs real terminal escape sequences.
func keyToBytes(msg tea.KeyPressMsg, appCursor bool) []byte {
	k := msg.Key()
	if k.Text != "" && !k.Mod.Contains(tea.ModCtrl) {
		if k.Mod.Contains(tea.ModAlt) {
			b := []byte{0x1b}
			return append(b, []byte(k.Text)...)
		}
		return []byte(k.Text)
	}
	prefix := ""
	if k.Mod.Contains(tea.ModAlt) && !k.Mod.Contains(tea.ModCtrl) {
		prefix = "\x1b"
	}
	if k.Mod.Contains(tea.ModCtrl) {
		if b, ok := ctrlByte(k.Code); ok {
			if k.Mod.Contains(tea.ModAlt) {
				return []byte{0x1b, b}
			}
			return []byte{b}
		}
	}
	if seq, ok := keySequence(k, appCursor); ok {
		return []byte(prefix + seq)
	}
	return nil
}

func keySequence(k tea.Key, appCursor bool) (string, bool) {
	if appCursor {
		switch k.Code {
		case tea.KeyUp:
			return "\x1bOA", true
		case tea.KeyDown:
			return "\x1bOB", true
		case tea.KeyRight:
			return "\x1bOC", true
		case tea.KeyLeft:
			return "\x1bOD", true
		}
	}
	switch k.Code {
	case tea.KeyEnter, tea.KeyKpEnter:
		return "\r", true
	case tea.KeyBackspace:
		return "\x7f", true
	case tea.KeyDelete, tea.KeyKpDelete:
		return "\x1b[3~", true
	case tea.KeyTab:
		if k.Mod.Contains(tea.ModShift) {
			return "\x1b[Z", true
		}
		return "\t", true
	case tea.KeySpace:
		return " ", true
	case tea.KeyEscape:
		return "\x1b", true
	case tea.KeyUp, tea.KeyKpUp:
		return arrowSeq("A", k.Mod), true
	case tea.KeyDown, tea.KeyKpDown:
		return arrowSeq("B", k.Mod), true
	case tea.KeyRight, tea.KeyKpRight:
		return arrowSeq("C", k.Mod), true
	case tea.KeyLeft, tea.KeyKpLeft:
		return arrowSeq("D", k.Mod), true
	case tea.KeyHome, tea.KeyKpHome:
		return homeEndSeq("H", k.Mod), true
	case tea.KeyEnd, tea.KeyKpEnd:
		return homeEndSeq("F", k.Mod), true
	case tea.KeyPgUp, tea.KeyKpPgUp:
		return modTildeSeq(5, k.Mod), true
	case tea.KeyPgDown, tea.KeyKpPgDown:
		return modTildeSeq(6, k.Mod), true
	case tea.KeyInsert, tea.KeyKpInsert:
		return "\x1b[2~", true
	case tea.KeyF1:
		return "\x1bOP", true
	case tea.KeyF2:
		return "\x1bOQ", true
	case tea.KeyF3:
		return "\x1bOR", true
	case tea.KeyF4:
		return "\x1bOS", true
	case tea.KeyF5:
		return "\x1b[15~", true
	case tea.KeyF6:
		return "\x1b[17~", true
	case tea.KeyF7:
		return "\x1b[18~", true
	case tea.KeyF8:
		return "\x1b[19~", true
	case tea.KeyF9:
		return "\x1b[20~", true
	case tea.KeyF10:
		return "\x1b[21~", true
	case tea.KeyF11:
		return "\x1b[23~", true
	case tea.KeyF12:
		return "\x1b[24~", true
	}
	return "", false
}

func arrowSeq(final string, mod tea.KeyMod) string {
	if mod.Contains(tea.ModCtrl) && mod.Contains(tea.ModShift) {
		return "\x1b[1;6" + final
	}
	if mod.Contains(tea.ModCtrl) {
		return "\x1b[1;5" + final
	}
	if mod.Contains(tea.ModShift) {
		return "\x1b[1;2" + final
	}
	return "\x1b[" + final
}

func homeEndSeq(final string, mod tea.KeyMod) string {
	if mod.Contains(tea.ModCtrl) && mod.Contains(tea.ModShift) {
		return "\x1b[1;6" + final
	}
	if mod.Contains(tea.ModCtrl) {
		return "\x1b[1;5" + final
	}
	if mod.Contains(tea.ModShift) {
		return "\x1b[1;2" + final
	}
	return "\x1b[" + final
}

func modTildeSeq(code int, mod tea.KeyMod) string {
	if mod.Contains(tea.ModCtrl) {
		return "\x1b[" + string(rune('0'+code)) + ";5~"
	}
	return "\x1b[" + string(rune('0'+code)) + "~"
}

func ctrlByte(code rune) (byte, bool) {
	if code >= 'a' && code <= 'z' {
		return byte(code - 'a' + 1), true
	}
	if code >= 'A' && code <= 'Z' {
		return byte(code - 'A' + 1), true
	}
	switch code {
	case '@':
		return 0x00, true
	case '[':
		return 0x1b, true
	case '\\':
		return 0x1c, true
	case ']':
		return 0x1d, true
	case '^':
		return 0x1e, true
	case '_':
		return 0x1f, true
	}
	return 0, false
}
