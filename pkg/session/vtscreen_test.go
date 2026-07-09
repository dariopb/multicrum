package session

import (
	"strings"
	"testing"
)

func TestScrollbackStoresLogicalLinesAcrossResize(t *testing.T) {
	s := NewVTScreen(20, 2)
	s.Write([]byte("abcdefghijklmnopqrstuvwxyz\r\n"))
	s.Write([]byte("next\r\n"))

	lines := s.BufferLines()
	if len(lines) < 3 {
		t.Fatalf("BufferLines len = %d, want at least 3", len(lines))
	}
	if got := lines[0].Text; got != "abcdefghijklmnopqrst" {
		t.Fatalf("wrapped first row = %q, want %q", got, "abcdefghijklmnopqrst")
	}
	if !lines[0].SoftWrap {
		t.Fatalf("first row SoftWrap = false, want true")
	}
	if got := lines[1].Text; got != "uvwxyz" {
		t.Fatalf("wrapped second row = %q, want %q", got, "uvwxyz")
	}

	s.Resize(10, 2)
	lines = s.BufferLines()
	if got := []string{lines[0].Text, lines[1].Text, lines[2].Text}; !equalStrings(got, []string{"abcdefghij", "klmnopqrst", "uvwxyz"}) {
		t.Fatalf("narrow wrapped lines = %#v, want %#v", got, []string{"abcdefghij", "klmnopqrst", "uvwxyz"})
	}
	if !lines[0].SoftWrap || !lines[1].SoftWrap {
		t.Fatalf("narrow wrapped SoftWrap = %v/%v, want true/true", lines[0].SoftWrap, lines[1].SoftWrap)
	}

	s.Resize(20, 2)
	lines = s.BufferLines()
	if got := []string{lines[0].Text, lines[1].Text}; !equalStrings(got, []string{"abcdefghijklmnopqrst", "uvwxyz"}) {
		t.Fatalf("wide wrapped lines = %#v, want %#v", got, []string{"abcdefghijklmnopqrst", "uvwxyz"})
	}

	rendered := s.RenderWithScrollback()
	if !strings.Contains(rendered, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("RenderWithScrollback() = %q, want logical line preserved", rendered)
	}
	if strings.Contains(rendered, "abcdefghijklmnopqrst\nuvwxyz") {
		t.Fatalf("RenderWithScrollback() contains physical wrap break: %q", rendered)
	}
}

func TestBufferLinesPreservesLiveLogicalLineAcrossNarrowResize(t *testing.T) {
	s := NewVTScreen(20, 2)
	s.Write([]byte("abcdefghijklmnopqrstuvwxyz"))
	s.Resize(10, 2)

	lines := s.BufferLines()
	if got := []string{lines[0].Text, lines[1].Text, lines[2].Text}; !equalStrings(got, []string{"abcdefghij", "klmnopqrst", "uvwxyz"}) {
		t.Fatalf("narrow live lines = %#v, want %#v", got, []string{"abcdefghij", "klmnopqrst", "uvwxyz"})
	}

	s.Resize(20, 2)
	lines = s.BufferLines()
	if got := []string{lines[0].Text, lines[1].Text}; !equalStrings(got, []string{"abcdefghijklmnopqrst", "uvwxyz"}) {
		t.Fatalf("wide live lines = %#v, want %#v", got, []string{"abcdefghijklmnopqrst", "uvwxyz"})
	}
}

func TestRenderReflowsEmulatorAcrossResizeDownUp(t *testing.T) {
	s := NewVTScreen(20, 3)
	s.Write([]byte("hello world this is a long line here\r\nsecond line of text here too\r\n"))

	if got := s.Render(); !strings.Contains(got, "second line of text") {
		t.Fatalf("initial Render() = %q, want full text", got)
	}

	// Shrinking crops each row; the widened Render must restore the characters
	// the emulator dropped on the shrink (they were not simply hidden).
	s.Resize(10, 3)
	s.Resize(40, 3)

	got := s.Render()
	for _, want := range []string{"hello world this is a long line here", "second line of text here too"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render() after resize down/up = %q, want it to contain %q", got, want)
		}
	}
}

func TestReflowTailIsBoundedForLargeHistory(t *testing.T) {
	var raw []byte
	for i := 0; i < 5000; i++ {
		raw = append(raw, []byte("some line of terminal output here 0123456789\r\n")...)
	}
	tail := reflowTail(raw, 40)
	if len(tail) >= len(raw) {
		t.Fatalf("reflowTail returned %d of %d bytes, want a small suffix", len(tail), len(raw))
	}
	// Must still cover at least a screenful of rows for correct reconstruction.
	if got := strings.Count(string(tail), "\n"); got < 40 {
		t.Fatalf("reflowTail covers %d lines, want >= 40", got)
	}
	if len(tail) > 96*1024 {
		t.Fatalf("reflowTail returned %d bytes, exceeds cap", len(tail))
	}
}

func TestLogicalCaptureIgnoresCursorMovementEscapes(t *testing.T) {
	s := NewVTScreen(80, 4)
	s.Write([]byte("abc\x1b[2K\r\n"))
	s.Write([]byte("def\r\n"))
	s.Write([]byte("ghi\r\n"))

	rendered := s.RenderWithScrollback()
	if strings.Contains(rendered, "\x1b[2K") {
		t.Fatalf("RenderWithScrollback() leaked cursor-control escape: %q", rendered)
	}
	if !strings.Contains(rendered, "abc") {
		t.Fatalf("RenderWithScrollback() = %q, want printable text preserved", rendered)
	}
}

// TestScrollbackBareCarriageReturnOverwritesLine verifies that a lone CR
// (prompt redraw / progress bar) overwrites the current line in place instead
// of committing a duplicate logical line, matching the live emulator screen.
func TestScrollbackBareCarriageReturnOverwritesLine(t *testing.T) {
	s := NewVTScreen(60, 6)
	s.Write([]byte("done\r\n"))
	s.Write([]byte("user@host:~$ "))
	for i := 0; i < 5; i++ {
		s.Write([]byte("\ruser@host:~$ \x1b[K"))
	}
	rendered := s.RenderWithScrollback()
	if got := strings.Count(rendered, "user@host"); got != 1 {
		t.Fatalf("prompt appears %d times in scrollback, want 1:\n%s", got, rendered)
	}
}

func TestScrollbackCRLFOnlyCreatesOneLogicalLine(t *testing.T) {
	s := NewVTScreen(80, 2)
	s.Write([]byte("first\r\nsecond\r\nthird\r\n"))

	lines := s.BufferLines()
	if len(lines) < 3 {
		t.Fatalf("BufferLines len = %d, want at least 3", len(lines))
	}
	if got := lines[0].Text; got != "first" {
		t.Fatalf("first scrollback line = %q, want first", got)
	}
	if strings.Contains(strings.Join([]string{lines[0].Text, lines[1].Text}, "\n"), "\n\n") {
		t.Fatalf("CRLF produced an extra blank copied line: %#v", lines[:2])
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
