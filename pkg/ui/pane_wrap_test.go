package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestSoftWrapRowsMatchesViewportModel verifies that softWrapRows expands
// logical lines into the same fixed-width wrapped rows the viewport counts when
// computing YOffset. If these ever diverge, scrollback scrolling drifts and
// duplicates/drops rows.
func TestSoftWrapRowsMatchesViewportModel(t *testing.T) {
	width := 10
	lines := []string{
		"short",                       // 1 row
		"exactlyten",                  // exactly width -> 1 row
		"this is a longer line wraps", // 27 -> 3 rows
		"",                            // blank -> 1 row
	}
	got := softWrapRows(lines, width)

	// Expected wrapped-row count using the same ceil(width/maxWidth) rule the
	// viewport uses in calculateLine.
	want := 0
	for _, l := range lines {
		w := ansi.StringWidth(l)
		if w <= width {
			want++
			continue
		}
		for idx := 0; idx < w; idx += width {
			want++
		}
	}
	if len(got) != want {
		t.Fatalf("softWrapRows produced %d rows, want %d", len(got), want)
	}
	for i, row := range got {
		if w := ansi.StringWidth(row); w > width {
			t.Fatalf("row %d width %d exceeds pane width %d: %q", i, w, width, row)
		}
	}
}

// TestRenderPaneContentCacheMatchesUncached verifies the memoized pane render
// returns exactly what the uncached path produces across content/offset/size
// changes — otherwise a stale cache key would show wrong or frozen output.
func TestRenderPaneContentCacheMatchesUncached(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.s.ensureViewport(0, 80, 24)
	vp := m.s.viewports[0]

	check := func(label string) {
		content := vp.GetContent()
		yoff := vp.YOffset()
		want := renderPaneContentUncached(content, 80, 22, false, yoff)
		got := m.s.renderPaneContent(vp, 80, 22, false)
		if got != want {
			t.Fatalf("%s: cached render != uncached render", label)
		}
		// A second call must hit the cache and stay identical.
		if again := m.s.renderPaneContent(vp, 80, 22, false); again != got {
			t.Fatalf("%s: cache-hit render changed output", label)
		}
	}

	vp.SetContent("line one\nline two\nline three")
	check("initial content")

	vp.SetContent("different\ncontent\nhere\nnow")
	check("changed content")

	vp.SetYOffset(1)
	check("changed offset")
}
