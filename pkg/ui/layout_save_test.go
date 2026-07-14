//go:build linux

package ui

import (
	"testing"

	"multicrum/pkg/session"
)

func TestSessionEntryForSavePersistsShellWorkingDirectory(t *testing.T) {
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	dir := t.TempDir()
	sess, err := manager.NewInDir([]string{"sh"}, dir)
	if err != nil {
		t.Fatalf("NewInDir: %v", err)
	}

	entry := sessionEntryForSave(sess)
	if entry.Cwd != dir {
		t.Fatalf("saved cwd = %q, want %q", entry.Cwd, dir)
	}
}

func TestSessionEntryForSaveOmitsWorkingDirectoryForCommands(t *testing.T) {
	sess := &session.Session{}
	entry := sessionEntryForSave(sess)
	if entry.Cwd != "" {
		t.Fatalf("non-shell cwd = %q, want empty", entry.Cwd)
	}
}
