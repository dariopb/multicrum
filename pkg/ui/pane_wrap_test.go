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
