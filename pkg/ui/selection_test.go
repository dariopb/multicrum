package ui

import "testing"

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
