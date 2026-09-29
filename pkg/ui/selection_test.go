package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/session"
)

func newSelectionScrollModel(t *testing.T, layout connectionLayout) *Model {
	t.Helper()
	m := NewModel([]string{"bash"}, 40, 10)
	m.s.connectionLayout = layout
	geom := m.s.geometry()
	m.s.manager = session.NewManager(geom.Pane.Width, geom.Pane.Height, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	if _, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { m.s.manager.CloseAll() })
	m.s.ensureViewport(0, 40, 10)
	return m
}

func TestSelectionDragScrollsAtEdges(t *testing.T) {
	for _, layout := range []connectionLayout{connectionLayoutBottom, connectionLayoutLeft} {
		for _, rectangular := range []bool{false, true} {
			for _, delta := range []int{-1, 1} {
				t.Run(fmt.Sprintf("%s/block=%v/delta=%d", layout, rectangular, delta), func(t *testing.T) {
					m := newSelectionScrollModel(t, layout)
					var rows []string
					for i := 0; i < 40; i++ {
						rows = append(rows, fmt.Sprintf("row %02d  ", i))
					}
					vp := m.s.viewports[0]
					m.s.setScrollbackContent(0, vp, strings.Join(rows, "\n"))
					m.s.scrollbackMode[0] = true
					vp.SetYOffset(10)
					geom := m.s.geometry()
					mod := tea.KeyMod(0)
					if rectangular {
						mod = tea.ModCtrl | tea.ModAlt
					}
					_, _ = m.Update(tea.MouseClickMsg{
						X: geom.Pane.X, Y: geom.Pane.Y + 3, Button: tea.MouseLeft, Mod: mod,
					})
					anchor := m.s.sel.startL
					edgeY := geom.Pane.Y
					if delta > 0 {
						edgeY += geom.Pane.Height - 1
					}
					motion := tea.MouseMotionMsg{
						X: geom.Pane.X + 8, Y: edgeY, Button: tea.MouseLeft,
					}
					_, cmd := m.Update(motion)
					if cmd == nil || vp.YOffset() != 10+delta {
						t.Fatalf("edge drag: offset=%d cmd=%v", vp.YOffset(), cmd != nil)
					}
					scroll := m.s.sel.scroll
					for i := 0; i < 3; i++ {
						_, cmd = m.Update(selectionScrollMsg{scroll: scroll})
						if cmd == nil {
							t.Fatal("edge scrolling stopped before the history boundary")
						}
					}
					if vp.YOffset() != 10+4*delta || m.s.sel.startL != anchor {
						t.Fatalf("scroll lost anchor: offset=%d selection=%#v", vp.YOffset(), m.s.sel)
					}
					sl, _, el, _ := m.s.sel.normalized()
					var wantRows []string
					for row := sl; row <= el; row++ {
						text := strings.TrimRight(rows[row], " ")
						if !rectangular && delta < 0 {
							if row == sl {
								text = ""
							}
							if row == el {
								text = "r"
							}
						}
						wantRows = append(wantRows, text)
					}
					want := strings.Join(wantRows, "\n")
					if got := m.s.selectionText(); got != want {
						t.Fatalf("extended selection = %q, want %q", got, want)
					}
					var copied string
					m.s.clipboardWrite = func(text string) { copied = text }
					_, cmd = m.Update(tea.MouseReleaseMsg{
						X: motion.X, Y: motion.Y, Button: tea.MouseLeft,
					})
					if cmd == nil {
						t.Fatal("release did not queue the full selection for copying")
					}
					_ = cmd()
					if copied != want {
						t.Fatalf("copied = %q, want %q", copied, want)
					}
					if _, cmd = m.Update(selectionScrollMsg{scroll: scroll}); cmd != nil {
						t.Fatal("stale tick continued after release")
					}
				})
			}
		}
	}
}

func TestSelectionScrollKeepsLiveSnapshotAndAnchor(t *testing.T) {
	for _, cleared := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleared=%v", cleared), func(t *testing.T) {
			m := newSelectionScrollModel(t, connectionLayoutBottom)
			sess := m.s.manager.Focused()
			for i := 0; i < 30; i++ {
				sess.Screen().Write([]byte(fmt.Sprintf("history %02d\r\n", i)))
			}
			if cleared {
				sess.Screen().Write([]byte("\x1b[2J\x1b[Hpainted first\r\npainted second"))
			}
			vp := m.s.viewports[0]
			m.s.setLiveContent(0, vp, sess)
			before := m.s.selectionLines(0, vp)
			m.s.startSelection(0, 2, false)
			sess.Screen().Write([]byte("\x1b[2J\x1b[Hnew output"))
			_, _ = m.Update(renderTickMsg{})
			if got := m.s.selectionLines(0, vp)[2]; got != before[2] {
				t.Fatalf("output changed painted selection row: %#v, want %#v", got, before[2])
			}
			base := m.s.sel.historyBase
			if base == 0 {
				t.Fatal("missing history before live screen")
			}
			m.s.scrollSelection(-3, 4, 0)
			lines := m.s.selectionLines(0, vp)
			if m.s.sel.startL != base+2 || lines[m.s.sel.startL] != before[2] {
				t.Fatalf("live anchor moved to different text: %#v", m.s.sel)
			}
			for i, line := range before {
				if lines[base+i] != line {
					t.Fatalf("painted row %d = %#v, want %#v", i, lines[base+i], line)
				}
			}
			m.s.scrollSelection(100, 4, vp.Height()-1)
			content := vp.GetContent()
			_, _ = m.Update(renderTickMsg{})
			if !m.s.scrollbackMode[0] || vp.GetContent() != content {
				t.Fatal("render tick discarded selection at scrollback tail")
			}
		})
	}
}

func TestSelectionWheelExtendsAndEdgeTickStops(t *testing.T) {
	m := newSelectionScrollModel(t, connectionLayoutBottom)
	vp := m.s.viewports[0]
	m.s.setScrollbackContent(0, vp, strings.Repeat("row\n", 40))
	m.s.scrollbackMode[0] = true
	vp.SetYOffset(10)
	geom := m.s.geometry()
	m.s.startSelection(0, 3, true)
	anchor := m.s.sel.startL
	for _, button := range []tea.MouseButton{tea.MouseWheelUp, tea.MouseWheelDown} {
		offset := vp.YOffset()
		_, _ = m.Update(tea.MouseWheelMsg{
			X: geom.Pane.X + 2, Y: geom.Pane.Y + 1, Button: button,
		})
		delta := 3
		if button == tea.MouseWheelUp {
			delta = -3
		}
		if vp.YOffset() != offset+delta || m.s.sel.endL != vp.YOffset()+1 ||
			m.s.sel.startL != anchor || !m.s.sel.rectangular {
			t.Fatalf("wheel lost selection: offset=%d selection=%#v", vp.YOffset(), m.s.sel)
		}
	}
	_, _ = m.Update(tea.MouseMotionMsg{X: geom.Pane.X + 2, Y: 0, Button: tea.MouseLeft})
	scroll := m.s.sel.scroll
	_, _ = m.Update(tea.MouseMotionMsg{
		X: geom.Pane.X + 2, Y: geom.Pane.Y + 2, Button: tea.MouseLeft,
	})
	offset := vp.YOffset()
	if _, cmd := m.Update(selectionScrollMsg{scroll: scroll}); cmd != nil || vp.YOffset() != offset {
		t.Fatal("returning inside the pane did not stop edge scrolling")
	}
	_, _ = m.Update(tea.MouseMotionMsg{X: geom.Pane.X + 2, Y: 0, Button: tea.MouseLeft})
	scroll = m.s.sel.scroll
	for i := 0; i < 50 && m.s.sel.scroll != nil; i++ {
		_, _ = m.Update(selectionScrollMsg{scroll: scroll})
	}
	if vp.YOffset() != 0 || m.s.sel.scroll != nil || m.s.sel.endL != 0 {
		t.Fatal("edge scrolling did not stop at the history boundary")
	}
}

func TestSelectionScrollCancellation(t *testing.T) {
	for _, action := range []string{"resize", "focus", "shift", "new-drag", "typing", "paste"} {
		t.Run(action, func(t *testing.T) {
			m := newSelectionScrollModel(t, connectionLayoutBottom)
			vp := m.s.viewports[0]
			m.s.setScrollbackContent(0, vp, strings.Repeat("row\n", 40))
			m.s.scrollbackMode[0] = true
			vp.SetYOffset(10)
			m.s.startSelection(0, 3, false)
			_ = m.s.dragSelection(2, 0, -1)
			scroll := m.s.sel.scroll
			if scroll == nil {
				t.Fatal("missing edge-scroll timer")
			}
			switch action {
			case "resize":
				_, _ = m.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
			case "focus":
				m.s.refreshFocused()
			case "shift":
				_, _ = m.Update(tea.MouseMotionMsg{X: 2, Y: 2, Button: tea.MouseLeft, Mod: tea.ModShift})
			case "new-drag":
				m.s.startSelection(0, 3, false)
				_ = m.s.dragSelection(2, 0, -1)
			case "typing":
				_, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			case "paste":
				_, _ = m.Update(tea.PasteMsg{Content: "text"})
			}
			offset := vp.YOffset()
			if _, cmd := m.Update(selectionScrollMsg{scroll: scroll}); cmd != nil || vp.YOffset() != offset {
				t.Fatal("stale scroll timer changed a cancelled/replaced selection")
			}
		})
	}
}

func TestSelectionScrollDoesNotEnterAlternateScreenHistory(t *testing.T) {
	m := newSelectionScrollModel(t, connectionLayoutBottom)
	sess := m.s.manager.Focused()
	sess.Screen().Write([]byte(strings.Repeat("history\r\n", 30) + "\x1b[?1049halt screen"))
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	m.s.startSelection(0, 2, false)
	if cmd := m.s.dragSelection(3, 0, -1); cmd != nil || m.s.scrollbackMode[0] {
		t.Fatal("alternate-screen drag exposed main-screen scrollback")
	}
}

func TestSelectionReleaseResumesOutput(t *testing.T) {
	for _, dragged := range []bool{false, true} {
		t.Run(fmt.Sprintf("dragged=%v", dragged), func(t *testing.T) {
			m := newSelectionScrollModel(t, connectionLayoutBottom)
			sess := m.s.manager.Focused()
			sess.Screen().Write([]byte("old output"))
			vp := m.s.viewports[0]
			m.s.setLiveContent(0, vp, sess)
			m.s.startSelection(0, 0, false)
			if dragged {
				m.s.updateSelection(3, 0)
			}
			sess.Screen().Write([]byte("\x1b[2J\x1b[Hnew output"))
			_, _ = m.Update(renderTickMsg{})
			_ = m.s.finishSelection()
			if !strings.Contains(vp.GetContent(), "new output") {
				t.Fatal("release left the terminal frozen on the selection snapshot")
			}
		})
	}
}

func TestRectangularSelectionTrimsSpacesButPreservesLineBreaks(t *testing.T) {
	lines := []session.BufferLine{
		{Text: "  ab   ", SoftWrap: true},
		{Text: "       "},
		{Text: "  c d  "},
		{Text: ""},
	}
	sel := selection{rectangular: true, startC: 0, endL: 3, endC: 6}
	if got := rectangularSelectionText(lines, sel); got != "  ab\n\n  c d\n" {
		t.Fatalf("block copy = %q, want leading/interior spaces and all row breaks", got)
	}
	m := newSelectionScrollModel(t, connectionLayoutBottom)
	vp := m.s.viewports[0]
	vp.SetContent("  ab   \n  c d  ")
	m.s.liveLines[0] = []session.BufferLine{{Text: "  ab   "}, {Text: "  c d  "}}
	m.s.sel = selection{startC: 0, endL: 1, endC: 6, hasRange: true}
	if got := m.s.selectionText(); got != "  ab   \n  c d  " {
		t.Fatalf("linear copy spaces changed: %q", got)
	}
}

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
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
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

func TestLiveSelectionUsesWrapMetadataFromPaintedSnapshot(t *testing.T) {
	m := NewModel([]string{"bash"}, 5, 6)
	m.s.manager = session.NewManager(10, 4, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	sess.Screen().Write([]byte("\x1b[2J\x1b[Habcdefghijklmnop\r\nnext"))
	m.s.ensureViewport(0, 5, 6)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)

	// Advance the emulator after the painted frame. Selection still targets
	// the viewport snapshot, so its terminal-wrap metadata must do the same.
	sess.Screen().Write([]byte("\x1b[2J\x1b[Hnew frame"))

	lines := m.s.selectionLines(0, vp)
	if len(lines) < 4 || !lines[0].SoftWrap || !lines[1].SoftWrap || !lines[2].SoftWrap {
		t.Fatalf("snapshot selection lines = %#v, want all virtual segments joined", lines)
	}
	m.s.sel = selection{startL: 0, startC: 0, endL: 3, endC: 0, hasRange: true}
	if got := m.s.selectionText(); got != "abcdefghijklmnop" {
		t.Fatalf("selectionText() = %q, want one logical line", got)
	}
}

func TestLiveSelectionJoinsLongLineAfterHeavyScroll(t *testing.T) {
	for terminalWidth := 6; terminalWidth <= 20; terminalWidth++ {
		for paneWidth := 3; paneWidth <= terminalWidth; paneWidth++ {
			m := NewModel([]string{"bash"}, paneWidth, 8)
			m.s.manager = session.NewManager(terminalWidth, 6, nil, nil)
			m.s.connections[0].manager = m.s.manager
			m.s.syncActiveConnectionFields()
			sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
			if err != nil {
				t.Fatalf("new session: %v", err)
			}

			for i := 0; i < 100; i++ {
				sess.Screen().Write([]byte("repeated output\r\n"))
			}
			line := strings.Repeat("x", terminalWidth*2+3)
			sess.Screen().Write([]byte(line))
			m.s.ensureViewport(0, paneWidth, 8)
			vp := m.s.viewports[0]
			m.s.setLiveContent(0, vp, sess)
			lines := m.s.selectionLines(0, vp)

			start := -1
			for i := range lines {
				if strings.Contains(lines[i].Text, "xxx") {
					start = i
					break
				}
			}
			if start < 0 {
				m.s.manager.CloseAll()
				t.Fatalf("widths %d/%d: long line not found in %#v", terminalWidth, paneWidth, lines)
			}
			remaining := len(line)
			end := start
			endCol := 0
			for end < len(lines) && remaining > 0 {
				rowLen := len([]rune(lines[end].Text))
				if remaining <= rowLen {
					endCol = remaining - 1
					remaining = 0
					break
				}
				remaining -= rowLen
				end++
			}
			m.s.sel = selection{
				startL:   start,
				startC:   0,
				endL:     end,
				endC:     endCol,
				hasRange: true,
			}
			if got := m.s.selectionText(); got != line {
				m.s.manager.CloseAll()
				t.Fatalf("widths %d/%d: selectionText() = %q, want %q; rows=%#v",
					terminalWidth, paneWidth, got, line, lines[start:end+1])
			}
			m.s.manager.CloseAll()
		}
	}
}

func TestLiveSelectionCopiesCloudHypervisorCommandAsOneLine(t *testing.T) {
	const line = `sudo ./cloud-hypervisor-41 --kernel vmlinux-6.8.0-52-generic --cpus boot=1 --memory size=2G --cmdline "root=/dev/vda console=hvc0 panic=0 ip=169.254.0.2::169.254.0.1:255.255.255.0:vm:eth0:off init=/sbin/init.sh" --disk path=./rootfs.ext4,readonly=off`
	for terminalWidth := 20; terminalWidth <= 160; terminalWidth++ {
		m := NewModel([]string{"bash"}, terminalWidth, 32)
		m.s.manager = session.NewManager(terminalWidth, 30, nil, nil)
		m.s.connections[0].manager = m.s.manager
		m.s.syncActiveConnectionFields()
		sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		sess.Screen().Write([]byte(line))

		for paneWidth := 5; paneWidth <= terminalWidth; paneWidth++ {
			m.s.width = paneWidth
			m.s.resetViewport(0, paneWidth, 32)
			m.s.ensureViewport(0, paneWidth, 32)
			vp := m.s.viewports[0]
			m.s.setLiveContent(0, vp, sess)
			lines := m.s.selectionLines(0, vp)

			end := 0
			for end+1 < len(lines) && lines[end].SoftWrap {
				end++
			}
			m.s.sel = selection{
				startL:   0,
				startC:   0,
				endL:     end,
				endC:     len([]rune(lines[end].Text)) - 1,
				hasRange: true,
			}
			if got := m.s.selectionText(); got != line {
				t.Fatalf("widths %d/%d: selectionText() = %q, want one command; rows=%#v snapshot=%#v",
					terminalWidth, paneWidth, got, lines[:end+1], m.s.liveLines[0])
			}
		}
		m.s.manager.CloseAll()
	}
}

func TestLiveSelectionCopiesRedrawnCommandEndingWrapInSpace(t *testing.T) {
	const line = `sudo ./cloud-hypervisor-41 --kernel vmlinux-6.8.0-52-generic --cpus boot=1 --memory size=2G --cmdline "root=/dev/vda console=hvc0 panic=0 ip=169.254.0.2::169.254.0.1:255.255.255.0:vm:eth0:off init=/sbin/init.sh" --disk path=./rootfs.ext4,readonly=off`
	m := NewModel([]string{"bash"}, 138, 12)
	m.s.manager = session.NewManager(138, 10, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	// Cursor-addressed prompts can leave extra text in the semantic byte
	// capture even though it has been erased from the visible screen.
	sess.Screen().Write([]byte("stale prompt\x1b[H\x1b[2K" + line))
	m.s.ensureViewport(0, 138, 12)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	lines := m.s.selectionLines(0, vp)

	end := 0
	for end+1 < len(lines) && lines[end].SoftWrap {
		end++
	}
	m.s.sel = selection{
		startL: 0, startC: 0,
		endL: end, endC: len([]rune(lines[end].Text)) - 1,
		hasRange: true,
	}
	if got := m.s.selectionText(); got != line {
		t.Fatalf("selectionText() = %q, want one command; rows=%#v snapshot=%#v",
			got, lines[:end+1], m.s.liveLines[0])
	}
}

func TestLiveSelectionJoinsANSISeparatedCRLFAfterWrapBoundarySpace(t *testing.T) {
	const line = "abcdefghijklmnopqrs continuation after boundary"
	m := NewModel([]string{"bash"}, 20, 6)
	m.s.manager = session.NewManager(20, 4, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	sess.Screen().Write([]byte(line + "\r\x1b[0m\nnext"))
	m.s.ensureViewport(0, 20, 6)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	lines := m.s.selectionLines(0, vp)

	end := 0
	for end+1 < len(lines) && lines[end].SoftWrap {
		end++
	}
	m.s.sel = selection{
		startL: 0, startC: 0,
		endL: end, endC: len([]rune(lines[end].Text)) - 1,
		hasRange: true,
	}
	if got := m.s.selectionText(); got != line {
		t.Fatalf("selectionText() = %q, want %q; rows=%#v", got, line, lines[:end+1])
	}
}

func TestLiveSelectionJoinsBoundarySpaceFromLogicalScrollbackTail(t *testing.T) {
	m := NewModel([]string{"bash"}, 5, 7)
	m.s.manager = session.NewManager(5, 5, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	sess.Screen().Write([]byte("abcdefghijklmn p\r\none\r\ntwo\r\nthree"))
	m.s.ensureViewport(0, 5, 7)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	lines := m.s.selectionLines(0, vp)

	m.s.sel = selection{
		startL: 0, startC: 0,
		endL: 1, endC: 0,
		hasRange: true,
	}
	if got := m.s.selectionText(); got != "klmn p" {
		t.Fatalf("selectionText() = %q, want %q; rows=%#v", got, "klmn p", lines[:2])
	}
}

func TestLiveSelectionJoinsObservedBoundarySpaceAfterRedrawControl(t *testing.T) {
	const line = "abcd efgh"
	m := NewModel([]string{"bash"}, 5, 6)
	m.s.manager = session.NewManager(5, 4, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	sess.Screen().Write([]byte(line))
	sess.Screen().Write([]byte("\r\x1b[?25l"))
	m.s.ensureViewport(0, 5, 6)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	lines := m.s.selectionLines(0, vp)

	m.s.sel = selection{
		startL: 0, startC: 0,
		endL: 1, endC: 3,
		hasRange: true,
	}
	if got := m.s.selectionText(); got != line {
		t.Fatalf("selectionText() = %q, want %q; rows=%#v", got, line, lines[:2])
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

			m.s.startSelection(paneWidth-2, 0, false)
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

func TestRectangularSelectionCopiesFixedBlock(t *testing.T) {
	m := NewModel([]string{"bash"}, 20, 8)
	m.s.manager = session.NewManager(20, 6, nil, nil)
	m.s.connections[0].manager = m.s.manager
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()
	m.s.ensureViewport(0, 20, 8)
	vp := m.s.viewports[0]
	vp.SetContent("0123456789\nabcdefghij\nXYZ")
	m.s.viewports[0] = vp

	m.s.sel = selection{
		rectangular: true,
		startL:      0, startC: 6,
		endL: 2, endC: 2,
		hasRange: true,
	}
	if got := m.s.selectionText(); got != "23456\ncdefg\nZ" {
		t.Fatalf("selectionText() = %q, want rectangular block", got)
	}
}

func TestRectangularSelectionKeepsNewlinesAcrossSoftWraps(t *testing.T) {
	lines := []session.BufferLine{
		{Text: "abcdef", SoftWrap: true},
		{Text: "ghijkl"},
	}
	sel := selection{
		rectangular: true,
		startL:      0, startC: 1,
		endL: 1, endC: 3,
		hasRange: true,
	}
	if got := rectangularSelectionText(lines, sel); got != "bcd\nhij" {
		t.Fatalf("rectangularSelectionText() = %q, want fixed rows", got)
	}
}

func TestCtrlAltMouseDragStartsRectangularSelection(t *testing.T) {
	m := NewModel([]string{"bash"}, 40, 10)
	m.s.manager = session.NewManager(40, 8, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()
	m.s.ensureViewport(0, 40, 10)
	geom := m.s.geometry()

	_, _ = m.Update(tea.MouseClickMsg{
		X: geom.Pane.X + 2, Y: geom.Pane.Y + 1,
		Button: tea.MouseLeft, Mod: tea.ModCtrl | tea.ModAlt,
	})
	if !m.s.sel.active || !m.s.sel.rectangular {
		t.Fatalf("selection = %#v, want active rectangular selection", m.s.sel)
	}
}

func TestRectangularSelectionOverlayUsesSameColumnsOnEveryRow(t *testing.T) {
	m := NewModel([]string{"bash"}, 10, 6)
	m.s.manager = session.NewManager(10, 4, nil, nil)
	m.s.connections[0].manager = m.s.manager
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()
	m.s.ensureViewport(0, 10, 6)
	vp := m.s.viewports[0]
	vp.SetContent("abcdefghij\n0123456789")
	m.s.viewports[0] = vp
	m.s.sel = selection{
		rectangular: true,
		startL:      0, startC: 2,
		endL: 1, endC: 4,
		hasRange: true,
	}

	got := m.s.overlaySelection("abcdefghij\n0123456789", 10, 2)
	if !strings.Contains(got, "ab\x1b[7mcde\x1b[0mfghij") ||
		!strings.Contains(got, "01\x1b[7m234\x1b[0m56789") {
		t.Fatalf("overlaySelection() = %q, want columns 2..4 highlighted on both rows", got)
	}
}

func TestScrollbackSelectionJoinsLogicalLineAfterHeavyOutput(t *testing.T) {
	m := NewModel([]string{"bash"}, 12, 8)
	m.s.manager = session.NewManager(12, 6, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	for i := 0; i < 100; i++ {
		sess.Screen().Write([]byte("repeated output\r\n"))
	}
	const line = "this is one logical line spanning several displayed rows"
	sess.Screen().Write([]byte(line + "\r\nlast"))
	m.s.ensureViewport(0, 12, 8)
	vp := m.s.viewports[0]
	m.s.setScrollbackContent(0, vp, sess.Screen().RenderWithScrollback())
	m.s.scrollbackMode[0] = true

	lines := m.s.selectionLines(0, vp)
	start := -1
	for i := range lines {
		if strings.HasPrefix(lines[i].Text, "this is one") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("logical line not found in %#v", lines)
	}
	end := start
	for end+1 < len(lines) && lines[end].SoftWrap {
		end++
	}
	m.s.sel = selection{
		startL:   start,
		startC:   0,
		endL:     end,
		endC:     len([]rune(lines[end].Text)) - 1,
		hasRange: true,
	}
	if got := m.s.selectionText(); got != line {
		t.Fatalf("selectionText() = %q, want %q; rows=%#v", got, line, lines[start:end+1])
	}
}

func TestScrollbackSelectionCopiesCloudHypervisorCommandAsOneLine(t *testing.T) {
	const line = `sudo ./cloud-hypervisor-41 --kernel vmlinux-6.8.0-52-generic --cpus boot=1 --memory size=2G --cmdline "root=/dev/vda console=hvc0 panic=0 ip=169.254.0.2::169.254.0.1:255.255.255.0:vm:eth0:off init=/sbin/init.sh" --disk path=./rootfs.ext4,readonly=off`
	m := NewModel([]string{"bash"}, 160, 32)
	m.s.manager = session.NewManager(160, 30, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()
	sess.Screen().Write([]byte(line + "\r\nlast"))
	content := sess.Screen().RenderWithScrollback()

	for paneWidth := 5; paneWidth <= 160; paneWidth++ {
		m.s.width = paneWidth
		m.s.resetViewport(0, paneWidth, 32)
		vp := m.s.viewports[0]
		m.s.setScrollbackContent(0, vp, content)
		m.s.scrollbackMode[0] = true
		lines := m.s.selectionLines(0, vp)

		end := 0
		for end+1 < len(lines) && lines[end].SoftWrap {
			end++
		}
		m.s.sel = selection{
			startL:   0,
			startC:   0,
			endL:     end,
			endC:     len([]rune(lines[end].Text)) - 1,
			hasRange: true,
		}
		if got := m.s.selectionText(); got != line {
			t.Fatalf("pane width %d: selectionText() = %q, want one command; rows=%#v",
				paneWidth, got, lines[:end+1])
		}
	}
}

func TestRectangularSelectionAfterScrollbackPadding(t *testing.T) {
	m := NewModel([]string{"bash"}, 12, 8)
	geom := m.s.geometry()
	m.s.manager = session.NewManager(geom.Pane.Width, geom.Pane.Height, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	for i := 0; i < 20; i++ {
		sess.Screen().Write([]byte("history-row\r\n"))
	}
	sess.Screen().Write([]byte("\x1b[2J\x1b[HABCDE\r\n12\r\nxyz"))
	m.s.ensureViewport(0, 12, 8)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	anchorViewportToCursor(vp, sess)
	m.s.scrollFocused(-1)

	m.s.startSelection(1, 1, true)
	m.s.updateSelection(3, 2)
	if got := m.s.selectionText(); got != "BCD\n2" {
		t.Fatalf("rectangular selection after scroll = %q, want %q", got, "BCD\n2")
	}
}

func TestWrappedLineCopyAfterScrollbackPadding(t *testing.T) {
	m := NewModel([]string{"bash"}, 12, 8)
	geom := m.s.geometry()
	m.s.manager = session.NewManager(geom.Pane.Width, geom.Pane.Height, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()

	for i := 0; i < 20; i++ {
		sess.Screen().Write([]byte("history-row\r\n"))
	}
	const line = "this logical line spans several physical rows"
	sess.Screen().Write([]byte("\x1b[2J\x1b[H" + line))
	m.s.ensureViewport(0, 12, 8)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	anchorViewportToCursor(vp, sess)
	m.s.scrollFocused(-1)

	vp = m.s.viewports[0]
	lines := m.s.selectionLines(0, vp)
	start := -1
	for i := range lines {
		if strings.HasPrefix(lines[i].Text, "this logical") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("wrapped logical line not found in %#v", lines)
	}
	end := start
	for end+1 < len(lines) && lines[end].SoftWrap {
		end++
	}
	m.s.sel = selection{
		startL:   start,
		startC:   0,
		endL:     end,
		endC:     len([]rune(lines[end].Text)) - 1,
		hasRange: true,
	}
	if got := m.s.selectionText(); got != line {
		t.Fatalf("wrapped selection = %q, want %q; rows=%#v", got, line, lines[start:end+1])
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
			if cmd == nil {
				t.Fatal("right-click did not return the terminal clipboard command")
			}
			if copied != "" {
				t.Fatalf("clipboard write ran synchronously: %q", copied)
			}
			_ = cmd()
			if copied != "selected" {
				t.Fatalf("copied text = %q, want %q", copied, "selected")
			}
			if m.s.sel.hasRange || m.s.scrollbackMode[0] {
				t.Fatal("successful copy did not clear selection and return to live mode")
			}
		})
	}
}

func TestCopySelectionDoesNotBlockUpdateOnSlowClipboard(t *testing.T) {
	m := NewModel([]string{"bash"}, 20, 8)
	m.s.manager = session.NewManager(20, 6, nil, nil)
	m.s.connections[0].manager = m.s.manager
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()
	m.s.ensureViewport(0, 20, 8)
	vp := m.s.viewports[0]
	vp.SetContent("selected")
	m.s.viewports[0] = vp
	m.s.sel = selection{startL: 0, startC: 0, endL: 0, endC: 7, hasRange: true}

	block := make(chan struct{})
	m.s.clipboardWrite = func(string) { <-block }
	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := m.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseRight})
		done <- cmd
	}()

	var cmd tea.Cmd
	select {
	case cmd = <-done:
	case <-time.After(100 * time.Millisecond):
		close(block)
		t.Fatal("mouse Update blocked on clipboard delivery")
	}
	close(block)
	if cmd == nil {
		t.Fatal("right-click did not return clipboard command")
	}
}

func TestFinishSelectionCopiesAndClearsByDefault(t *testing.T) {
	m := NewModel([]string{"bash"}, 20, 8)
	m.s.manager = session.NewManager(20, 6, nil, nil)
	m.s.connections[0].manager = m.s.manager
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()
	m.s.ensureViewport(0, 20, 8)
	vp := m.s.viewports[0]
	vp.SetContent("selected")
	m.s.viewports[0] = vp
	m.s.sel = selection{
		active: true, startL: 0, startC: 0,
		endL: 0, endC: 7, hasRange: true,
	}
	var copied string
	m.s.clipboardWrite = func(text string) { copied = text }

	cmd := m.s.finishSelection()
	if cmd == nil {
		t.Fatal("finishSelection did not return clipboard command")
	}
	if m.s.sel.hasRange {
		t.Fatal("default copy on release did not clear selection")
	}
	_ = cmd()
	if copied != "selected" {
		t.Fatalf("copied text = %q, want selected", copied)
	}
}

func TestFinishSelectionRetainsSelectionWhenCopyDisabled(t *testing.T) {
	m := NewModel([]string{"bash"}, 20, 8)
	m.s.manager = session.NewManager(20, 6, nil, nil)
	m.s.connections[0].manager = m.s.manager
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.s.manager.CloseAll()
	m.s.ensureViewport(0, 20, 8)
	vp := m.s.viewports[0]
	vp.SetContent("selected")
	m.s.viewports[0] = vp
	m.s.copySelectionOnRelease = false
	m.s.sel = selection{
		active: true, startL: 0, startC: 0,
		endL: 0, endC: 7, hasRange: true,
	}

	if cmd := m.s.finishSelection(); cmd != nil {
		t.Fatal("disabled copy on release returned clipboard command")
	}
	if m.s.sel.active || !m.s.sel.hasRange {
		t.Fatalf("selection = %#v, want completed retained selection", m.s.sel)
	}
}
