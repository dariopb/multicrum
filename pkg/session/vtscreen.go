package session

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

const (
	maxScrollback      = 256 * 1024 // raw byte cap for WS replay
	maxScrollbackLines = 10000      // visible line cap for local scrollback
)

// VTScreen maintains a virtual terminal screen buffer using charmbracelet/x/vt.
type VTScreen struct {
	mu         sync.Mutex
	term       *vt.Emulator
	cols       int
	rows       int
	dirty      bool
	rawHistory byteRing // raw PTY bytes capped at maxScrollback, for WS replay

	logicalScrollback []logicalLine
	pendingLines      []logicalLine
	pendingRows       int
	linePlain         strings.Builder
	lineANSI          strings.Builder
	pendingControl    []byte
	pendingCR         bool
	pendingCRANSI     []byte
	observedLines     []string
	observedPlain     strings.Builder
	observedControl   []byte
	observedCR        bool

	reply     atomic.Pointer[io.Writer]
	replyOnce sync.Once
	replies   atomic.Bool

	appCursor     bool
	mouseX10      bool
	mouseNormal   bool
	mouseButton   bool
	mouseAny      bool
	mouseSGR      bool
	bracketPaste  bool
	cursorVisible bool
	cursorShape   vt.CursorStyle
	cursorBlink   bool
}

type CursorInfo struct {
	X       int
	Y       int
	Visible bool
	Shape   vt.CursorStyle
	Blink   bool
}

// BufferLine is one rendered row of the terminal as plain text plus whether
// the row is soft-wrapped. The UI uses these to extract selection text with
// correct line breaks.
type BufferLine struct {
	Text      string
	SoftWrap  bool
	WrapKnown bool
}

type logicalLine struct {
	ANSI  string
	Plain string
}

// byteRing keeps the replay tail without reallocating and copying the full
// 256 KiB history on every PTY write after the cap is reached.
type byteRing struct {
	buf   []byte
	start int
}

func (r *byteRing) append(p []byte) {
	if len(p) == 0 {
		return
	}
	if len(p) >= maxScrollback {
		if cap(r.buf) < maxScrollback {
			r.buf = make([]byte, maxScrollback)
		} else {
			r.buf = r.buf[:maxScrollback]
		}
		copy(r.buf, p[len(p)-maxScrollback:])
		r.start = 0
		return
	}
	if len(r.buf) < maxScrollback {
		if len(r.buf)+len(p) <= maxScrollback {
			r.buf = append(r.buf, p...)
			return
		}
		keep := maxScrollback - len(p)
		next := make([]byte, maxScrollback)
		copy(next, r.buf[len(r.buf)-keep:])
		copy(next[keep:], p)
		r.buf = next
		r.start = 0
		return
	}

	first := copy(r.buf[r.start:], p)
	copy(r.buf, p[first:])
	r.start = (r.start + len(p)) % maxScrollback
}

func (r *byteRing) len() int {
	return len(r.buf)
}

func (r *byteRing) bytes() []byte {
	out := make([]byte, len(r.buf))
	if r.start == 0 || len(r.buf) < maxScrollback {
		copy(out, r.buf)
		return out
	}
	n := copy(out, r.buf[r.start:])
	copy(out[n:], r.buf[:r.start])
	return out
}

// NewVTScreen creates a VT screen of given dimensions.
func NewVTScreen(cols, rows int) *VTScreen {
	e := vt.NewEmulator(cols, rows)
	// VTScreen owns semantic scrollback in logicalScrollback. Keeping another
	// 80,000-line cell buffer inside the emulator made every line after the
	// buffer filled shift a huge slice, dominating continuous-output workloads.
	// The emulator API cannot disable scrollback entirely, so retain one line.
	e.SetScrollbackSize(1)
	s := &VTScreen{cols: cols, rows: rows, term: e, cursorVisible: true, cursorShape: vt.CursorBlock}
	e.SetCallbacks(vt.Callbacks{
		EnableMode:       func(mode ansi.Mode) { s.setModeLocked(mode, true) },
		DisableMode:      func(mode ansi.Mode) { s.setModeLocked(mode, false) },
		CursorVisibility: func(visible bool) { s.cursorVisible = visible },
		CursorStyle:      func(style vt.CursorStyle, blink bool) { s.setCursorStyleLocked(style, blink) },
	})
	return s
}

func (s *VTScreen) setModeLocked(mode ansi.Mode, enabled bool) {
	switch mode {
	case ansi.ModeCursorKeys:
		s.appCursor = enabled
	case ansi.ModeMouseX10:
		s.mouseX10 = enabled
	case ansi.ModeMouseNormal:
		s.mouseNormal = enabled
	case ansi.ModeMouseButtonEvent:
		s.mouseButton = enabled
	case ansi.ModeMouseAnyEvent:
		s.mouseAny = enabled
	case ansi.ModeMouseExtSgr:
		s.mouseSGR = enabled
	case ansi.ModeBracketedPaste:
		s.bracketPaste = enabled
	}
}

func (s *VTScreen) setCursorStyleLocked(style vt.CursorStyle, blink bool) {
	s.cursorShape = style
	s.cursorBlink = blink
}

// SetReplyWriter installs the writer that receives terminal-generated replies
// such as CPR/DSR responses. Apps like fzf, less, and ranger issue ESC[6n and
// wait for a reply.
func (s *VTScreen) SetReplyWriter(w io.Writer) {
	s.reply.Store(&w)
	s.replyOnce.Do(func() {
		go s.replyLoop()
	})
}

func (s *VTScreen) replyLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.term.Read(buf)
		if n > 0 && s.replies.Load() {
			if wp := s.reply.Load(); wp != nil && *wp != nil {
				out := make([]byte, n)
				copy(out, buf[:n])
				_, _ = (*wp).Write(out)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *VTScreen) SetTerminalReplies(enabled bool) {
	s.replies.Store(enabled)
}

// Write feeds raw PTY bytes into the VT parser.
func (s *VTScreen) Write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rawHistory.append(p)

	s.captureObservedLines(p)
	s.captureLogicalLines(p)
	_, _ = s.term.Write(translateSCORC(p))
	s.dirty = true
}

// translateSCORC rewrites SCORC (CSI u, "ESC [ u" — Restore Cursor Position)
// to DECRC ("ESC 8"). The vt emulator we use (github.com/charmbracelet/x/vt)
// registers a handler for DECRC but none for SCORC, so apps that draw popups
// with the ESC[s / ESC[u pair (notably btop's kill/signal confirmation
// dialog) end up writing every line after the first save at the wrong
// position because ESC[u silently no-ops. Translating to DECRC fixes the
// dialog without touching the upstream emulator.
//
// The browser replay buffer (rawHistory) keeps the original bytes — xterm.js
// handles SCORC natively, so no rewrite is needed there.
//
// Only the canonical 3-byte SCORC ("ESC [ u" with no intermediate or
// parameter bytes) is rewritten. Any "ESC [ <params> u" form is left alone
// because CSI u with parameters is the kitty keyboard protocol report, not
// SCORC, and rewriting it would corrupt input reports.
func translateSCORC(p []byte) []byte {
	// Fast path: no ESC at all.
	if !containsESC(p) {
		return p
	}
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if i+2 < len(p) && p[i] == 0x1b && p[i+1] == '[' && p[i+2] == 'u' {
			out = append(out, 0x1b, '8')
			i += 2
			continue
		}
		out = append(out, p[i])
	}
	return out
}

func containsESC(p []byte) bool {
	for _, b := range p {
		if b == 0x1b {
			return true
		}
	}
	return false
}

func (s *VTScreen) captureObservedLines(p []byte) {
	for _, b := range p {
		if len(s.observedControl) > 0 {
			s.observedControl = append(s.observedControl, b)
			if ansiSequenceComplete(s.observedControl) {
				s.observedControl = nil
			}
			continue
		}
		if s.observedCR {
			switch b {
			case '\n':
				s.observedCR = false
				s.finishObservedLine()
				continue
			case '\r':
				continue
			case 0x1b:
				s.observedControl = append(s.observedControl[:0], b)
				continue
			}
			s.observedCR = false
			s.observedPlain.Reset()
		}
		switch b {
		case 0x1b:
			s.observedControl = append(s.observedControl[:0], b)
		case '\r':
			s.observedCR = true
		case '\n':
			s.finishObservedLine()
		case '\b':
			trimLastRune(&s.observedPlain)
		case '\t':
			s.observedPlain.WriteByte('\t')
		default:
			if b >= 0x20 || b >= 0x80 {
				s.observedPlain.WriteByte(b)
			}
		}
	}
}

func (s *VTScreen) finishObservedLine() {
	if line := s.observedPlain.String(); line != "" {
		s.observedLines = append(s.observedLines, line)
		if overflow := len(s.observedLines) - 256; overflow > 0 {
			s.observedLines = s.observedLines[overflow:]
		}
	}
	s.observedPlain.Reset()
}

func (s *VTScreen) captureLogicalLines(p []byte) {
	for _, b := range p {
		if len(s.pendingControl) > 0 {
			s.pendingControl = append(s.pendingControl, b)
			if ansiSequenceComplete(s.pendingControl) {
				if s.pendingCR && isSGR(s.pendingControl) {
					s.pendingCRANSI = append(s.pendingCRANSI, s.pendingControl...)
				} else {
					if s.pendingCR {
						s.pendingCR = false
						s.pendingCRANSI = nil
						s.linePlain.Reset()
						s.lineANSI.Reset()
					}
					s.captureControlSequence(s.pendingControl)
				}
				s.pendingControl = nil
			}
			continue
		}
		// Resolve a deferred carriage return. A CR immediately followed by LF
		// is a single CRLF line break (the common PTY line ending produced by
		// the terminal's ONLCR translation). A *lone* CR instead returns the
		// cursor to column 0 so the bytes that follow overwrite the current
		// line in place — this is how shells redraw their prompt and how
		// progress bars/spinners update. Treating a lone CR as a line break
		// (the previous behavior) committed a fresh logical line on every
		// prompt redraw, so scrolled-back output filled with duplicated,
		// blank-looking prompt lines that were absent from the live screen.
		if s.pendingCR {
			switch b {
			case '\n':
				s.pendingCR = false
				s.pendingCRANSI = nil
				s.finishLogicalLine()
				continue
			case '\r':
				s.pendingCRANSI = nil
				continue
			case 0x1b:
				s.pendingControl = append(s.pendingControl[:0], b)
				continue
			}
			// Lone CR: overwrite the current (still-uncommitted) line.
			s.pendingCR = false
			s.linePlain.Reset()
			s.lineANSI.Reset()
			s.lineANSI.Write(s.pendingCRANSI)
			s.pendingCRANSI = nil
		}
		switch b {
		case 0x1b:
			s.pendingControl = append(s.pendingControl[:0], b)
		case '\r':
			s.pendingCR = true
			s.pendingCRANSI = nil
		case '\n':
			s.finishLogicalLine()
		case '\b':
			trimLastRune(&s.linePlain)
			trimLastRune(&s.lineANSI)
		case '\t':
			s.linePlain.WriteByte('\t')
			s.lineANSI.WriteByte('\t')
		default:
			if b >= 0x20 || b >= 0x80 {
				s.linePlain.WriteByte(b)
				s.lineANSI.WriteByte(b)
			}
		}
	}
}

func ansiSequenceComplete(seq []byte) bool {
	if len(seq) == 0 || seq[0] != 0x1b {
		return true
	}
	if len(seq) == 1 {
		return false
	}
	switch seq[1] {
	case '[':
		if len(seq) < 3 {
			return false
		}
		last := seq[len(seq)-1]
		return last >= 0x40 && last <= 0x7e
	case ']':
		last := seq[len(seq)-1]
		return last == 0x07 || (len(seq) >= 3 && seq[len(seq)-2] == 0x1b && last == '\\')
	default:
		return true
	}
}

func (s *VTScreen) captureControlSequence(seq []byte) {
	switch {
	case isSGR(seq):
		s.lineANSI.Write(seq)
	case isEraseDisplay(seq, '2'):
		s.clearLogicalScreen()
	case isEraseDisplay(seq, '3'):
		s.logicalScrollback = nil
		s.observedLines = nil
	case isRIS(seq):
		s.logicalScrollback = nil
		s.clearLogicalScreen()
	}
}

func isSGR(seq []byte) bool {
	return len(seq) >= 3 && seq[0] == 0x1b && seq[1] == '[' && seq[len(seq)-1] == 'm'
}

func isEraseDisplay(seq []byte, mode byte) bool {
	return len(seq) == 4 &&
		seq[0] == 0x1b &&
		seq[1] == '[' &&
		seq[2] == mode &&
		seq[3] == 'J'
}

func isRIS(seq []byte) bool {
	return len(seq) == 2 && seq[0] == 0x1b && seq[1] == 'c'
}

func (s *VTScreen) clearLogicalScreen() {
	s.pendingLines = nil
	s.pendingRows = 0
	s.linePlain.Reset()
	s.lineANSI.Reset()
	s.pendingCR = false
	s.pendingCRANSI = nil
	s.observedLines = nil
	s.observedPlain.Reset()
	s.observedCR = false
}

func (s *VTScreen) finishLogicalLine() {
	line := logicalLine{ANSI: s.lineANSI.String(), Plain: s.linePlain.String()}
	s.pendingLines = append(s.pendingLines, line)
	s.pendingRows += wrappedLineCount(line.Plain, s.cols)
	s.linePlain.Reset()
	s.lineANSI.Reset()
	s.flushPendingLogicalLines()
}

func (s *VTScreen) flushPendingLogicalLines() {
	for s.pendingRows > s.rows && len(s.pendingLines) > 0 {
		line := s.pendingLines[0]
		s.appendLogicalScrollback(line)
		s.pendingLines = s.pendingLines[1:]
		s.pendingRows -= wrappedLineCount(line.Plain, s.cols)
	}
}

func (s *VTScreen) recalcPendingRows() {
	s.pendingRows = 0
	for _, line := range s.pendingLines {
		s.pendingRows += wrappedLineCount(line.Plain, s.cols)
	}
}

func (s *VTScreen) appendLogicalScrollback(line logicalLine) {
	s.logicalScrollback = append(s.logicalScrollback, line)
	if overflow := len(s.logicalScrollback) - maxScrollbackLines; overflow > 0 {
		s.logicalScrollback = s.logicalScrollback[overflow:]
	}
}

func wrappedLineCount(text string, width int) int {
	if width <= 0 {
		return 1
	}
	n := len([]rune(text))
	if n == 0 {
		return 1
	}
	return (n + width - 1) / width
}

func appendWrappedBufferLines(out []BufferLine, text string, width int) []BufferLine {
	runes := []rune(text)
	if width <= 0 || len(runes) <= width {
		return append(out, BufferLine{Text: text, WrapKnown: true})
	}
	for len(runes) > width {
		out = append(out, BufferLine{Text: string(runes[:width]), SoftWrap: true, WrapKnown: true})
		runes = runes[width:]
	}
	return append(out, BufferLine{Text: string(runes), WrapKnown: true})
}

func trimLastRune(b *strings.Builder) {
	s := b.String()
	if s == "" {
		return
	}
	r := []rune(s)
	b.Reset()
	b.WriteString(string(r[:len(r)-1]))
}

func (s *VTScreen) writeLogicalRender(b *strings.Builder, includeScrollback bool) {
	if includeScrollback {
		for _, line := range s.logicalScrollback {
			b.WriteString(line.ANSI)
			b.WriteByte('\n')
		}
	}
	for _, line := range s.pendingLines {
		b.WriteString(line.ANSI)
		b.WriteByte('\n')
	}
	currentRows := s.pendingRows + 1
	if current := s.lineANSI.String(); current != "" {
		b.WriteString(current)
		currentRows = s.pendingRows + wrappedLineCount(s.linePlain.String(), s.cols)
	}
	for currentRows < s.rows {
		b.WriteByte('\n')
		currentRows++
	}
}

// Resize adjusts the terminal dimensions.
func (s *VTScreen) Resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cols <= 0 || rows <= 0 {
		return
	}
	changed := cols != s.cols || rows != s.rows
	s.cols = cols
	s.rows = rows
	s.recalcPendingRows()
	s.flushPendingLogicalLines()
	s.term.Resize(cols, rows)
	if changed {
		s.reflowEmulatorLocked(cols, rows)
	}
	s.dirty = true
}

// reflowEmulatorLocked rebuilds the visible emulator screen by replaying the
// raw PTY history at the new dimensions.
//
// The underlying vt emulator does not reflow: shrinking the width crops the
// right edge of every row and that content is lost, so a later widen leaves
// blank cells where the cropped characters used to be. Replaying the raw byte
// stream re-wraps every line at the current width, which restores characters
// dropped by an earlier shrink and reflows logical lines to fit — matching how
// a real terminal behaves.
//
// Skipped while the child is on the alternate screen: alt-screen apps (vim,
// btop, less) own an exact rows x cols grid and redraw themselves on the
// SIGWINCH that accompanies a resize, so replaying their old absolute-coordinate
// drawing at a new width would only flash a garbage frame before their redraw.
func (s *VTScreen) reflowEmulatorLocked(cols, rows int) {
	if s.rawHistory.len() == 0 || s.term.IsAltScreen() {
		return
	}
	// Render() only shows the visible rows x cols screen, so we only need to
	// replay enough of the tail to reconstruct it. Replaying the full history
	// (capped at 256 KiB) on every resize tick was far too slow. Start the
	// replay just after a newline boundary that leaves at least a screenful of
	// logical lines ahead of it — each logical line yields >= 1 physical row,
	// so rows+pad newlines guarantee the visible screen fills without leaving
	// blank ghost rows at the top.
	tail := reflowTail(s.rawHistory.bytes(), rows)

	// Suppress terminal replies while replaying: the history may contain DSR/CPR
	// queries whose responses would otherwise be forwarded to the child as if it
	// had just asked for them.
	prevReplies := s.replies.Swap(false)
	s.term.ClearScrollback()
	// RIS (ESC c) fully resets the emulator so the replay reconstructs state
	// from scratch instead of appending to the cropped screen.
	_, _ = s.term.Write([]byte{0x1b, 'c'})
	s.term.Resize(cols, rows)
	_, _ = s.term.Write(translateSCORC(tail))
	s.replies.Store(prevReplies)
}

// reflowTail returns the suffix of raw history to replay so the visible screen
// can be reconstructed cheaply. It walks backwards to a newline boundary that
// keeps at least rows+reflowPadRows logical lines ahead of it, bounded by
// reflowMaxTail bytes so pathological escape-heavy streams can't blow up cost.
func reflowTail(raw []byte, rows int) []byte {
	const (
		reflowPadRows = 8
		reflowMaxTail = 96 * 1024
	)
	wantLines := rows + reflowPadRows
	limit := len(raw)
	if limit > reflowMaxTail {
		limit = reflowMaxTail
	}
	start := len(raw)
	lines := 0
	hitCap := true
	for i := len(raw) - 1; i >= len(raw)-limit; i-- {
		if raw[i] == '\n' {
			lines++
			if lines >= wantLines {
				start = i + 1
				hitCap = false
				break
			}
		}
		start = i
	}
	tail := raw[start:]
	// When the byte cap cut us off mid-stream, trim forward to the first
	// newline so replay doesn't begin in the middle of a line or escape.
	if hitCap && len(raw) > limit {
		if nl := indexByte(tail, '\n'); nl >= 0 && nl+1 < len(tail) {
			tail = tail[nl+1:]
		}
	}
	return tail
}

func indexByte(p []byte, b byte) int {
	for i := range p {
		if p[i] == b {
			return i
		}
	}
	return -1
}

// Render returns the current screen with ANSI SGR sequences preserved.
func (s *VTScreen) Render() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
	return s.term.Render()
}

// RenderSnapshot returns the ANSI screen and matching plain rows/wrap metadata
// from the same emulator state. Selection must not combine a previously
// painted frame with metadata read after newer PTY output arrives.
func (s *VTScreen) RenderSnapshot() (string, []BufferLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
	return s.term.Render(), s.visibleLinesLocked()
}

// RenderWithScrollback returns scrollback lines followed by the current screen.
// Scrollback is intentionally omitted while the child is on the alternate
// screen (e.g. btop, vim, less): alt-screen apps draw at absolute coordinates
// against an exact rows x cols grid, so prepending main-screen scrollback
// would shift every popup/dialog out of place.
func (s *VTScreen) RenderWithScrollback() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false

	if s.term.IsAltScreen() {
		return s.term.Render()
	}

	var b strings.Builder
	s.writeLogicalRender(&b, true)
	if b.Len() == 0 {
		b.WriteString(s.term.Render())
	}
	return b.String()
}

// BufferLines returns the full scrollback plus the current screen rows as
// plain-text BufferLine entries in top-to-bottom order. Designed for mouse
// selection text extraction.
func (s *VTScreen) BufferLines() []BufferLine {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]BufferLine, 0, len(s.logicalScrollback)+len(s.pendingLines)+s.rows)
	if !s.term.IsAltScreen() {
		for _, line := range s.logicalScrollback {
			out = appendWrappedBufferLines(out, line.Plain, s.cols)
		}
		for _, line := range s.pendingLines {
			out = appendWrappedBufferLines(out, line.Plain, s.cols)
		}
		if current := s.linePlain.String(); current != "" {
			out = appendWrappedBufferLines(out, current, s.cols)
		}
	}
	if len(out) < s.rows {
		start := 0
		if len(out) > 0 {
			start = len(out)
		}
		for y := start; y < s.rows; y++ {
			out = append(out, BufferLine{Text: strings.TrimRight(s.plainRowAtLocked(y), " ")})
		}
	}
	if len(out) == 0 {
		for y := 0; y < s.rows; y++ {
			out = append(out, BufferLine{Text: strings.TrimRight(s.plainRowAtLocked(y), " ")})
		}
	}
	if n := len(out); n > 0 {
		out[n-1].SoftWrap = false
	}
	return out
}

// VisibleLines returns the current on-screen rows as plain-text BufferLine
// entries, one per emulator row (0 = top of the visible screen). This is the
// exact set of lines that Render() paints, so it is the correct source for
// live-mode mouse selection: unlike BufferLines() (which is the *logical*
// scrollback and only coincides with the visible screen when output is
// scrolling at the bottom), VisibleLines() always matches what the user sees —
// e.g. immediately after `clear`, when the screen is a fresh top-aligned frame
// with blank padding below the cursor rather than the tail of the logical
// buffer. SoftWrap is reconstructed from the bounded set of pending logical
// lines when their wrapped rows still match the visible grid. Rows changed by
// cursor-addressed applications remain hard boundaries because their logical
// origin cannot be determined safely.
func (s *VTScreen) VisibleLines() []BufferLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.visibleLinesLocked()
}

func (s *VTScreen) visibleLinesLocked() []BufferLine {
	out := make([]BufferLine, 0, s.rows)
	for y := 0; y < s.rows; y++ {
		out = append(out, BufferLine{Text: strings.TrimRight(s.plainRowAtLocked(y), " ")})
	}
	logical := make([]BufferLine, 0, s.pendingRows+s.rows)
	logicalSources := make([]string, 0, len(s.pendingLines)+s.rows+1)
	scrollbackStart := len(s.logicalScrollback)
	scrollbackRows := 0
	for scrollbackStart > 0 && scrollbackRows < s.rows*2 {
		scrollbackStart--
		scrollbackRows += wrappedLineCount(s.logicalScrollback[scrollbackStart].Plain, s.cols)
	}
	for _, line := range s.logicalScrollback[scrollbackStart:] {
		logical = appendWrappedBufferLines(logical, line.Plain, s.cols)
		logicalSources = append(logicalSources, line.Plain)
	}
	for _, line := range s.pendingLines {
		logical = appendWrappedBufferLines(logical, line.Plain, s.cols)
		logicalSources = append(logicalSources, line.Plain)
	}
	if current := s.linePlain.String(); current != "" {
		logical = appendWrappedBufferLines(logical, current, s.cols)
		logicalSources = append(logicalSources, current)
	}
	observedStart := len(s.observedLines) - s.rows*2
	if observedStart < 0 {
		observedStart = 0
	}
	logicalSources = append(logicalSources, s.observedLines[observedStart:]...)
	if current := s.observedPlain.String(); current != "" {
		logicalSources = append(logicalSources, current)
	}
	if len(logical) > s.rows*2 {
		logical = logical[len(logical)-s.rows*2:]
	}
	applyMatchingWrapMetadata(out, logical)
	applyAdjacentWrapMetadata(out, logicalSources, s.cols)
	for i := 0; i+1 < len(out); i++ {
		if !out[i].WrapKnown && ansi.StringWidth(out[i].Text) >= s.cols {
			out[i].SoftWrap = true
		}
	}
	return out
}

func applyAdjacentWrapMetadata(visible []BufferLine, logical []string, width int) {
	if width <= 0 {
		return
	}
	for i := 0; i+1 < len(visible); i++ {
		if visible[i].WrapKnown || visible[i].Text == "" || visible[i+1].Text == "" {
			continue
		}
		padding := width - ansi.StringWidth(visible[i].Text)
		if padding <= 0 {
			continue
		}
		joined := visible[i].Text + strings.Repeat(" ", padding) + visible[i+1].Text
		for _, source := range logical {
			if strings.Contains(source, joined) {
				visible[i].Text += strings.Repeat(" ", padding)
				visible[i].SoftWrap = true
				visible[i].WrapKnown = true
				break
			}
		}
	}
}

func applyMatchingWrapMetadata(visible, logical []BufferLine) {
	bestVisible, bestLogical, bestLen := 0, 0, 0
	for vi := range visible {
		for li := range logical {
			n := 0
			for vi+n < len(visible) && li+n < len(logical) &&
				visible[vi+n].Text == strings.TrimRight(logical[li+n].Text, " ") {
				n++
			}
			if n > bestLen {
				bestVisible, bestLogical, bestLen = vi, li, n
			}
		}
	}
	for i := 0; i < bestLen; i++ {
		logicalLine := logical[bestLogical+i]
		visible[bestVisible+i].SoftWrap = logicalLine.SoftWrap
		visible[bestVisible+i].WrapKnown = true
		// A space in the terminal's last column is visually blank and is
		// trimmed from the cell snapshot, but it remains selected content
		// regardless of whether the following boundary is soft or CR/LF.
		trimmed := strings.TrimRight(logicalLine.Text, " ")
		if trailing := len(logicalLine.Text) - len(trimmed); trailing > 0 {
			visible[bestVisible+i].Text += strings.Repeat(" ", trailing)
		}
	}
}

func (s *VTScreen) plainRowAtLocked(y int) string {
	var b strings.Builder
	b.Grow(s.cols)
	for x := 0; x < s.cols; x++ {
		cell := s.term.CellAt(x, y)
		b.WriteString(cellText(cell))
	}
	return b.String()
}

func cellText(cell *uv.Cell) string {
	if cell == nil || cell.IsZero() {
		return " "
	}
	if cell.Content == "" {
		return " "
	}
	return cell.Content
}

// RawSnapshot returns a copy of the raw byte history for replaying to new clients.
func (s *VTScreen) RawSnapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rawHistory.bytes()
}

// Dirty reports whether the screen has changed since the last Render.
func (s *VTScreen) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirty
}

func (s *VTScreen) Cursor() CursorInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := s.term.CursorPosition()
	return CursorInfo{X: pos.X, Y: pos.Y, Visible: s.cursorVisible, Shape: s.cursorShape, Blink: s.cursorBlink}
}

func (s *VTScreen) AppCursorMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appCursor
}

// BracketedPasteMode reports whether the child process has enabled DEC mode
// 2004 (bracketed paste). When true, pasted text fed to the PTY should be
// wrapped in ESC[200~...ESC[201~ so the child can distinguish it from typed
// input.
func (s *VTScreen) BracketedPasteMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bracketPaste
}

// IsAltScreen reports whether the child terminal is currently on the
// alternate screen (DEC mode 1049/47/1047). Callers use this to suppress
// scrollback presentation and to reset viewport scroll state on transitions.
func (s *VTScreen) IsAltScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.IsAltScreen()
}

// MouseMode reports what kind of mouse reporting the child process has
// enabled, if any. The returned booleans indicate:
//   - anyButton: button press/release should be reported (modes 1000/1002/1003)
//   - motion:    motion-with-button-pressed should be reported (mode 1002)
//   - anyMotion: any motion (even without a button) should be reported (mode 1003)
func (s *VTScreen) MouseMode() (anyButton, motion, anyMotion bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	anyButton = s.mouseX10 || s.mouseNormal || s.mouseButton || s.mouseAny
	motion = s.mouseButton || s.mouseAny
	anyMotion = s.mouseAny
	return
}
