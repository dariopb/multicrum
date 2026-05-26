package ui

import (
	"bytes"
	"testing"
)

func strip(t *testing.T, in []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewKeyboardStripWriter(&buf)
	if _, err := w.Write(in); err != nil {
		t.Fatalf("write: %v", err)
	}
	return buf.Bytes()
}

func TestKeyboardStripDropsKnownSequences(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"SetModifyOtherKeys2", []byte("\x1b[>4;2m")},
		{"ResetModifyOtherKeys", []byte("\x1b[>4m")},
		{"RequestKittyKeyboard", []byte("\x1b[?u")},
		{"KittyKeyboardSet", []byte("\x1b[=1;1u")},
		{"KittyKeyboardSetZero", []byte("\x1b[=0;1u")},
		{"PushKittyKeyboard", []byte("\x1b[>1u")},
		{"PushKittyKeyboardEmpty", []byte("\x1b[>u")},
		{"PopKittyKeyboard", []byte("\x1b[<u")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strip(t, tc.in)
			if len(got) != 0 {
				t.Fatalf("expected sequence stripped, got %q", got)
			}
		})
	}
}

func TestKeyboardStripPreservesOtherSequences(t *testing.T) {
	cases := [][]byte{
		[]byte("hello"),
		[]byte("\x1b[31mred\x1b[0m"),         // SGR
		[]byte("\x1b[2J\x1b[H"),              // clear + home
		[]byte("\x1b[?1049h"),                // alt screen
		[]byte("\x1b[?2004h"),                // bracketed paste enable
		[]byte("\x1b[1;5u"),                  // CSI u with kitty-keyboard-style report from child (no = > < ?)
		[]byte("\x1b]0;title\x07"),           // OSC
		[]byte("plain \x1b[1m bold \x1b[0m"), // mixed
	}
	for _, in := range cases {
		got := strip(t, in)
		if !bytes.Equal(got, in) {
			t.Fatalf("input %q mutated to %q", in, got)
		}
	}
}

func TestKeyboardStripMixed(t *testing.T) {
	in := []byte("before\x1b[>4;2mmiddle\x1b[?utail")
	want := []byte("beforemiddletail")
	got := strip(t, in)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestKeyboardStripSplitAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewKeyboardStripWriter(&buf)
	// Split the SetModifyOtherKeys2 sequence across two writes.
	if _, err := w.Write([]byte("a\x1b[>4")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(";2mb")); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "ab"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestKeyboardStripReportsWrittenLen(t *testing.T) {
	w := NewKeyboardStripWriter(&bytes.Buffer{})
	in := []byte("x\x1b[>4my")
	n, err := w.Write(in)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(in) {
		t.Fatalf("Write returned %d, want %d", n, len(in))
	}
}
