package localserver

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAttachedClipboardFallsBackToOSC52(t *testing.T) {
	t.Setenv("TMUX", "")
	var output bytes.Buffer
	writeAttachedClipboard(&output, []byte("selected"))

	encoded := base64.StdEncoding.EncodeToString([]byte("selected"))
	if !strings.Contains(output.String(), "\x1b]52;c;"+encoded+"\x07") {
		t.Fatalf("clipboard output = %q, want OSC 52 payload", output.String())
	}
}

func TestAttachedClipboardUsesClientTmuxEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture")
	tmux := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nprintf '%s' \"$4\" > \"$CAPTURE\"\n"
	if err := os.WriteFile(tmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TMUX", "/tmp/client-tmux")
	t.Setenv("CAPTURE", capture)

	var output bytes.Buffer
	writeAttachedClipboard(&output, []byte("selected"))

	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("tmux clipboard command was not invoked: %v", err)
	}
	if string(got) != "selected" {
		t.Fatalf("tmux clipboard text = %q, want %q", got, "selected")
	}
	if output.Len() != 0 {
		t.Fatalf("successful tmux copy also wrote fallback output: %q", output.String())
	}
}
