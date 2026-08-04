//go:build !windows

package localserver

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSocketDirUsesPrivateTempDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tmp, "runtime"))

	got, err := SocketDir()
	if err != nil {
		t.Fatalf("SocketDir: %v", err)
	}
	want := filepath.Join(tmp, "multicrum-"+strconv.Itoa(os.Getuid()))
	if got != want {
		t.Fatalf("SocketDir = %q, want %q", got, want)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat SocketDir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("SocketDir permissions = %o, want 700", perm)
	}
}

func TestSocketDirRejectsSymlink(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, "multicrum-"+strconv.Itoa(os.Getuid()))
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if _, err := SocketDir(); err == nil {
		t.Fatal("SocketDir accepted a symlink")
	}
}
