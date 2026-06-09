package ui

import (
	"fmt"
	"strings"

	"multicrum/pkg/config"
)

func sanitizePaste(in string) string {
	var b strings.Builder
	b.Grow(len(in))
	for _, r := range in {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func (s *state) saveLayout() {
	if s.configPath == "" {
		s.statusMsg = "save layout: no --config path set"
		return
	}
	connections := make([]config.ConnectionEntry, 0, len(s.connections))
	total := 0
	for _, conn := range s.connections {
		if conn.manager == nil {
			continue
		}
		sessions := conn.manager.Sessions()
		entries := make([]config.SessionEntry, 0, len(sessions))
		for _, sess := range sessions {
			entry := config.SessionEntry{Title: sess.Title()}
			if line := sess.CmdLine(); line != "" {
				entry.CmdLine = line
			} else {
				entry.Cmd = sess.Cmd()
			}
			if sshCfg, ok := sess.SSHConfig(); ok {
				entry.SSH = sshEntryFromResolved(sshCfg)
			}
			entries = append(entries, entry)
		}
		total += len(entries)
		connections = append(connections, config.ConnectionEntry{Name: conn.name, Sessions: entries})
	}
	active := ""
	if c := s.activeConnection(); c != nil {
		active = c.name
	}
	cfg := &config.Config{Server: s.serverName, ActiveConnection: active, Connections: connections}
	if err := config.Save(s.configPath, cfg); err != nil {
		s.statusMsg = fmt.Sprintf("save layout failed: %v", err)
		return
	}
	s.statusMsg = fmt.Sprintf("layout saved to %s (%d connections, %d sessions)", s.configPath, len(connections), total)
}
