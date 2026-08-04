package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func filePickerTestModel(t *testing.T) (*Model, string) {
	t.Helper()
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(filepath.Join(sshDir, "nested"), 0o700); err != nil {
		t.Fatalf("create SSH directory: %v", err)
	}
	keyPath := filepath.Join(sshDir, "id_test")
	if err := os.WriteFile(keyPath, []byte("test key"), 0o600); err != nil {
		t.Fatalf("create key file: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	m := NewModel([]string{"sh"}, 100, 30)
	m.s.openNewSessionModal([]string{"sh"})
	m.s.newSession.choice = 2
	m.s.newSession.field = newFieldKey
	return m, keyPath
}

func TestSSHKeyFieldEnterOpensFilePicker(t *testing.T) {
	m, keyPath := filePickerTestModel(t)

	m.s.handleNewSessionKey(*m, enterKey())

	if m.s.mode != modeFilePicker {
		t.Fatalf("mode = %v, want file picker", m.s.mode)
	}
	if got, want := m.s.filePicker.dir, filepath.Dir(keyPath); got != want {
		t.Fatalf("picker directory = %q, want %q", got, want)
	}
	if box := m.renderFilePickerModal(); !strings.Contains(box, "Select SSH key file") || !strings.Contains(box, "\x1b[") {
		t.Fatalf("file picker did not use themed modal rendering: %q", box)
	}
	box := m.renderFilePickerModal()
	if !strings.Contains(box, "/..") || !strings.Contains(box, "/nested") {
		t.Fatalf("directories do not use slash-prefixed MC style: %q", box)
	}
	if !strings.Contains(box, filePickerDirStyle.Render("/..")) {
		t.Fatalf("unselected directory does not use the lighter directory style: %q", box)
	}
	if strings.Contains(box, "D  ") || strings.Contains(box, "F  ") {
		t.Fatalf("file picker still uses D/F type markers: %q", box)
	}
}

func TestFilePickerNavigatesAndSelectsKey(t *testing.T) {
	m, keyPath := filePickerTestModel(t)
	m.s.openSSHKeyPicker()

	nested := filePickerEntryIndex(t, m, "nested")
	m.s.filePicker.cursor = nested
	m.s.handleFilePickerKey(enterKey())
	if got, want := m.s.filePicker.dir, filepath.Join(filepath.Dir(keyPath), "nested"); got != want {
		t.Fatalf("navigated directory = %q, want %q", got, want)
	}

	m.s.handleFilePickerKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	key := filePickerEntryIndex(t, m, filepath.Base(keyPath))
	m.s.filePicker.cursor = key
	m.s.handleFilePickerKey(enterKey())

	if m.s.mode != modeNewSession {
		t.Fatalf("mode after selection = %v, want new-session modal", m.s.mode)
	}
	if m.s.newSession.key != keyPath {
		t.Fatalf("selected key = %q, want %q", m.s.newSession.key, keyPath)
	}
}

func TestFilePickerMouseSelectsKey(t *testing.T) {
	m, keyPath := filePickerTestModel(t)
	m.s.openSSHKeyPicker()
	index := filePickerEntryIndex(t, m, filepath.Base(keyPath))
	m.s.filePicker.cursor = index
	m.s.ensureFilePickerCursorVisible()

	row := 4 + index - m.s.filePicker.scroll
	m.s.handleModalMouse(*m, modalClick(m, 0, row))

	if m.s.mode != modeNewSession || m.s.newSession.key != keyPath {
		t.Fatalf("mouse selection mode=%v key=%q, want new-session/%q", m.s.mode, m.s.newSession.key, keyPath)
	}
}

func filePickerEntryIndex(t *testing.T, m *Model, name string) int {
	t.Helper()
	for i, entry := range m.s.filePicker.entries {
		if entry.name == name {
			return i
		}
	}
	t.Fatalf("file picker entry %q not found", name)
	return -1
}
