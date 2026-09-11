//go:build !windows

package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVimPagingPreservesShellScrollback(t *testing.T) {
	vim, err := exec.LookPath("vim")
	if err != nil {
		t.Skip("vim is not installed")
	}
	path := filepath.Join(t.TempDir(), "scrollback-fixture.log")
	var content strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&content, "editor-only-entry-%04d\n", i)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := newSession(0, []string{
		"sh", "-c", `ls -l "$1"; printf 'before-editor\r\n'; "$2" -Nu NONE -i NONE -n "$1"; printf 'after-editor\r\n'`,
		"sh", path, vim,
	}, 80, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	sess.SendExit = func(ExitMsg) { close(exited) }
	if err := sess.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	waitForScreen := func(condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for Vim screen:\n%s", sess.Screen().Render())
	}
	write := func(input string) {
		t.Helper()
		if _, err := sess.Write([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	waitForScreen(func() bool {
		return sess.Screen().IsAltScreen() && strings.Contains(sess.Screen().Render(), "editor-only-entry-0000")
	})
	write("\x06\x06")
	waitForScreen(func() bool {
		return !strings.Contains(sess.Screen().Render(), "editor-only-entry-0000")
	})
	write("\x02\x02")
	waitForScreen(func() bool {
		return strings.Contains(sess.Screen().Render(), "editor-only-entry-0000")
	})
	write(":q!\r")
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("Vim did not exit")
	}
	history := sess.Screen().RenderWithScrollback()
	for _, want := range []string{"scrollback-fixture.log", "before-editor", "after-editor"} {
		if !strings.Contains(history, want) {
			t.Fatalf("shell history lost %q:\n%s", want, history)
		}
	}
	if strings.Contains(history, "editor-only-entry") {
		t.Fatalf("editor page text leaked into shell history:\n%s", history)
	}
	for _, line := range sess.Screen().BufferLines() {
		if strings.Contains(line.Text, "editor-only-entry") {
			t.Fatalf("editor page text leaked into copy buffer: %#v", line)
		}
	}
}
