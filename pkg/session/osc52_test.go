package session

import (
	"encoding/base64"
	"testing"
)

func TestOSC52DecoderHandlesSplitTerminators(t *testing.T) {
	var decoder osc52Decoder
	encoded := base64.StdEncoding.EncodeToString([]byte("selected text"))

	if got := decoder.Write([]byte("before\x1b]52;c;" + encoded[:4])); len(got) != 0 {
		t.Fatalf("incomplete OSC 52 produced %q", got)
	}
	if got := decoder.Write([]byte(encoded[4:] + "\x1b")); len(got) != 0 {
		t.Fatalf("incomplete ST terminator produced %q", got)
	}
	got := decoder.Write([]byte("\\after"))
	if len(got) != 1 || got[0] != "selected text" {
		t.Fatalf("decoded clipboard = %q, want %q", got, "selected text")
	}
}

func TestOSC52DecoderHandlesBELAndIgnoresQueries(t *testing.T) {
	var decoder osc52Decoder
	first := base64.StdEncoding.EncodeToString([]byte("one"))
	second := base64.RawStdEncoding.EncodeToString([]byte("two"))
	input := "\x1b]52;c;" + first + "\x07" +
		"\x1b]52;c;?\x07" +
		"\x1b]52;p;" + second + "\x07"

	got := decoder.Write([]byte(input))
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("decoded clipboards = %q, want [one two]", got)
	}
}

func TestOSC52DecoderRecoversAfterUnrelatedOSC(t *testing.T) {
	var decoder osc52Decoder
	encoded := base64.StdEncoding.EncodeToString([]byte("copied"))
	got := decoder.Write([]byte("\x1b]0;title\x07text\x1b]52;c;" + encoded + "\x07"))
	if len(got) != 1 || got[0] != "copied" {
		t.Fatalf("decoded clipboard = %q, want [copied]", got)
	}
}
