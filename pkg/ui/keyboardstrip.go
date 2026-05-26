package ui

import (
	"bytes"
	"io"
)

// keyboardStripWriter wraps an io.Writer and strips terminal "keyboard
// enhancement" escape sequences before forwarding the rest. Bubble Tea v2
// unconditionally emits these on startup, shutdown, and alt-screen
// transitions; in a multiplexer they leak into child PTYs and break input
// handling for any child that does not understand them.
//
// Stripped sequences:
//
//   - CSI > 4 ; 2 m   (SetModifyOtherKeys2)
//   - CSI > 4 m       (ResetModifyOtherKeys)
//   - CSI ? u         (RequestKittyKeyboard)
//   - CSI = <flags> ; <mode> u (KittyKeyboard set)
//   - CSI > <flags> u (PushKittyKeyboard)
//   - CSI < u         (PopKittyKeyboard)
//
// All other bytes (including CSI sequences we don't recognize) pass through
// unchanged.
type keyboardStripWriter struct {
	w   io.Writer
	buf []byte // partial sequence held between Write calls
}

// NewKeyboardStripWriter wraps w with a filter that removes keyboard
// enhancement escapes. Use it around the io.Writer passed to
// tea.WithOutput when embedding multicrum so bubbletea's startup
// sequences do not leak into child PTYs.
//
// If w also implements io.ReadWriteCloser and Fd() (i.e. it's an
// *os.File like os.Stdout), the returned writer satisfies bubbletea's
// term.File interface so TTY detection keeps working and the program
// renders normally.
func NewKeyboardStripWriter(w io.Writer) io.Writer {
	base := &keyboardStripWriter{w: w}
	if f, ok := w.(termFile); ok {
		return &keyboardStripFile{keyboardStripWriter: base, file: f}
	}
	return base
}

// termFile mirrors charmbracelet/x/term.File without importing it,
// keeping this file free of bubbletea-specific dependencies.
type termFile interface {
	io.ReadWriteCloser
	Fd() uintptr
}

// keyboardStripFile is the term.File-shaped variant returned by
// NewKeyboardStripWriter when its underlying writer is a real TTY
// file. It delegates Read/Close/Fd to the wrapped file and only
// filters Write.
type keyboardStripFile struct {
	*keyboardStripWriter
	file termFile
}

func (k *keyboardStripFile) Read(p []byte) (int, error) { return k.file.Read(p) }
func (k *keyboardStripFile) Close() error               { return k.file.Close() }
func (k *keyboardStripFile) Fd() uintptr                { return k.file.Fd() }

func (k *keyboardStripWriter) Write(p []byte) (int, error) {
	// Append to any partial sequence carried over from a previous Write.
	data := p
	if len(k.buf) > 0 {
		data = append(k.buf, p...)
		k.buf = nil
	}

	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		b := data[i]
		if b != 0x1b {
			out = append(out, b)
			i++
			continue
		}
		// ESC: check for CSI (ESC [).
		if i+1 >= len(data) {
			k.buf = append(k.buf, data[i:]...)
			break
		}
		if data[i+1] != '[' {
			// Not a CSI; pass ESC through and continue at next byte.
			out = append(out, data[i])
			i++
			continue
		}
		// Try to scan a complete CSI sequence: ESC [ <params> <final>.
		end, ok := scanCSIEnd(data, i+2)
		if !ok {
			// Incomplete sequence; buffer remainder.
			k.buf = append(k.buf, data[i:]...)
			break
		}
		seq := data[i : end+1]
		if isKeyboardEnhancement(seq) {
			// Drop it.
		} else {
			out = append(out, seq...)
		}
		i = end + 1
	}

	if _, err := k.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// scanCSIEnd finds the index of the final byte of a CSI sequence whose
// parameter bytes begin at start. Returns ok=false if data is truncated.
//
// CSI grammar: parameter bytes 0x30-0x3F, intermediate bytes 0x20-0x2F,
// final byte 0x40-0x7E.
func scanCSIEnd(data []byte, start int) (int, bool) {
	for j := start; j < len(data); j++ {
		c := data[j]
		if c >= 0x40 && c <= 0x7E {
			return j, true
		}
		if c < 0x20 || c > 0x3F {
			// Malformed sequence; treat current position as final to
			// avoid swallowing unrelated bytes.
			return j, true
		}
	}
	return 0, false
}

// isKeyboardEnhancement reports whether seq (a complete CSI sequence
// including the leading ESC [) is one of the keyboard-enhancement
// sequences bubbletea emits.
func isKeyboardEnhancement(seq []byte) bool {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return false
	}
	final := seq[len(seq)-1]
	body := seq[2 : len(seq)-1]

	switch final {
	case 'm':
		// modifyOtherKeys: CSI > 4 m  or  CSI > 4 ; <n> m
		if len(body) > 0 && body[0] == '>' {
			rest := body[1:]
			if bytes.Equal(rest, []byte("4")) {
				return true
			}
			if bytes.HasPrefix(rest, []byte("4;")) && allDigits(rest[2:]) {
				return true
			}
		}
	case 'u':
		// Kitty keyboard variants.
		if len(body) == 0 {
			return false
		}
		switch body[0] {
		case '?':
			// CSI ? u (request) — only the bare form, no params.
			return len(body) == 1
		case '=':
			// CSI = <flags>[;<mode>] u
			return validKittyParams(body[1:])
		case '>':
			// CSI > [<flags>] u (push)
			return validKittyParams(body[1:])
		case '<':
			// CSI < u (pop)
			return len(body) == 1
		}
	}
	return false
}

// validKittyParams reports whether p is a (possibly empty) sequence of
// digits and semicolons — the parameter form used by the Kitty keyboard
// set/push sequences. Refuses any other private/intermediate markers so
// unrelated sequences pass through untouched.
func validKittyParams(p []byte) bool {
	for _, c := range p {
		if c == ';' {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func allDigits(p []byte) bool {
	if len(p) == 0 {
		return false
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
