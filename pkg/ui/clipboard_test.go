package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestClipboardOutputUsesConfiguredWriter(t *testing.T) {
	t.Setenv("TMUX", "")
	m := NewModel([]string{"bash"}, 80, 24)
	var output bytes.Buffer
	m.SetClipboardOutput(&output)

	m.s.writeClipboard("selected")

	got := output.String()
	osc := buildOSC52("selected")
	if !strings.HasPrefix(got, osc) || !strings.Contains(got, "\x1bPtmux;\x1b\x1b]52;") {
		t.Fatalf("clipboard output lacks direct or tmux OSC 52: %q", got)
	}
}

func TestClipboardOutputDoesNotDependOnOwnerTmuxEnvironment(t *testing.T) {
	var sequences []string
	for _, tmux := range []string{"", "/tmp/tmux-test"} {
		t.Setenv("TMUX", tmux)
		sequences = append(sequences, clipboardOutputSequence("selected"))
	}
	if sequences[0] != sequences[1] {
		t.Fatalf("clipboard output depends on owner TMUX environment:\nplain %q\ntmux  %q", sequences[0], sequences[1])
	}
	if !strings.HasSuffix(sequences[0], "\x1b\\") {
		t.Fatalf("clipboard output lacks tmux passthrough terminator: %q", sequences[0])
	}
}
