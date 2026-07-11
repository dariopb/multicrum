package ui

import (
	"testing"

	"multicrum/pkg/session"
)

func TestClampRange(t *testing.T) {
	cases := []struct {
		start, end, n      int
		wantStart, wantEnd int
	}{
		// Regression: start column past a short scrollback line (the crash:
		// "slice bounds out of range [:60] with capacity 56").
		{60, 57, 56, 56, 56},
		{0, 10, 56, 0, 10},
		{-5, 10, 56, 0, 10},
		{10, 5, 56, 10, 10},
		{3, 100, 56, 3, 56},
		{0, 0, 0, 0, 0},
	}
	for _, c := range cases {
		gotStart, gotEnd := clampRange(c.start, c.end, c.n)
		if gotStart != c.wantStart || gotEnd != c.wantEnd {
			t.Errorf("clampRange(%d,%d,%d) = (%d,%d), want (%d,%d)",
				c.start, c.end, c.n, gotStart, gotEnd, c.wantStart, c.wantEnd)
		}
		if gotStart < 0 || gotEnd < gotStart || gotEnd > c.n {
			t.Errorf("clampRange(%d,%d,%d) produced unsafe bounds (%d,%d)",
				c.start, c.end, c.n, gotStart, gotEnd)
		}
	}
}

// TestLiveSelectionUsesRenderedViewportSnapshot ensures selection targets the
// exact frame currently painted to the terminal. A resize or child redraw can
// advance the emulator after the last render tick; consulting the current
// screen in that interval would select a different (often blank) row.
func TestLiveSelectionUsesRenderedViewportSnapshot(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	m.s.manager = session.NewManager(80, 22, nil, nil)
	m.s.connections[0].manager = m.s.manager
	sess, err := m.s.manager.New([]string{"sh"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	m.s.ensureViewport(0, 80, 24)
	vp := m.s.viewports[0]
	vp.SetContent("\x1b[32mrendered row\x1b[0m")
	m.s.viewports[0] = vp

	// Simulate child output received after the viewport snapshot but before
	// the next coalesced render tick.
	sess.Screen().Write([]byte("newer emulator row"))

	lines := m.s.selectionLines(0, vp)
	if len(lines) != 1 || lines[0].Text != "rendered row" {
		t.Fatalf("selection source = %#v, want rendered viewport row", lines)
	}
}
