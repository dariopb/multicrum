package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ANSI SGR styles for search-match highlighting in the pane overlay.
const (
	searchMatchSGR   = "\x1b[30;43m" // black on yellow: all matches
	searchCurrentSGR = "\x1b[30;46m" // black on cyan: the focused match
)

// searchHit is a single occurrence of the search query in the focused
// session's buffer, addressed by BufferLines index and rune column.
type searchHit struct {
	line int
	col  int
}

// scrollSearch holds the state of an in-progress or committed scrollback
// search. It backs two vi-style prompts available while scrolled into the
// buffer: '/' text search (with n/N to move between matches) and ':' line
// jump.
type scrollSearch struct {
	input    string      // text typed into the currently open prompt
	lineJump bool        // ':' line-number prompt (vs '/' text search)
	query    string      // last committed search text, for n/N navigation
	hits     []searchHit // all matches for query, in buffer order
	cur      int         // index into hits of the focused match
}

// clearSearch drops any committed search state (matches + query). The active
// input prompt, if open, is handled separately by the mode transition.
func (s *state) clearSearch() {
	s.search = scrollSearch{}
}

// openScrollSearch opens the '/' (text) or ':' (line jump) prompt.
func (s *state) openScrollSearch(lineJump bool) {
	s.search.input = ""
	s.search.lineJump = lineJump
	s.mode = modeScrollSearch
	s.statusMsg = ""
}

// cancelScrollSearch closes the prompt without running it.
func (s *state) cancelScrollSearch() {
	s.search.input = ""
	s.mode = modeNormal
}

// commitScrollSearch runs the prompt's action (line jump or text search) and
// returns to normal mode.
func (s *state) commitScrollSearch() {
	in := s.search.input
	lineJump := s.search.lineJump
	s.search.input = ""
	s.mode = modeNormal
	if lineJump {
		s.jumpToLine(in)
		return
	}
	if strings.TrimSpace(in) == "" {
		return
	}
	s.runSearch(in)
}

// jumpToLine scrolls so the given 1-based buffer line is visible.
func (s *state) jumpToLine(in string) {
	n, err := strconv.Atoi(strings.TrimSpace(in))
	if err != nil {
		s.statusMsg = "invalid line: " + in
		return
	}
	sess := s.manager.Focused()
	if sess == nil {
		return
	}
	total := len(sess.Screen().BufferLines())
	if total == 0 {
		return
	}
	line := n - 1
	if line < 0 {
		line = 0
	}
	if line >= total {
		line = total - 1
	}
	s.scrollToBufferLine(line)
	s.statusMsg = fmt.Sprintf("line %d/%d", line+1, total)
}

// runSearch computes all matches for query and jumps to the first one at or
// after the current scroll position (wrapping to the top otherwise).
func (s *state) runSearch(query string) {
	s.search.query = query
	s.search.hits = s.computeHits(query)
	s.search.cur = 0
	if len(s.search.hits) == 0 {
		s.statusMsg = "no match: " + query
		return
	}
	s.search.cur = s.nearestHitForward()
	s.gotoCurrentHit()
}

// computeHits returns every case-insensitive occurrence of query across the
// focused session's buffer lines. Columns are rune indices.
func (s *state) computeHits(query string) []searchHit {
	sess := s.manager.Focused()
	if sess == nil {
		return nil
	}
	bl := sess.Screen().BufferLines()
	texts := make([]string, len(bl))
	for i := range bl {
		texts[i] = bl[i].Text
	}
	return findHits(texts, query)
}

// findHits scans lines for every case-insensitive occurrence of query,
// reporting rune-column positions. Exposed as a pure function for testing.
func findHits(lines []string, query string) []searchHit {
	qr := []rune(strings.ToLower(query))
	if len(qr) == 0 {
		return nil
	}
	var hits []searchHit
	for i, ln := range lines {
		lower := []rune(strings.ToLower(ln))
		for start := 0; start+len(qr) <= len(lower); start++ {
			if runesEqual(lower[start:start+len(qr)], qr) {
				hits = append(hits, searchHit{line: i, col: start})
			}
		}
	}
	return hits
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// nearestHitForward returns the index of the first hit at or below the current
// viewport top, wrapping to 0 when all hits are above it.
func (s *state) nearestHitForward() int {
	idx := s.manager.FocusedIndex()
	vp, ok := s.viewports[idx]
	if !ok {
		return 0
	}
	top := vp.YOffset()
	for i, h := range s.search.hits {
		if h.line >= top {
			return i
		}
	}
	return 0
}

// searchNext / searchPrev cycle through matches (vi n / N).
func (s *state) searchNext() {
	if len(s.search.hits) == 0 {
		return
	}
	s.search.cur = (s.search.cur + 1) % len(s.search.hits)
	s.gotoCurrentHit()
}

func (s *state) searchPrev() {
	if len(s.search.hits) == 0 {
		return
	}
	s.search.cur = (s.search.cur - 1 + len(s.search.hits)) % len(s.search.hits)
	s.gotoCurrentHit()
}

// gotoCurrentHit scrolls the focused match into view and updates the status.
func (s *state) gotoCurrentHit() {
	if len(s.search.hits) == 0 {
		return
	}
	if s.search.cur < 0 || s.search.cur >= len(s.search.hits) {
		s.search.cur = 0
	}
	s.scrollToBufferLine(s.search.hits[s.search.cur].line)
	s.statusMsg = fmt.Sprintf("/%s  %d/%d", s.search.query, s.search.cur+1, len(s.search.hits))
}

// scrollToBufferLine enters scrollback mode (if needed) and positions the
// viewport so the given buffer line is visible with a little context above.
func (s *state) scrollToBufferLine(line int) {
	idx := s.manager.FocusedIndex()
	s.ensureViewport(idx, s.width, s.height)
	if !s.scrollbackMode[idx] {
		s.enterScrollback(idx)
	}
	vp, ok := s.viewports[idx]
	if !ok {
		return
	}
	off := line - 2
	if off < 0 {
		off = 0
	}
	vp.SetYOffset(off)
	s.viewports[idx] = vp
}

// handleScrollSearchKey processes input while the '/' or ':' prompt is open.
func (s *state) handleScrollSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) && key.Code == 'u' {
		s.search.input = ""
		return nil
	}
	if key.Text != "" {
		s.search.input += key.Text
		return nil
	}
	switch key.Code {
	case tea.KeyEnter:
		s.commitScrollSearch()
	case tea.KeyEscape:
		s.cancelScrollSearch()
	case tea.KeyBackspace:
		r := []rune(s.search.input)
		if len(r) > 0 {
			s.search.input = string(r[:len(r)-1])
		}
	}
	return nil
}

// maybeScrollSearchTrigger intercepts the vi-style scrollback keys ('/', ':',
// 'n', 'N') while the focused session is scrolled into its buffer. Returns
// true when the key was consumed (so it is not forwarded to the child PTY).
func (s *state) maybeScrollSearchTrigger(msg tea.KeyPressMsg) bool {
	idx := s.manager.FocusedIndex()
	if !s.scrollbackMode[idx] {
		return false
	}
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) || key.Mod.Contains(tea.ModAlt) {
		return false
	}
	switch key.Text {
	case "/":
		s.openScrollSearch(false)
		return true
	case ":":
		s.openScrollSearch(true)
		return true
	case "n":
		s.searchNext()
		return true
	case "N":
		s.searchPrev()
		return true
	}
	return false
}

// overlaySearch highlights all matches on the visible scrollback lines,
// rendering the focused match with a distinct style. Matched lines are
// rebuilt from plain buffer text (dropping their original color) so the
// highlight is unambiguous, mirroring overlaySelection.
func (s *state) overlaySearch(pane string, paneCols, paneRows int) string {
	if len(s.search.hits) == 0 {
		return pane
	}
	idx := s.manager.FocusedIndex()
	if !s.scrollbackMode[idx] {
		return pane
	}
	sess := s.manager.Focused()
	if sess == nil {
		return pane
	}
	vp, ok := s.viewports[idx]
	if !ok {
		return pane
	}
	qlen := len([]rune(strings.ToLower(s.search.query)))
	if qlen == 0 {
		return pane
	}
	lines := sess.Screen().BufferLines()
	byLine := map[int][]int{}
	for _, h := range s.search.hits {
		byLine[h.line] = append(byLine[h.line], h.col)
	}
	cur := s.search.hits[s.search.cur]
	base := s.paneRowBase(idx, vp)
	paneLines := strings.Split(pane, "\n")
	for i := 0; i < len(paneLines) && i < paneRows; i++ {
		bufRow := base + i
		cols, has := byLine[bufRow]
		if !has || bufRow < 0 || bufRow >= len(lines) {
			continue
		}
		runes := []rune(lines[bufRow].Text)
		if len(runes) < paneCols {
			runes = append(runes, []rune(strings.Repeat(" ", paneCols-len(runes)))...)
		}
		var b strings.Builder
		last := 0
		for _, c := range cols {
			start, end := clampRange(c, c+qlen, len(runes))
			if start < last {
				continue
			}
			b.WriteString(string(runes[last:start]))
			style := searchMatchSGR
			if bufRow == cur.line && c == cur.col {
				style = searchCurrentSGR
			}
			b.WriteString(style)
			b.WriteString(string(runes[start:end]))
			b.WriteString("\x1b[0m")
			last = end
		}
		b.WriteString(string(runes[last:]))
		paneLines[i] = b.String()
	}
	return strings.Join(paneLines, "\n")
}
