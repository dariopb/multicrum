package ui

import (
	"fmt"
	"strings"

	"multicrum/pkg/config"
)

// sanitizePaste strips control characters from pasted text so it can be
// safely inserted into single-line modal fields. Newlines and tabs are
// collapsed to spaces; other control bytes are dropped. Trailing
// whitespace is trimmed because terminals typically append a newline to
// the paste payload.
func sanitizePaste(in string) string {
	var b strings.Builder
	b.Grow(len(in))
	for _, r := range in {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// drop other C0 controls
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// saveLayout writes the current session list to s.configPath, recording the
// title (if user-set) and the command the session was started with. The
// result is reflected in s.statusMsg so the user sees feedback.
func (s *state) saveLayout() {
	if s.configPath == "" {
		s.statusMsg = "save layout: no --config path set"
		return
	}
	sessions := s.manager.Sessions()
	entries := make([]config.SessionEntry, 0, len(sessions))
	for _, sess := range sessions {
		entry := config.SessionEntry{Title: sess.Title()}
		if line := sess.CmdLine(); line != "" {
			entry.CmdLine = line
		} else {
			entry.Cmd = sess.Cmd()
		}
		entries = append(entries, entry)
	}
	cfg := &config.Config{Sessions: entries}
	if err := config.Save(s.configPath, cfg); err != nil {
		s.statusMsg = fmt.Sprintf("save layout failed: %v", err)
		return
	}
	s.statusMsg = fmt.Sprintf("layout saved to %s (%d sessions)", s.configPath, len(entries))
}
