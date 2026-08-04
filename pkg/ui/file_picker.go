package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type filePickerEntry struct {
	name  string
	path  string
	isDir bool
}

type filePickerState struct {
	dir     string
	entries []filePickerEntry
	cursor  int
	scroll  int
	err     string
}

func defaultSSHDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	sshDir := filepath.Join(home, ".ssh")
	if info, err := os.Stat(sshDir); err == nil && info.IsDir() {
		return sshDir
	}
	return home
}

func expandHomePath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func (s *state) openSSHKeyPicker() {
	start := defaultSSHDirectory()
	if key := strings.TrimSpace(s.newSession.key); key != "" {
		key = expandHomePath(key)
		if info, err := os.Stat(key); err == nil {
			if info.IsDir() {
				start = key
			} else {
				start = filepath.Dir(key)
			}
		}
	}
	s.filePicker = filePickerState{}
	s.loadFilePickerDirectory(start)
	s.mode = modeFilePicker
}

func (s *state) loadFilePickerDirectory(dir string) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		s.filePicker.err = err.Error()
		return
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		s.filePicker.err = err.Error()
		return
	}

	items := make([]filePickerEntry, 0, len(entries)+1)
	parent := filepath.Dir(absolute)
	if parent != absolute {
		items = append(items, filePickerEntry{name: "..", path: parent, isDir: true})
	}
	for _, entry := range entries {
		path := filepath.Join(absolute, entry.Name())
		info, statErr := os.Stat(path)
		items = append(items, filePickerEntry{
			name:  entry.Name(),
			path:  path,
			isDir: statErr == nil && info.IsDir(),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].name == ".." {
			return true
		}
		if items[j].name == ".." {
			return false
		}
		if items[i].isDir != items[j].isDir {
			return items[i].isDir
		}
		return strings.ToLower(items[i].name) < strings.ToLower(items[j].name)
	})

	s.filePicker.dir = absolute
	s.filePicker.entries = items
	s.filePicker.cursor = 0
	if len(items) > 1 && items[0].name == ".." {
		s.filePicker.cursor = 1
	}
	s.filePicker.scroll = 0
	s.filePicker.err = ""
	s.ensureFilePickerCursorVisible()
}

func (s *state) filePickerListRows() int {
	return max(4, min(12, s.height-10))
}

func (s *state) ensureFilePickerCursorVisible() {
	rows := s.filePickerListRows()
	if s.filePicker.cursor < s.filePicker.scroll {
		s.filePicker.scroll = s.filePicker.cursor
	}
	if s.filePicker.cursor >= s.filePicker.scroll+rows {
		s.filePicker.scroll = s.filePicker.cursor - rows + 1
	}
	maxScroll := max(0, len(s.filePicker.entries)-rows)
	if s.filePicker.scroll > maxScroll {
		s.filePicker.scroll = maxScroll
	}
}

func (s *state) handleFilePickerKey(msg tea.KeyPressMsg) {
	key := msg.Key()
	switch key.Code {
	case tea.KeyEscape:
		s.filePicker = filePickerState{}
		s.mode = modeNewSession
	case tea.KeyEnter, tea.KeyRight:
		s.chooseFilePickerEntry()
	case tea.KeyBackspace, tea.KeyLeft:
		s.loadFilePickerDirectory(filepath.Dir(s.filePicker.dir))
	case tea.KeyUp:
		if s.filePicker.cursor > 0 {
			s.filePicker.cursor--
			s.ensureFilePickerCursorVisible()
		}
	case tea.KeyDown:
		if s.filePicker.cursor+1 < len(s.filePicker.entries) {
			s.filePicker.cursor++
			s.ensureFilePickerCursorVisible()
		}
	case tea.KeyHome:
		s.filePicker.cursor = 0
		s.ensureFilePickerCursorVisible()
	case tea.KeyEnd:
		if len(s.filePicker.entries) > 0 {
			s.filePicker.cursor = len(s.filePicker.entries) - 1
			s.ensureFilePickerCursorVisible()
		}
	case tea.KeyPgUp:
		s.filePicker.cursor = max(0, s.filePicker.cursor-s.filePickerListRows())
		s.ensureFilePickerCursorVisible()
	case tea.KeyPgDown:
		if len(s.filePicker.entries) == 0 {
			return
		}
		s.filePicker.cursor = min(len(s.filePicker.entries)-1, s.filePicker.cursor+s.filePickerListRows())
		s.ensureFilePickerCursorVisible()
	}
}

func (s *state) chooseFilePickerEntry() {
	if s.filePicker.cursor < 0 || s.filePicker.cursor >= len(s.filePicker.entries) {
		return
	}
	entry := s.filePicker.entries[s.filePicker.cursor]
	if entry.isDir {
		s.loadFilePickerDirectory(entry.path)
		return
	}
	s.newSession.key = entry.path
	s.newSession.keyCur = len([]rune(entry.path))
	s.newSession.choice = 2
	s.newSession.field = newFieldKey
	s.filePicker = filePickerState{}
	s.mode = modeNewSession
}

func (m Model) renderFilePickerModal() string {
	s := m.s
	const width = 72
	rows := []string{
		"Select SSH key file",
		"",
		"Directory: " + truncateLeft(s.filePicker.dir, width-11),
		"",
	}
	listRows := s.filePickerListRows()
	for row := 0; row < listRows; row++ {
		index := s.filePicker.scroll + row
		line := ""
		if index < len(s.filePicker.entries) {
			entry := s.filePicker.entries[index]
			name := entry.name
			if entry.isDir {
				name = "/" + name
			}
			line = truncate(name, width)
			if index == s.filePicker.cursor {
				line = selectorActiveStyle.Render(line + strings.Repeat(" ", max(0, width-lipglossWidth(line))))
			} else if entry.isDir {
				line = filePickerDirStyle.Render(line)
			}
		}
		rows = append(rows, line)
	}
	if s.filePicker.err != "" {
		rows = append(rows, "", "Error: "+truncate(s.filePicker.err, width-7))
	}
	rows = append(rows, "", "Up/Down select   Enter open/select   Backspace parent   Esc cancel")
	return padBox(rows, width)
}

func truncateLeft(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-width+1:])
}
