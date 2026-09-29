package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestScrollSearchPromptInRightPaneTopBar(t *testing.T) {
	for _, layout := range []connectionLayout{connectionLayoutBottom, connectionLayoutLeft} {
		t.Run(string(layout), func(t *testing.T) {
			m := newSelectionScrollModel(t, layout)
			m.s.width = 90
			m.s.resetViewport(0, 90, 10)
			vp := m.s.viewports[0]
			m.s.setScrollbackContent(0, vp, strings.Repeat("needle in haystack\n", 40))
			m.s.scrollbackMode[0] = true
			vp.SetYOffset(5)
			normal := ansi.Strip(m.renderTabBar())
			counter := fmt.Sprintf("%d/%d", vp.YOffset()+vp.Height(), vp.TotalLineCount())
			_, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
			_, _ = m.Update(tea.PasteMsg{Content: "needle"})
			top := ansi.Strip(m.renderTabBar())
			if !strings.Contains(top, "/needle") || strings.Index(top, "/needle") >= strings.Index(top, counter) {
				t.Fatalf("prompt must precede position in topbar: %q", top)
			}
			if strings.Contains(ansi.Strip(m.renderPane()), counter) {
				t.Fatal("scroll indicator still obscures the first terminal row")
			}
			frameTop := strings.Split(ansi.Strip(m.View().Content), "\n")[0]
			if !strings.Contains(frameTop, "/needle") {
				t.Fatalf("search missing from composed frame topbar: %q", frameTop)
			}
			_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if got := ansi.Strip(m.renderTabBar()); got != normal {
				t.Fatalf("cancel did not restore tabbar: %q, want %q", got, normal)
			}
			m.s.openScrollSearch(false)
			m.s.search.input = "needle"
			_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.s.mode != modeNormal || strings.Contains(ansi.Strip(m.renderTabBar()), "/needle") {
				t.Fatal("committing search left the input in the topbar")
			}
		})
	}
}

func TestScrollSearchTopbarKeepsTypedTailAndPosition(t *testing.T) {
	m := newSelectionScrollModel(t, connectionLayoutLeft)
	vp := m.s.viewports[0]
	m.s.setScrollbackContent(0, vp, strings.Repeat("row\n", 40))
	m.s.scrollbackMode[0] = true
	m.s.openScrollSearch(false)
	m.s.search.input = strings.Repeat("long query ", 8) + "TAIL"
	bar := m.renderTabBar()
	if ansi.StringWidth(bar) != m.s.geometry().TabBar.Width || !strings.Contains(ansi.Strip(bar), "TAIL") {
		t.Fatalf("narrow topbar lost input tail or overflowed: %q", ansi.Strip(bar))
	}
	statusLeft := m.s.geometry().TabBar.X + m.s.geometry().TabBar.Width - ansi.StringWidth(m.s.scrollbackStatus(m.s.geometry().TabBar.Width))
	for _, box := range m.s.sessionHitboxes {
		if box.Bounds.Width > 0 && box.Bounds.X+box.Bounds.Width > statusLeft {
			t.Fatalf("covered tab remains clickable: %#v", box)
		}
	}
	m.s.search.lineJump = true
	m.s.search.input = "12"
	if !strings.Contains(ansi.Strip(m.renderTabBar()), ":12") {
		t.Fatal("line-jump prompt missing from topbar")
	}
	vp.GotoBottom()
	if !strings.Contains(ansi.Strip(m.renderTabBar()), ":12") {
		t.Fatal("active prompt disappeared at the scrollback tail")
	}
}

func TestScrollSearchUsesDisplayedWrappedRows(t *testing.T) {
	m := newSelectionScrollModel(t, connectionLayoutLeft)
	vp := m.s.viewports[0]
	width := m.s.geometry().Pane.Width
	m.s.setScrollbackContent(0, vp, strings.Repeat("x", width+3)+" needle\n"+strings.Repeat("row\n", 20))
	m.s.scrollbackMode[0] = true
	m.s.runSearch("needle")
	if len(m.s.search.hits) != 1 || m.s.search.hits[0] != (searchHit{line: 1, col: 4}) {
		t.Fatalf("search used a different row source from the pane: %#v", m.s.search.hits)
	}
	m.s.jumpToLine("15")
	if vp.YOffset() != 12 {
		t.Fatalf("line jump used stale emulator rows: offset=%d", vp.YOffset())
	}
}

func TestFindHits(t *testing.T) {
	lines := []string{
		"the quick brown fox",
		"THE lazy dog",
		"no match here",
		"catcat", // overlapping-adjacent repeats
	}
	cases := []struct {
		name  string
		query string
		want  []searchHit
	}{
		{
			name:  "case insensitive across lines",
			query: "the",
			want:  []searchHit{{line: 0, col: 0}, {line: 1, col: 0}},
		},
		{
			name:  "multiple hits same line",
			query: "cat",
			want:  []searchHit{{line: 3, col: 0}, {line: 3, col: 3}},
		},
		{
			name:  "no match",
			query: "zzz",
			want:  nil,
		},
		{
			name:  "empty query",
			query: "",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findHits(lines, tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findHits(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

func TestRunesEqual(t *testing.T) {
	if !runesEqual([]rune("abc"), []rune("abc")) {
		t.Fatal("expected equal runes to compare equal")
	}
	if runesEqual([]rune("abc"), []rune("abd")) {
		t.Fatal("expected different runes to compare unequal")
	}
	if runesEqual([]rune("ab"), []rune("abc")) {
		t.Fatal("expected different lengths to compare unequal")
	}
}
