package ui

import (
	"strings"
	"testing"
	"time"

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
	if got := m.s.selectionText(); got != "23456\ncdefg\nZ    " {
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
