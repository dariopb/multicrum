package localserver

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, FrameInput, []byte("abc")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	typ, body, err := ReadFrame(&b)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if typ != FrameInput || string(body) != "abc" {
		t.Fatalf("frame mismatch typ=%d body=%q", typ, body)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0x00, 0x10, 0x00, 0x01})
	if _, _, err := ReadFrame(&b); err == nil {
		t.Fatal("expected oversized frame error")
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("work/prod server"); got != "work_prod_server" {
		t.Fatalf("sanitize mismatch: %q", got)
	}
}
