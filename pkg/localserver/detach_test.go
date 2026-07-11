package localserver

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDetachRequiresAltCtrlQ(t *testing.T) {
	if isDetachSequence([]byte{0x11}) {
		t.Fatal("plain Ctrl+Q must be forwarded, not detach the client")
	}
	if !isDetachSequence([]byte{0x1b, 0x11}) {
		t.Fatal("Alt+Ctrl+Q must detach the client")
	}
}

func TestDetachCleanupRestoresTerminal(t *testing.T) {
	for _, sequence := range []string{
		ansi.ResetModeMouseButtonEvent,
		ansi.ResetModeMouseAnyEvent,
		ansi.ResetModeMouseExtSgr,
		ansi.ResetModeAltScreenSaveCursor,
		ansi.EraseEntireScreen,
		ansi.CursorHomePosition,
	} {
		if !strings.Contains(terminalCleanupSequence, sequence) {
			t.Fatalf("terminal cleanup is missing %q", sequence)
		}
	}
}
