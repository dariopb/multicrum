//go:build !windows

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"multicrum/pkg/session"
)

func TestLiveSelectionJoinsShellEchoAcrossTerminalAndPaneWraps(t *testing.T) {
	m := NewModel([]string{"sh"}, 5, 6)
	m.s.manager = session.NewManager(10, 4, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"sh"})
	if err != nil {
		t.Fatalf("new shell: %v", err)
	}

	defer m.s.manager.CloseAll()

	const input = "abcdefghijklmnopqrstuvwxyz"
	if _, err := sess.Write([]byte(input)); err != nil {
		t.Fatalf("write shell input: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.ReplaceAll(sess.Screen().Render(), "\n", ""), input) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	m.s.ensureViewport(0, 5, 6)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	lines := m.s.selectionLines(0, vp)

	start, end := -1, -1
	for i := range lines {
		if start < 0 && strings.Contains(lines[i].Text, "abc") {
			start = i
		}
		if start >= 0 && strings.Contains(lines[i].Text, "z") {
			end = i
			break
		}
	}
	if start < 0 || end < start {
		t.Fatalf("echoed input rows not found: %#v", lines)
	}
	m.s.sel = selection{
		startL:   start,
		startC:   strings.Index(lines[start].Text, "a"),
		endL:     end,
		endC:     strings.Index(lines[end].Text, "z"),
		hasRange: true,
	}
	if got := m.s.selectionText(); got != input {
		t.Fatalf("selectionText() = %q, want %q; rows=%#v", got, input, lines[start:end+1])
	}
}

func TestLiveSelectionCopiesCloudHypervisorShellEchoAsOneLine(t *testing.T) {
	const input = `sudo ./cloud-hypervisor-41 --kernel vmlinux-6.8.0-52-generic --cpus boot=1 --memory size=2G --cmdline "root=/dev/vda console=hvc0 panic=0 ip=169.254.0.2::169.254.0.1:255.255.255.0:vm:eth0:off init=/sbin/init.sh" --disk path=./rootfs.ext4,readonly=off`
	for terminalWidth := 120; terminalWidth <= 150; terminalWidth++ {
		m := NewModel([]string{"sh"}, terminalWidth, 22)
		m.s.manager = session.NewManager(terminalWidth, 20, nil, nil)
		m.s.connections[0].manager = m.s.manager
		m.s.syncActiveConnectionFields()
		sess, err := m.s.manager.New([]string{"sh"})
		if err != nil {
			t.Fatalf("width %d: new shell: %v", terminalWidth, err)
		}
		promptDeadline := time.Now().Add(time.Second)
		for time.Now().Before(promptDeadline) {
			if strings.Contains(sess.Screen().Render(), "$") {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if _, err := sess.Write([]byte(input)); err != nil {
			m.s.manager.CloseAll()
			t.Fatalf("width %d: write shell input: %v", terminalWidth, err)
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(strings.ReplaceAll(sess.Screen().Render(), "\n", ""), "readonly=off") {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		for paneWidth := 80; paneWidth <= terminalWidth; paneWidth++ {
			m.s.width = paneWidth
			m.s.resetViewport(0, paneWidth, 22)
			vp := m.s.viewports[0]
			m.s.setLiveContent(0, vp, sess)
			lines := m.s.selectionLines(0, vp)

			start, startCol, end, endCol := -1, -1, -1, -1
			remaining := len(input)
			for i := range lines {
				if start < 0 {
					if col := strings.Index(lines[i].Text, "sudo "); col >= 0 {
						start, startCol = i, col
					}
				}

				if start < 0 {
					continue
				}
				if i == start {
					remaining -= len(lines[i].Text) - startCol
				} else {
					remaining -= len(lines[i].Text)
				}
				if remaining <= 0 {
					end = i
					endCol = len(lines[i].Text) + remaining - 1
					break
				}
			}
			if start < 0 || end < start {
				m.s.manager.CloseAll()
				t.Fatalf("widths %d/%d: command rows not found: %#v", terminalWidth, paneWidth, lines)
			}
			m.s.sel = selection{
				startL: start, startC: startCol,
				endL: end, endC: endCol, hasRange: true,
			}
			if got := m.s.selectionText(); got != input {
				m.s.manager.CloseAll()
				t.Fatalf("widths %d/%d: selectionText() = %q, want %q; rows=%#v snapshot=%#v",
					terminalWidth, paneWidth, got, input, lines[start:end+1], m.s.liveLines[0])
			}
		}
		m.s.manager.CloseAll()
	}
}

func TestLiveSelectionPreservesMoreCRLFAndBoundarySpace(t *testing.T) {
	const input = `sudo ./cloud-hypervisor-41 --kernel vmlinux-6.8.0-52-generic --cpus boot=1 --memory size=2G --cmdline "root=/dev/vda console=hvc0 panic=0 ip=169.254.0.2::169.254.0.1:255.255.255.0:vm:eth0:off init=/sbin/init.sh" --disk path=./rootfs.ext4,readonly=off`
	path := filepath.Join(t.TempDir(), "pad.txt")
	if err := os.WriteFile(path, []byte(input+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	m := NewModel([]string{"more"}, 130, 44)
	m.s.manager = session.NewManager(130, 42, nil, nil)
	m.s.connections[0].manager = m.s.manager
	m.s.syncActiveConnectionFields()
	sess, err := m.s.manager.New([]string{"more", path})
	if err != nil {
		t.Fatalf("new more session: %v", err)
	}
	defer m.s.manager.CloseAll()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(sess.Screen().Render(), "readonly=off") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	m.s.ensureViewport(0, 130, 44)
	vp := m.s.viewports[0]
	m.s.setLiveContent(0, vp, sess)
	lines := m.s.selectionLines(0, vp)
	if len(lines) < 2 {
		t.Fatalf("more output rows = %#v", lines)
	}
	m.s.sel = selection{
		startL: 0, startC: 0,
		endL: 1, endC: len([]rune(lines[1].Text)) - 1,
		hasRange: true,
	}
	want := input[:130] + "\n" + input[130:]
	if got := m.s.selectionText(); got != want {
		t.Fatalf("selectionText() = %q, want %q; rows=%#v", got, want, lines[:2])
	}
}
