package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestLiveSelectionPreservesMatchingSoftWrapMetadata(t *testing.T) {
	m := NewModel([]string{"bash"}, 5, 6)
	m.s.manager = session.NewManager(10, 4, nil, nil)
	m.s.connections[0].manager = m.s.manager
	sess, err := m.s.manager.New([]string{"sh"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	sess.Screen().Write([]byte("\x1b[2J\x1b[Habcdefgh\r\nnext"))
	m.s.ensureViewport(0, 5, 6)
	vp := m.s.viewports[0]
	vp.SetContent(sess.Screen().Render())

	lines := m.s.selectionLines(0, vp)
	if len(lines) < 2 || lines[0].Text != "abcde" || lines[1].Text != "fgh" || !lines[0].SoftWrap {
		t.Fatalf("live selection lines = %#v, want pane-wrapped abcdefgh rows", lines)
	}
	m.s.sel = selection{startL: 0, startC: 0, endL: 1, endC: 2, hasRange: true}
	if got := m.s.selectionText(); got != "abcdefgh" {
		t.Fatalf("selectionText() = %q, want joined soft-wrapped line", got)
	}
}

func TestScrollbackSelectionUsesDisplayedWrappedRows(t *testing.T) {
	for _, layout := range []connectionLayout{connectionLayoutBottom, connectionLayoutLeft} {
		t.Run(string(layout), func(t *testing.T) {
			m := NewModel([]string{"bash"}, 40, 10)
			m.s.connectionLayout = layout
			m.s.manager = session.NewManager(40, 8, nil, nil)
			m.s.connections[0].manager = m.s.manager
			if _, err := m.s.manager.New([]string{"sh"}); err != nil {
				t.Fatalf("new session: %v", err)
			}
			defer m.s.manager.CloseAll()
			m.s.ensureViewport(0, 40, 10)
			vp := m.s.viewports[0]
			paneWidth := m.s.geometry().Pane.Width
			content := "\x1b[32m" + strings.Repeat("abcdefghij", 6) + "\x1b[0m\nlast"
			m.s.setScrollbackContent(0, vp, content)
			m.s.scrollbackMode[0] = true
			vp.SetHeight(4)
			vp.GotoTop()

			lines := m.s.selectionLines(0, vp)
			if len(lines) < 2 || !lines[0].SoftWrap {
				t.Fatalf("selection rows = %#v, want wrapped display rows", lines)
			}

			m.s.startSelection(paneWidth-2, 0)
			m.s.updateSelection(2, 1)
			want := lines[0].Text[paneWidth-2:] + lines[1].Text[:3]
			if got := m.s.selectionText(); got != want {
				t.Fatalf("selectionText() = %q, want %q", got, want)
			}

			pane := m.s.renderPaneContent(0, vp, paneWidth, 4, true)
			if got := m.s.overlaySelection(pane, paneWidth, 4); !strings.Contains(got, "\x1b[7m") {
				t.Fatal("selection overlay did not highlight the displayed wrapped rows")
			}
		})
	}
}

func TestRightClickCopiesSelectionOnPressOrRelease(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.MouseMsg
	}{
		{name: "press", msg: tea.MouseClickMsg{X: 2, Y: 1, Button: tea.MouseRight}},
		{name: "release", msg: tea.MouseReleaseMsg{X: 2, Y: 1, Button: tea.MouseRight}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel([]string{"bash"}, 40, 10)
			m.s.manager = session.NewManager(40, 8, nil, nil)
			m.s.connections[0].manager = m.s.manager
			if _, err := m.s.manager.New([]string{"sh"}); err != nil {
				t.Fatalf("new session: %v", err)
			}
			defer m.s.manager.CloseAll()
			m.s.ensureViewport(0, 40, 10)
			vp := m.s.viewports[0]
			m.s.setScrollbackContent(0, vp, "selected text\nother")
			m.s.scrollbackMode[0] = true
			m.s.sel = selection{startL: 0, startC: 0, endL: 0, endC: 7, hasRange: true}

			var copied string
			m.s.clipboardWrite = func(text string) { copied = text }
			_, cmd := m.Update(tc.msg)
			if copied != "selected" {
				t.Fatalf("copied text = %q, want %q", copied, "selected")
			}
			if cmd == nil {
				t.Fatal("right-click did not return the terminal clipboard command")
			}
			if m.s.sel.hasRange || m.s.scrollbackMode[0] {
				t.Fatal("successful copy did not clear selection and return to live mode")
			}
		})
	}
}
