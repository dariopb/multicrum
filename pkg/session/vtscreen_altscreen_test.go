package session

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

func TestAlternateScreenPreservesShellScrollback(t *testing.T) {
	var listing bytes.Buffer
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&listing, "-rw-r--r-- file-%02d.txt\r\n", i)
	}
	listing.WriteString("$ vi syslog\r\n")
	const pages = "\x1b[2J\x1b[Heditor-page-one\r\neditor-line-two" +
		"\x1b[3J\x1b[2J\x1b[Heditor-page-down\r\neditor-line-three" +
		"\x1b[H\x1b[2Jeditor-page-up\r\neditor-status"
	for _, mode := range []string{"1047", "1049", "25;1049"} {
		for _, chunkSize := range []int{1, 7, 4096} {
			t.Run(fmt.Sprintf("mode=%s/chunk=%d", mode, chunkSize), func(t *testing.T) {
				raw := []byte(listing.String() + "\x1b[?" + mode + "h" +
					pages + "\x1b[?" + mode + "l$ ")
				s := NewVTScreen(40, 6)
				for start := 0; start < len(raw); start += chunkSize {
					s.Write(raw[start:min(start+chunkSize, len(raw))])
				}
				want := NewVTScreen(40, 6)
				want.Write(append(listing.Bytes(), []byte("$ ")...))
				if got := s.RenderWithScrollback(); got != want.RenderWithScrollback() {
					t.Fatalf("shell history changed after editor exit:\ngot: %q\nwant: %q", got, want.RenderWithScrollback())
				}
				if !reflect.DeepEqual(s.BufferLines(), want.BufferLines()) {
					t.Fatalf("copy buffer changed after editor exit:\ngot: %#v\nwant: %#v", s.BufferLines(), want.BufferLines())
				}
				if !reflect.DeepEqual(s.observedLines, want.observedLines) || s.observedPlain.String() != want.observedPlain.String() {
					t.Fatal("editor output contaminated observed shell lines")
				}
				if !bytes.Equal(s.RawSnapshot(), raw) {
					t.Fatal("alternate-screen filtering changed browser replay")
				}
			})
		}
	}
}

func TestAlternateScreenPreservesPendingShellLine(t *testing.T) {
	const before = "\x1b[32mwrapped shell output before editor\r"
	const after = "\nnext shell line"
	s := NewVTScreen(20, 4)
	s.Write([]byte(before))
	s.Write([]byte("\x1b[?1049h\x1b[0m\x1b[2Jeditor\r\n\x1b[?1049hmore editor"))
	s.Write([]byte("\x1b[?1049l" + after))
	want := NewVTScreen(20, 4)
	want.Write([]byte(before + after))
	if got := s.RenderWithScrollback(); got != want.RenderWithScrollback() {
		t.Fatalf("pending shell line changed: got %q, want %q", got, want.RenderWithScrollback())
	}
}

func TestAlternateScreenResizePreservesMainHistory(t *testing.T) {
	const shell = "long shell line that wraps at the narrower width\r\n$ "
	s := NewVTScreen(40, 6)
	s.Write([]byte(shell + "\x1b[?1049h\x1b[2Jeditor page"))
	s.Resize(20, 4)
	s.Write([]byte("\x1b[2Jdifferent editor page\x1b[?1049l"))
	want := NewVTScreen(40, 6)
	want.Write([]byte(shell))
	want.Resize(20, 4)
	if got := s.RenderWithScrollback(); got != want.RenderWithScrollback() {
		t.Fatalf("resize changed shell history: got %q, want %q", got, want.RenderWithScrollback())
	}
	if !reflect.DeepEqual(s.BufferLines(), want.BufferLines()) {
		t.Fatal("resize changed wrapped shell copy rows")
	}
}

func TestResetFromAlternateScreenResumesMainCapture(t *testing.T) {
	s := NewVTScreen(40, 6)
	s.Write([]byte("old shell\r\n\x1b[?1049heditor page\x1bcfresh shell\r\n"))
	want := NewVTScreen(40, 6)
	want.Write([]byte("fresh shell\r\n"))
	if got := s.RenderWithScrollback(); got != want.RenderWithScrollback() {
		t.Fatalf("reset did not resume fresh shell capture: got %q, want %q", got, want.RenderWithScrollback())
	}
}
