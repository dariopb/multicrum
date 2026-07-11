package localserver

import "testing"

func TestDetachRequiresAltCtrlQ(t *testing.T) {
	if isDetachSequence([]byte{0x11}) {
		t.Fatal("plain Ctrl+Q must be forwarded, not detach the client")
	}
	if !isDetachSequence([]byte{0x1b, 0x11}) {
		t.Fatal("Alt+Ctrl+Q must detach the client")
	}
}
