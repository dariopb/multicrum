package localserver

import (
	"net"
	"strings"
	"testing"
	"time"

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

func TestDetachActiveClientClosesOnlyInputSource(t *testing.T) {
	activeServer, activePeer := net.Pipe()
	otherServer, otherPeer := net.Pipe()
	defer activePeer.Close()
	defer otherServer.Close()
	defer otherPeer.Close()

	active := &client{conn: activeServer}
	other := &client{conn: otherServer}
	owner := &Owner{
		clients:      map[*client]struct{}{active: {}, other: {}},
		activeClient: active,
	}

	owner.DetachActiveClient()

	_ = activePeer.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := activePeer.Read(make([]byte, 1)); err == nil {
		t.Fatal("active client connection remained open")
	}
	_ = otherPeer.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := otherPeer.Read(make([]byte, 1)); err == nil {
		t.Fatal("unexpected data from untouched client")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("non-active client was closed: %v", err)
	}
}
