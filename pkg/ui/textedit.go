package ui

// Small helpers for editable single-line text fields with a rune-based cursor.
// All positions are rune indices (0..len(runes)).

func clampCursor(s string, pos int) int {
	n := len([]rune(s))
	if pos < 0 {
		return 0
	}
	if pos > n {
		return n
	}
	return pos
}

// insertAt inserts text into s at rune position pos. Returns new string
// and new cursor position (placed after the inserted text).
func insertAt(s string, pos int, text string) (string, int) {
	r := []rune(s)
	pos = clampCursor(s, pos)
	ins := []rune(text)
	out := make([]rune, 0, len(r)+len(ins))
	out = append(out, r[:pos]...)
	out = append(out, ins...)
	out = append(out, r[pos:]...)
	return string(out), pos + len(ins)
}

// backspaceAt deletes the rune immediately before pos.
func backspaceAt(s string, pos int) (string, int) {
	r := []rune(s)
	pos = clampCursor(s, pos)
	if pos == 0 {
		return s, 0
	}
	out := make([]rune, 0, len(r)-1)
	out = append(out, r[:pos-1]...)
	out = append(out, r[pos:]...)
	return string(out), pos - 1
}

// deleteAt deletes the rune at pos (the one to the right of the cursor).
func deleteAt(s string, pos int) (string, int) {
	r := []rune(s)
	pos = clampCursor(s, pos)
	if pos >= len(r) {
		return s, pos
	}
	out := make([]rune, 0, len(r)-1)
	out = append(out, r[:pos]...)
	out = append(out, r[pos+1:]...)
	return string(out), pos
}

// renderWithCursor returns s with a thin cursor bar inserted at the given
// rune position. Used for modal text fields.
func renderWithCursor(s string, pos int) string {
	r := []rune(s)
	pos = clampCursor(s, pos)
	return string(r[:pos]) + "▏" + string(r[pos:])
}
