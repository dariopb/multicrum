package session

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hinshun/vt10x"
)

const (
	maxScrollback      = 256 * 1024 // raw byte cap for WS replay
	maxScrollbackLines = 10000      // visible line cap for local scrollback
)

// VTScreen maintains a virtual terminal screen buffer using vt10x.
type VTScreen struct {
	mu         sync.Mutex
	term       vt10x.Terminal
	cols       int
	rows       int
	dirty      bool
	rawHistory []byte       // raw PTY bytes capped at maxScrollback, for WS replay
	scrollback []string     // pre-rendered (with SGR) lines that scrolled off the top
	plainSB    []BufferLine // plain-text + soft-wrap flag, parallel to scrollback
	reply      atomic.Pointer[io.Writer]
	replies    bool
}

// BufferLine is one physical row of the terminal as plain text plus whether
// the row is soft-wrapped (its last cell carries vt10x's attrWrap bit). The
// UI uses these to extract selection text with correct line breaks.
type BufferLine struct {
	Text     string
	SoftWrap bool
}

// replyWriter is a thin shim that forwards vt10x replies (CPR, DSR, etc.)
// to whatever writer the session has assigned. We MUST NOT take s.mu here:
// vt10x calls this writer from inside its own Write (which is called while
// VTScreen.Write already holds s.mu) — re-entering would deadlock.
type replyWriter struct {
	s *VTScreen
}

func (r *replyWriter) Write(p []byte) (int, error) {
	wp := r.s.reply.Load()
	if wp == nil || *wp == nil {
		return len(p), nil
	}
	w := *wp
	// Copy because vt10x reuses the buffer, and dispatch asynchronously so a
	// blocked PTY write never stalls the readLoop that's feeding vt10x.
	buf := make([]byte, len(p))
	copy(buf, p)
	go func() { _, _ = w.Write(buf) }()
	return len(p), nil
}

// NewVTScreen creates a VT screen of given dimensions.
func NewVTScreen(cols, rows int) *VTScreen {
	s := &VTScreen{cols: cols, rows: rows}
	s.term = vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(&replyWriter{s: s}))
	return s
}

// SetReplyWriter installs the writer that receives vt10x-generated terminal
// replies such as the CPR (cursor position report) response. Apps like fzf,
// less, ranger, etc. issue ESC[6n and wait for a reply; without this they
// print "your terminal doesn't support cursor position requests".
func (s *VTScreen) SetReplyWriter(w io.Writer) {
	s.reply.Store(&w)
}

func (s *VTScreen) SetTerminalReplies(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = enabled
}

// Write feeds raw PTY bytes into the VT parser. If the write produced a true
// upward scroll (new line entered from the bottom while the top line dropped
// off), the dropped line is captured into scrollback. In-place screen updates
// from TUIs do NOT add to scrollback.
func (s *VTScreen) Write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update raw replay history first (xterm.js doesn't care about scrollback
	// semantics — it just wants every byte we ever received).
	s.rawHistory = append(s.rawHistory, p...)
	if len(s.rawHistory) > maxScrollback {
		s.rawHistory = s.rawHistory[len(s.rawHistory)-maxScrollback:]
	}

	p = s.filterTerminalQueriesLocked(p)
	if len(p) == 0 {
		return
	}

	// Bulk writes from commands like `ls -l /usr/bin` can scroll many rows
	// within a single Write. Diffing only the final before/after state would
	// miss everything except the last few rows. Split the input at scroll
	// triggers so each inner write scrolls at most a small amount and we can
	// reliably capture each dropped row.
	for _, chunk := range splitScrollChunks(p) {
		s.writeChunkLocked(chunk)
	}
}

func (s *VTScreen) filterTerminalQueriesLocked(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); {
		if i+5 <= len(p) && p[i] == 0x1b && p[i+1] == ']' && p[i+2] == '1' && (p[i+3] == '0' || p[i+3] == '1') && p[i+4] == ';' {
			cmd := p[i+3]
			if end, ok := oscEnd(p, i+5); ok {
				payload := string(p[i+5 : end])
				if payload == "?" {
					s.replyOSCColorLocked(cmd)
					i = oscAfter(p, end)
					continue
				}
			}
		}
		if i+7 <= len(p) && p[i] == 0x1b && p[i+1] == '[' && p[i+2] == '?' && p[i+3] == '2' && p[i+4] == '0' && p[i+5] == '2' && p[i+6] == '6' {
			j := i + 7
			if j < len(p) && (p[j] == 'h' || p[j] == 'l') {
				i = j + 1
				continue
			}
		}
		out = append(out, p[i])
		i++
	}
	return out
}

func oscEnd(p []byte, start int) (int, bool) {
	for i := start; i < len(p); i++ {
		if p[i] == 0x07 {
			return i, true
		}
		if i+1 < len(p) && p[i] == 0x1b && p[i+1] == '\\' {
			return i, true
		}
	}
	return 0, false
}

func oscAfter(p []byte, end int) int {
	if end+1 < len(p) && p[end] == 0x1b && p[end+1] == '\\' {
		return end + 2
	}
	return end + 1
}

func (s *VTScreen) replyOSCColorLocked(cmd byte) {
	if wp := s.reply.Load(); wp != nil && *wp != nil {
		response := ""
		switch cmd {
		case '0':
			response = "\x1b]10;rgb:eeee/eeee/eeee\x1b\\"
		case '1':
			response = "\x1b]11;rgb:0000/0000/0000\x1b\\"
		}
		if response != "" {
			w := *wp
			go func() { _, _ = w.Write([]byte(response)) }()
		}
	}
}

func (s *VTScreen) writeChunkLocked(p []byte) {
	if len(p) == 0 {
		return
	}
	canScroll := s.term.Mode()&vt10x.ModeAltScreen == 0
	var beforeTop string
	var beforeTopPlain string
	var beforeTopSoftWrap bool
	var beforeTopValid bool
	if canScroll && containsScrollTrigger(p) {
		beforeTop = s.renderRowAtLocked(0, -1)
		beforeTopPlain, beforeTopSoftWrap = s.plainRowAtLocked(0)
		beforeTopValid = true
	}

	_, _ = s.term.Write(p)
	s.dirty = true

	if !beforeTopValid {
		return
	}
	if s.term.Mode()&vt10x.ModeAltScreen != 0 {
		return // entered alt screen mid-write; abandon scroll capture
	}
	afterTop := s.renderRowAtLocked(0, -1)
	if afterTop == beforeTop {
		return
	}
	s.scrollback = append(s.scrollback, trimTrailingSpaces(beforeTop))
	if len(s.scrollback) > maxScrollbackLines {
		s.scrollback = s.scrollback[len(s.scrollback)-maxScrollbackLines:]
	}
	plain := beforeTopPlain
	if !beforeTopSoftWrap {
		plain = strings.TrimRight(plain, " ")
	}
	s.plainSB = append(s.plainSB, BufferLine{Text: plain, SoftWrap: beforeTopSoftWrap})
	if len(s.plainSB) > maxScrollbackLines {
		s.plainSB = s.plainSB[len(s.plainSB)-maxScrollbackLines:]
	}
}

// trimTrailingSpaces removes trailing spaces from a line that may end with an
// SGR reset (\x1b[0m). It preserves the reset by re-appending it if present.
func trimTrailingSpaces(s string) string {
	const reset = "\x1b[0m"
	hasReset := strings.HasSuffix(s, reset)
	if hasReset {
		s = s[:len(s)-len(reset)]
	}
	s = strings.TrimRight(s, " ")
	if hasReset {
		s += reset
	}
	return s
}

// topRowPlainLocked returns the plain-text contents of row 0. O(cols), no SGR.
func (s *VTScreen) topRowPlainLocked() string {
	text, _ := s.plainRowAtLocked(0)
	return text
}

// plainRowAtLocked returns the plain-text contents of row y plus the
// soft-wrap flag of its last cell.
func (s *VTScreen) plainRowAtLocked(y int) (string, bool) {
	var b strings.Builder
	b.Grow(s.cols)
	var lastMode int16
	for x := 0; x < s.cols; x++ {
		cell := s.term.Cell(x, y)
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		b.WriteRune(ch)
		if x == s.cols-1 {
			lastMode = cell.Mode
		}
	}
	return b.String(), lastMode&attrWrap != 0
}

// BufferLines returns the full scrollback plus the current screen rows as
// plain-text BufferLine entries in top-to-bottom order. Designed for mouse
// selection text extraction.
func (s *VTScreen) BufferLines() []BufferLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BufferLine, 0, len(s.plainSB)+s.rows)
	out = append(out, s.plainSB...)
	for y := 0; y < s.rows; y++ {
		text, soft := s.plainRowAtLocked(y)
		if !soft {
			text = strings.TrimRight(text, " ")
		}
		out = append(out, BufferLine{Text: text, SoftWrap: soft})
	}
	if n := len(out); n > 0 {
		out[n-1].SoftWrap = false
	}
	return out
}

// splitScrollChunks splits p into segments that each end immediately after a
// single scroll-trigger byte (\n, \v, \f). Trailing bytes without a trigger
// form the final chunk. Escape sequences are not split — they're rare enough
// in bulk output that we accept potentially missing scrollback from CSI SU/SD.
func splitScrollChunks(p []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '\n' || c == '\v' || c == '\f' {
			out = append(out, p[start:i+1])
			start = i + 1
		}
	}
	if start < len(p) {
		out = append(out, p[start:])
	}
	if len(out) == 0 {
		out = append(out, p)
	}
	return out
}

// Resize adjusts the terminal dimensions.
func (s *VTScreen) Resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols = cols
	s.rows = rows
	s.term.Resize(cols, rows)
}

// Render returns the current screen with ANSI SGR sequences preserved.
func (s *VTScreen) Render() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
	return s.renderScreenLocked(true)
}

// RenderWithScrollback returns scrollback lines followed by the current screen.
func (s *VTScreen) RenderWithScrollback() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false

	var b strings.Builder
	for _, line := range s.scrollback {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(s.renderScreenLocked(true))
	return b.String()
}

// renderScreenLocked renders every visible row with ANSI SGR. Rows are
// joined with '\n' but no trailing newline is emitted — appending one would
// inflate the rendered content to rows+1 lines and push the top row out of
// view inside a height-`rows` viewport.
func (s *VTScreen) renderScreenLocked(showCursor bool) string {
	var b strings.Builder
	cursor := s.term.Cursor()
	cursorVisible := showCursor && s.term.CursorVisible()
	for y := 0; y < s.rows; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		cx := -1
		if cursorVisible && cursor.Y == y {
			cx = cursor.X
		}
		b.WriteString(s.renderRowAtLocked(y, cx))
	}
	return b.String()
}

// renderRowAtLocked renders a single row from the live terminal state.
// cursorX < 0 disables cursor highlighting.
func (s *VTScreen) renderRowAtLocked(y, cursorX int) string {
	var b strings.Builder
	lastFG := vt10x.DefaultFG
	lastBG := vt10x.DefaultBG
	var lastMode int16
	for x := 0; x < s.cols; x++ {
		cell := s.term.Cell(x, y)
		// vt10x swaps fg/bg in setChar() when attrReverse is set but leaves
		// attrReverse on the cell. Swap them back before emitting SGR 7 so the
		// host terminal performs the reverse-video operation with its own palette.
		if cell.Mode&attrReverse != 0 {
			cell.FG, cell.BG = normalizeDefaultColor(cell.BG, true), normalizeDefaultColor(cell.FG, false)
		} else if cell.FG == vt10x.DefaultBG {
			cell.FG = vt10x.DefaultFG
		} else if cell.BG == vt10x.DefaultFG {
			cell.BG = vt10x.DefaultBG
		}
		if cursorX == x {
			cell.FG = vt10x.Black
			cell.BG = vt10x.White
		}
		if cell.FG != lastFG || cell.BG != lastBG || cell.Mode != lastMode {
			b.WriteString(sgr(cell.FG, cell.BG, cell.Mode))
			lastFG = cell.FG
			lastBG = cell.BG
			lastMode = cell.Mode
		}
		if cell.Char == 0 {
			b.WriteRune(' ')
		} else {
			b.WriteRune(cell.Char)
		}
	}
	if lastFG != vt10x.DefaultFG || lastBG != vt10x.DefaultBG || lastMode != 0 {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func normalizeDefaultColor(c vt10x.Color, foreground bool) vt10x.Color {
	if c == vt10x.DefaultFG || c == vt10x.DefaultBG {
		if foreground {
			return vt10x.DefaultFG
		}
		return vt10x.DefaultBG
	}
	return c
}

// vt10x glyph attribute bits (mirrored from the package's private constants).
const (
	attrReverse   int16 = 1
	attrUnderline int16 = 1 << 1
	attrBold      int16 = 1 << 2
	attrItalic    int16 = 1 << 4
	attrBlink     int16 = 1 << 5
	attrWrap      int16 = 1 << 6
)

// containsScrollTrigger reports whether the byte stream can plausibly cause
// the terminal to scroll the visible area upward. Without one of these triggers
// vt10x will not move content off the top row.
func containsScrollTrigger(p []byte) bool {
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '\n', '\v', '\f':
			return true
		case 0x1b: // ESC
			if i+1 >= len(p) {
				return true // assume unknown sequence may scroll
			}
			next := p[i+1]
			// ESC D (IND), ESC E (NEL), ESC M (RI — reverse index, scrolls down)
			if next == 'D' || next == 'E' || next == 'M' {
				return true
			}
			if next == '[' {
				// scan for CSI final byte; look for S (SU), T (SD), n;mr (DECSTBM)
				for j := i + 2; j < len(p); j++ {
					if p[j] >= 0x40 && p[j] <= 0x7e {
						if p[j] == 'S' || p[j] == 'T' {
							return true
						}
						break
					}
				}
			}
		}
	}
	return false
}

func sgr(fg, bg vt10x.Color, mode int16) string {
	parts := []string{"0"}
	if mode&attrBold != 0 {
		parts = append(parts, "1")
	}
	if mode&attrItalic != 0 {
		parts = append(parts, "3")
	}
	if mode&attrUnderline != 0 {
		parts = append(parts, "4")
	}
	if mode&attrBlink != 0 {
		parts = append(parts, "5")
	}
	if mode&attrReverse != 0 {
		parts = append(parts, "7")
	}
	if fg == vt10x.DefaultFG || fg == vt10x.DefaultBG {
		parts = append(parts, "39")
	} else {
		parts = append(parts, colorCode(fg, 30, 38))
	}
	if bg == vt10x.DefaultBG || bg == vt10x.DefaultFG {
		parts = append(parts, "49")
	} else {
		parts = append(parts, colorCode(bg, 40, 48))
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

func colorCode(c vt10x.Color, ansiBase, extendedBase int) string {
	if c < 8 {
		return fmt.Sprintf("%d", ansiBase+int(c))
	}
	if c < 16 {
		return fmt.Sprintf("%d", ansiBase+60+int(c)-8)
	}
	if c < 256 {
		return fmt.Sprintf("%d;5;%d", extendedBase, c)
	}
	return fmt.Sprintf("%d;2;%d;%d;%d", extendedBase, (c>>16)&0xff, (c>>8)&0xff, c&0xff)
}

// RawSnapshot returns a copy of the raw byte history for replaying to new clients.
func (s *VTScreen) RawSnapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make([]byte, len(s.rawHistory))
	copy(snap, s.rawHistory)
	return snap
}

// Dirty reports whether the screen has changed since the last Render.
func (s *VTScreen) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirty
}

func (s *VTScreen) AppCursorMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.Mode()&vt10x.ModeAppCursor != 0
}

// MouseMode reports what kind of mouse reporting the child process has
// enabled, if any. The returned booleans indicate:
//   - anyButton: button press/release should be reported (modes 1000/1002/1003)
//   - motion:    motion-with-button-pressed should be reported (mode 1002)
//   - anyMotion: any motion (even without a button) should be reported (mode 1003)
func (s *VTScreen) MouseMode() (anyButton, motion, anyMotion bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.term.Mode()
	if m&vt10x.ModeMouseButton != 0 {
		anyButton = true
	}
	if m&vt10x.ModeMouseMotion != 0 {
		anyButton = true
		motion = true
	}
	if m&vt10x.ModeMouseMany != 0 {
		anyButton = true
		motion = true
		anyMotion = true
	}
	if m&vt10x.ModeMouseX10 != 0 {
		anyButton = true
	}
	return
}
