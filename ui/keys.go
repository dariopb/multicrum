package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// isCtrlAlt returns true when msg is Ctrl+Alt+<letter>. We detect this via
// Bubble Tea's KeyCtrlX types combined with the Alt flag, which is set when
// the terminal prefixes the control byte with ESC.
func isCtrlAlt(msg tea.KeyMsg, t tea.KeyType) bool {
	return msg.Alt && msg.Type == t
}

// keyToBytes converts a Bubble Tea KeyMsg to the byte sequence that should
// be written to the PTY. Bubble Tea gives us semantic names ("enter",
// "backspace", …) but the PTY needs real terminal escape sequences.
//
// Every printable rune, every named special key, every Ctrl+letter and every
// Alt-prefixed variant must be forwarded so child TUIs see exactly what the
// user typed.
func keyToBytes(msg tea.KeyMsg, appCursor bool) []byte {
	// For regular rune input (including Alt+rune sequences Bubble Tea decodes),
	// use the raw bytes that Bubble Tea already has.
	if msg.Type == tea.KeyRunes {
		if msg.Alt {
			b := []byte{0x1b}
			return append(b, []byte(string(msg.Runes))...)
		}
		return []byte(string(msg.Runes))
	}

	// Space is sometimes delivered as KeySpace with msg.Runes empty.
	if msg.Type == tea.KeySpace {
		if msg.Alt {
			return []byte{0x1b, ' '}
		}
		return []byte{' '}
	}

	prefix := ""
	if msg.Alt {
		prefix = "\x1b"
	}
	if seq, ok := keySequences[msg.Type]; ok {
		if appCursor {
			if appSeq, ok := appCursorKeySequences[msg.Type]; ok {
				seq = appSeq
			}
		}
		return []byte(prefix + seq)
	}
	return nil
}

var appCursorKeySequences = map[tea.KeyType]string{
	tea.KeyUp:    "\x1bOA",
	tea.KeyDown:  "\x1bOB",
	tea.KeyRight: "\x1bOC",
	tea.KeyLeft:  "\x1bOD",
}

// keySequences maps Bubble Tea KeyType values to VT100/xterm byte sequences.
var keySequences = map[tea.KeyType]string{
	tea.KeyEnter:     "\r",
	tea.KeyBackspace: "\x7f",
	tea.KeyDelete:    "\x1b[3~",
	tea.KeyTab:       "\t",
	tea.KeyShiftTab:  "\x1b[Z",
	tea.KeySpace:     " ",
	tea.KeyEscape:    "\x1b",
	tea.KeyUp:        "\x1b[A",
	tea.KeyDown:      "\x1b[B",
	tea.KeyRight:     "\x1b[C",
	tea.KeyLeft:      "\x1b[D",
	tea.KeyShiftUp:    "\x1b[1;2A",
	tea.KeyShiftDown:  "\x1b[1;2B",
	tea.KeyShiftRight: "\x1b[1;2C",
	tea.KeyShiftLeft:  "\x1b[1;2D",
	tea.KeyCtrlUp:     "\x1b[1;5A",
	tea.KeyCtrlDown:   "\x1b[1;5B",
	tea.KeyCtrlRight:  "\x1b[1;5C",
	tea.KeyCtrlLeft:   "\x1b[1;5D",
	tea.KeyCtrlShiftUp:    "\x1b[1;6A",
	tea.KeyCtrlShiftDown:  "\x1b[1;6B",
	tea.KeyCtrlShiftRight: "\x1b[1;6C",
	tea.KeyCtrlShiftLeft:  "\x1b[1;6D",
	tea.KeyHome:      "\x1b[H",
	tea.KeyEnd:       "\x1b[F",
	tea.KeyCtrlHome:  "\x1b[1;5H",
	tea.KeyCtrlEnd:   "\x1b[1;5F",
	tea.KeyShiftHome: "\x1b[1;2H",
	tea.KeyShiftEnd:  "\x1b[1;2F",
	tea.KeyCtrlShiftHome: "\x1b[1;6H",
	tea.KeyCtrlShiftEnd:  "\x1b[1;6F",
	tea.KeyPgUp:      "\x1b[5~",
	tea.KeyPgDown:    "\x1b[6~",
	tea.KeyCtrlPgUp:  "\x1b[5;5~",
	tea.KeyCtrlPgDown: "\x1b[6;5~",
	tea.KeyInsert:    "\x1b[2~",
	tea.KeyF1:        "\x1bOP",
	tea.KeyF2:        "\x1bOQ",
	tea.KeyF3:        "\x1bOR",
	tea.KeyF4:        "\x1bOS",
	tea.KeyF5:        "\x1b[15~",
	tea.KeyF6:        "\x1b[17~",
	tea.KeyF7:        "\x1b[18~",
	tea.KeyF8:        "\x1b[19~",
	tea.KeyF9:        "\x1b[20~",
	tea.KeyF10:       "\x1b[21~",
	tea.KeyF11:       "\x1b[23~",
	tea.KeyF12:       "\x1b[24~",
	// Ctrl keys that Bubble Tea decodes as named types — these MUST forward
	// to the PTY so apps like vim, emacs, htop, fzf, lazygit etc. see Ctrl+X.
	tea.KeyCtrlAt: "\x00",
	tea.KeyCtrlA:  "\x01",
	tea.KeyCtrlB:  "\x02",
	tea.KeyCtrlC:  "\x03",
	tea.KeyCtrlD:  "\x04",
	tea.KeyCtrlE:  "\x05",
	tea.KeyCtrlF:  "\x06",
	tea.KeyCtrlG:  "\x07",
	tea.KeyCtrlH:  "\x08",
	// KeyCtrlI == KeyTab (9), KeyCtrlM == KeyEnter (13) — skip to avoid dup
	tea.KeyCtrlJ: "\x0a",
	tea.KeyCtrlK: "\x0b",
	tea.KeyCtrlL: "\x0c",
	tea.KeyCtrlN: "\x0e",
	tea.KeyCtrlO: "\x0f",
	tea.KeyCtrlP: "\x10",
	tea.KeyCtrlQ: "\x11",
	tea.KeyCtrlR: "\x12",
	tea.KeyCtrlS: "\x13",
	tea.KeyCtrlT: "\x14",
	tea.KeyCtrlU: "\x15",
	tea.KeyCtrlV: "\x16",
	tea.KeyCtrlW: "\x17",
	tea.KeyCtrlX: "\x18",
	tea.KeyCtrlY: "\x19",
	tea.KeyCtrlZ: "\x1a",
	tea.KeyCtrlBackslash:    "\x1c",
	tea.KeyCtrlCloseBracket: "\x1d",
	tea.KeyCtrlCaret:        "\x1e",
	tea.KeyCtrlUnderscore:   "\x1f",
}
