//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLocalShellStartsAndTracksWorkingDirectory(t *testing.T) {
	manager := NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	start := t.TempDir()
	sess, err := manager.NewInDir([]string{"sh"}, start)
	if err != nil {
		t.Fatalf("NewInDir: %v", err)
	}
	if got, err := sess.CurrentDirectory(); err != nil || got != start {
		t.Fatalf("initial cwd = %q, %v; want %q", got, err, start)
	}

	next := t.TempDir()
	if _, err := sess.Write([]byte("cd " + next + "\r")); err != nil {
		t.Fatalf("cd: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := sess.CurrentDirectory()
		if err == nil && got == next {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cwd did not change to %q; last value %q, %v", next, got, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInvalidSavedWorkingDirectoryDoesNotBlockStartup(t *testing.T) {
	manager := NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := manager.NewInDir([]string{"sh"}, missing); err != nil {
		t.Fatalf("invalid saved cwd blocked shell startup: %v", err)
	}
}
