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
	rawHistory []byte // raw PTY bytes capped at maxScrollback, for WS replay
	reply      atomic.Pointer[io.Writer]
	replyOnce  sync.Once
	replies    atomic.Bool

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

// BufferLine is one physical row of the terminal as plain text plus whether
// the row is soft-wrapped. The UI uses these to extract selection text with
// correct line breaks.
type BufferLine struct {
	Text     string
	SoftWrap bool
}

// NewVTScreen creates a VT screen of given dimensions.
func NewVTScreen(cols, rows int) *VTScreen {
	e := vt.NewEmulator(cols, rows)
	e.SetScrollbackSize(maxScrollbackLines)
	s := &VTScreen{cols: cols, rows: rows, term: e, cursorVisible: true, cursorShape: vt.CursorBlock, cursorBlink: true}
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

	s.rawHistory = append(s.rawHistory, p...)
	if len(s.rawHistory) > maxScrollback {
		s.rawHistory = s.rawHistory[len(s.rawHistory)-maxScrollback:]
	}

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
	return s.term.Render()
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
	if sb := s.term.Scrollback(); sb != nil {
		lines := sb.Lines()
		for _, line := range lines {
			b.WriteString(line.Render())
			b.WriteByte('\n')
		}
	}
	b.WriteString(s.term.Render())
	return b.String()
}

// BufferLines returns the full scrollback plus the current screen rows as
// plain-text BufferLine entries in top-to-bottom order. Designed for mouse
// selection text extraction.
func (s *VTScreen) BufferLines() []BufferLine {
	s.mu.Lock()
	defer s.mu.Unlock()

	alt := s.term.IsAltScreen()
	sbLen := 0
	if !alt {
		sbLen = s.term.ScrollbackLen()
	}
	out := make([]BufferLine, 0, sbLen+s.rows)
	if !alt {
		if sb := s.term.Scrollback(); sb != nil {
			for _, line := range sb.Lines() {
				out = append(out, BufferLine{Text: line.String()})
			}
		}
	}
	for y := 0; y < s.rows; y++ {
		out = append(out, BufferLine{Text: strings.TrimRight(s.plainRowAtLocked(y), " ")})
	}
	if n := len(out); n > 0 {
		out[n-1].SoftWrap = false
	}
	return out
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
