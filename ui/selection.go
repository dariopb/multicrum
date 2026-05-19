package ui

import (
	"encoding/base64"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// selection tracks an in-progress or completed mouse selection over the
// focused session's buffer (scrollback + visible rows).
//
// Coordinates are buffer-relative: lineIdx is the index into VTScreen
// BufferLines (0 = oldest scrollback row), col is the rune column.
type selection struct {
	active   bool // mouse button is currently down
	startL   int
	startC   int
	endL     int
	endC     int
	hasRange bool // true once we've moved beyond the press point
}

func (sel selection) normalized() (sl, sc, el, ec int) {
	sl, sc, el, ec = sel.startL, sel.startC, sel.endL, sel.endC
	if sl > el || (sl == el && sc > ec) {
		sl, sc, el, ec = el, ec, sl, sc
	}
	return
}

// bufferRowFromMouse maps a mouse event Y inside the pane to a buffer line
// index, using the viewport's current YOffset.
func (s *state) bufferRowFromMouse(yInPane int) int {
	idx := s.manager.FocusedIndex()
	vp, ok := s.viewports[idx]
	if !ok {
		return -1
	}
	return vp.YOffset + yInPane
}

// startSelection begins a fresh selection at the given mouse coordinates
// (pane-relative). Returns true if the click was inside the pane.
func (s *state) startSelection(xInPane, yInPane int) {
	line := s.bufferRowFromMouse(yInPane)
	s.sel = selection{
		active:   true,
		startL:   line,
		startC:   xInPane,
		endL:     line,
		endC:     xInPane,
		hasRange: false,
	}
}

// updateSelection extends the current selection to the given mouse coords.
func (s *state) updateSelection(xInPane, yInPane int) {
	if !s.sel.active {
		return
	}
	line := s.bufferRowFromMouse(yInPane)
	s.sel.endL = line
	s.sel.endC = xInPane
	if s.sel.endL != s.sel.startL || s.sel.endC != s.sel.startC {
		s.sel.hasRange = true
	}
}

// finishSelection ends the drag, copies the selected text to the system
// clipboard via OSC 52, and returns a status message.
func (s *state) finishSelection() tea.Cmd {
	wasActive := s.sel.active
	hadRange := s.sel.hasRange
	s.sel.active = false
	if !wasActive || !hadRange {
		// Plain click without drag: clear selection.
		s.sel = selection{}
		return nil
	}
	text := s.selectionText()
	debugLog("finishSelection: active=%v hadRange=%v textLen=%d", wasActive, hadRange, len(text))
	if text == "" {
		s.sel = selection{}
		return nil
	}
	copyToClipboard(text)
	debugLog("copyToClipboard: done")
	return nil
}

// clearSelection drops any current selection (e.g. after content changes).
func (s *state) clearSelection() {
	s.sel = selection{}
}

// selectionText returns the selected text from the focused session, with
// soft-wrapped rows joined without a newline.
func (s *state) selectionText() string {
	sess := s.manager.Focused()
	if sess == nil {
		return ""
	}
	lines := sess.Screen().BufferLines()
	if len(lines) == 0 {
		return ""
	}
	sl, sc, el, ec := s.sel.normalized()
	if sl < 0 {
		sl = 0
	}
	if el >= len(lines) {
		el = len(lines) - 1
	}
	if sl > el {
		return ""
	}
	var b strings.Builder
	for row := sl; row <= el; row++ {
		runes := []rune(lines[row].Text)
		startX := 0
		endX := len(runes)
		if row == sl {
			startX = sc
		}
		if row == el {
			endX = ec + 1 // inclusive of cursor cell
		}
		if startX < 0 {
			startX = 0
		}
		if endX > len(runes) {
			endX = len(runes)
		}
		if endX < startX {
			endX = startX
		}
		b.WriteString(string(runes[startX:endX]))
		if row < el && !lines[row].SoftWrap {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// overlaySelection takes the rendered viewport content (ANSI-colored lines
// joined by \n) and replaces visible lines that intersect the selection with
// a plain-text version where the selected range is rendered inverse-video.
// Sacrificing per-line color on selected rows keeps the implementation simple
// and unambiguous; non-selected rows still show their original colors.
func (s *state) overlaySelection(pane string, paneCols, paneRows int) string {
	if !s.sel.active && !s.sel.hasRange {
		return pane
	}
	if !s.sel.hasRange {
		return pane
	}
	sess := s.manager.Focused()
	if sess == nil {
		return pane
	}
	lines := sess.Screen().BufferLines()
	sl, sc, el, ec := s.sel.normalized()
	idx := s.manager.FocusedIndex()
	vp, ok := s.viewports[idx]
	if !ok {
		return pane
	}
	yoff := vp.YOffset
	paneLines := strings.Split(pane, "\n")
	for i := 0; i < len(paneLines) && i < paneRows; i++ {
		row := yoff + i
		if row < sl || row > el {
			continue
		}
		if row < 0 || row >= len(lines) {
			continue
		}
		text := lines[row].Text
		runes := []rune(text)
		// Pad to paneCols so selection beyond end-of-text is visible.
		if len(runes) < paneCols {
			runes = append(runes, []rune(strings.Repeat(" ", paneCols-len(runes)))...)
		}
		selStart := 0
		selEnd := len(runes)
		if row == sl {
			selStart = sc
		}
		if row == el {
			selEnd = ec + 1
		}
		if selStart < 0 {
			selStart = 0
		}
		if selEnd > len(runes) {
			selEnd = len(runes)
		}
		if selEnd < selStart {
			selEnd = selStart
		}
		var b strings.Builder
		b.WriteString(string(runes[:selStart]))
		b.WriteString("\x1b[7m")
		b.WriteString(string(runes[selStart:selEnd]))
		b.WriteString("\x1b[0m")
		b.WriteString(string(runes[selEnd:]))
		paneLines[i] = b.String()
	}
	return strings.Join(paneLines, "\n")
}

// buildOSC52 returns the OSC 52 escape sequence that asks the terminal
// (or tmux, when set-clipboard is on/external) to set the system clipboard.
//
// We intentionally do NOT wrap in a tmux DCS passthrough here: with
// set-clipboard=on|external, tmux itself listens for OSC 52 from inner
// programs and forwards to the outer terminal — the passthrough wrapper
// only helps when set-clipboard=off, which also requires allow-passthrough=on.
// The plain sequence is the right thing in nearly all configurations.
func buildOSC52(text string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	return "\x1b]52;c;" + enc + "\x07"
}
