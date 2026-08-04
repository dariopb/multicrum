package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"multicrum/pkg/session"
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
		got := m.s.renderPaneContent(0, vp, 80, 22, false)
		if got != want {
			t.Fatalf("%s: cached render != uncached render", label)
		}
		// A second call must hit the cache and stay identical.
		if again := m.s.renderPaneContent(0, vp, 80, 22, false); again != got {
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

func TestLiveViewportUsesPhysicalTerminalRows(t *testing.T) {
	m := NewModel([]string{"bash"}, 20, 6)
	m.s.manager = session.NewManager(20, 4, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	sess.Screen().Write([]byte("one\r\ntwo\r\nthree\r\nfour"))
	m.s.ensureViewport(0, 20, 6)
	vp := m.s.viewports[0]
	vp.SoftWrap = true
	m.s.setLiveContent(0, vp, sess)
	anchorViewportToCursor(vp, sess)

	if vp.SoftWrap {
		t.Fatal("live viewport must count physical terminal rows without soft wrapping")
	}
	if got := vp.YOffset(); got != 0 {
		t.Fatalf("live viewport YOffset = %d, want 0", got)
	}
	pane := m.s.renderPaneContent(0, vp, 20, 4, false)
	if strings.Contains(pane, "three\n                    \nfour") {
		t.Fatalf("live pane inserted an empty row before the prompt: %q", pane)
	}
}

func TestRenderScrollbackWrapCacheMatchesUncached(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.s.ensureViewport(0, 80, 24)
	vp := m.s.viewports[0]
	vp.SoftWrap = true
	vp.SetContent(
		"short\n" +
			"this line is deliberately long enough to wrap several times in the vertical layout\n" +
			"middle\n" +
			"another line that keeps going past both tested pane widths to exercise cache replacement\n" +
			"last",
	)

	content := vp.GetContent()
	for _, width := range []int{65, 25} {
		m.s.width = width
		m.s.setScrollbackContent(0, vp, content)
		for _, offset := range []int{0, 1, 3, 5} {
			vp.SetYOffset(offset)
			want := renderPaneContentUncached(content, width, 8, true, vp.YOffset())
			got := m.s.renderPaneContent(0, vp, width, 8, true)
			if got != want {
				t.Fatalf("width %d offset %d: cached render != uncached render", width, offset)
			}
		}
	}

	content += "\nnew content invalidates the wrapped rows"
	m.s.setScrollbackContent(0, vp, content)
	want := renderPaneContentUncached(content, 25, 8, true, vp.YOffset())
	if got := m.s.renderPaneContent(0, vp, 25, 8, true); got != want {
		t.Fatal("changed content: cached render != uncached render")
	}

	vp.SetHeight(8)
	vp.GotoBottom()
	before := vp.YOffset()
	vp.ScrollUp(3)
	if got := before - vp.YOffset(); got != 3 {
		t.Fatalf("wheel-sized scroll moved %d rows, want 3", got)
	}
	if vp.SoftWrap {
		t.Fatal("scrollback viewport must use prewrapped rows")
	}
}
