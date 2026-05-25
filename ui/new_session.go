package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"multicrum/ssh_client"
)

type newSessionField int

type newSessionState struct {
	choice int // 0 same, 1 local, 2 remote
	field  newSessionField
	local  string
	target string
	passwd string
	key    string
	cmd    string
	err    string
}

const (
	newFieldLocal newSessionField = iota
	newFieldTarget
	newFieldPasswd
	newFieldKey
	newFieldRemoteCmd
)

func (s *state) openNewSessionModal(defaultCmd []string) {
	local := strings.Join(defaultCmd, " ")
	cfg := ssh_client.ResolvedConfig{}
	if s.sshClient != nil {
		cfg = s.sshClient.Config()
	}
	target := ""
	remoteCmd := ""
	key := ""
	if cfg.Host != "" {
		target = cfg.User + "@" + cfg.Host
		if cfg.Port != "" && cfg.Port != "22" {
			target += ":" + cfg.Port
		}
		remoteCmd = strings.Join(cfg.Command, " ")
		if len(cfg.IdentityFiles) > 0 {
			key = cfg.IdentityFiles[0]
		}
	}
	s.newSession = newSessionState{
		choice: 0,
		field:  newFieldLocal,
		local:  local,
		target: target,
		key:    key,
		cmd:    remoteCmd,
	}
	s.mode = modeNewSession
}

func (s *state) handleNewSessionKey(m Model, msg tea.KeyPressMsg) tea.Cmd {
	ns := &s.newSession
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case 'h':
			s.backspaceNewSessionField()
			return nil
		case 'u':
			s.clearNewSessionField()
			return nil
		}
	}
	if key.Text != "" {
		s.appendNewSessionField(key.Text)
		return nil
	}
	switch key.Code {
	case tea.KeyEscape:
		s.mode = modeNormal
		return nil
	case tea.KeyEnter:
		return s.resolveNewSession(m)
	case tea.KeyUp:
		if ns.choice > 0 {
			ns.choice--
		}
		return nil
	case tea.KeyDown:
		if ns.choice < 2 {
			ns.choice++
		}
		return nil
	case tea.KeyLeft:
		if ns.choice > 0 {
			ns.choice--
		}
		return nil
	case tea.KeyRight:
		if ns.choice < 2 {
			ns.choice++
		}
		return nil
	case tea.KeyTab:
		s.advanceNewSessionField(msg.Key().Mod.Contains(tea.ModShift))
		return nil
	case tea.KeyBackspace:
		s.backspaceNewSessionField()
		return nil
	case tea.KeySpace:
		s.appendNewSessionField(" ")
		return nil
	}
	switch msg.String() {
	case "1":
		ns.choice = 0
	case "2":
		ns.choice = 1
	case "3":
		ns.choice = 2
	}
	return nil
}

func (s *state) advanceNewSessionField(reverse bool) {
	fields := []newSessionField{newFieldLocal, newFieldTarget, newFieldPasswd, newFieldKey, newFieldRemoteCmd}
	idx := 0
	for i, f := range fields {
		if f == s.newSession.field {
			idx = i
			break
		}
	}
	if reverse {
		idx = (idx - 1 + len(fields)) % len(fields)
	} else {
		idx = (idx + 1) % len(fields)
	}
	s.newSession.field = fields[idx]
	if s.newSession.field == newFieldLocal {
		s.newSession.choice = 1
	} else if s.newSession.field != newFieldLocal {
		s.newSession.choice = 2
	}
}

func (s *state) appendNewSessionField(text string) {
	switch s.newSession.field {
	case newFieldLocal:
		s.newSession.local += text
		s.newSession.choice = 1
	case newFieldTarget:
		s.newSession.target += text
		s.newSession.choice = 2
	case newFieldPasswd:
		s.newSession.passwd += text
		s.newSession.choice = 2
	case newFieldKey:
		s.newSession.key += text
		s.newSession.choice = 2
	case newFieldRemoteCmd:
		s.newSession.cmd += text
		s.newSession.choice = 2
	}
}

func (s *state) backspaceNewSessionField() {
	trim := func(v string) string {
		r := []rune(v)
		if len(r) == 0 {
			return v
		}
		return string(r[:len(r)-1])
	}
	switch s.newSession.field {
	case newFieldLocal:
		s.newSession.local = trim(s.newSession.local)
	case newFieldTarget:
		s.newSession.target = trim(s.newSession.target)
	case newFieldPasswd:
		s.newSession.passwd = trim(s.newSession.passwd)
	case newFieldKey:
		s.newSession.key = trim(s.newSession.key)
	case newFieldRemoteCmd:
		s.newSession.cmd = trim(s.newSession.cmd)
	}
}

func (s *state) clearNewSessionField() {
	switch s.newSession.field {
	case newFieldLocal:
		s.newSession.local = ""
	case newFieldTarget:
		s.newSession.target = ""
	case newFieldPasswd:
		s.newSession.passwd = ""
	case newFieldKey:
		s.newSession.key = ""
	case newFieldRemoteCmd:
		s.newSession.cmd = ""
	}
}

func (s *state) resolveNewSession(m Model) tea.Cmd {
	ns := s.newSession
	s.mode = modeNormal
	s.newSession = newSessionState{}
	var err error
	switch ns.choice {
	case 0:
		_, err = s.manager.New(m.agentCmd)
	case 1:
		cmd := strings.Fields(strings.TrimSpace(ns.local))
		if len(cmd) == 0 {
			cmd = m.agentCmd
		}
		_, err = s.manager.NewWithSSH(cmd, nil)
	case 2:
		cmd := strings.Fields(strings.TrimSpace(ns.cmd))
		client, cfgErr := ssh_client.New(ssh_client.Options{
			Target:                strings.TrimSpace(ns.target),
			IdentityFile:          strings.TrimSpace(ns.key),
			Password:              ns.passwd,
			UseDefaultKeys:        strings.TrimSpace(ns.key) == "" && ns.passwd == "",
			UseAgent:              strings.TrimSpace(ns.key) == "" && ns.passwd == "",
			InsecureIgnoreHostKey: false,
			Command:               cmd,
		})
		if cfgErr != nil {
			err = cfgErr
		} else {
			_, err = s.manager.NewWithSSH(cmd, client)
		}
	}
	if err != nil {
		ns.err = err.Error()
		s.mode = modeNewSession
		s.newSession = ns
		return nil
	}
	s.errMsg = ""
	s.resetViewport(s.manager.FocusedIndex(), s.width, s.height)
	s.notifyMeta()
	return nil
}

func (m Model) renderNewSessionModal() string {
	s := m.s
	ns := s.newSession
	width := 76
	choice := func(idx int, label string) string {
		if ns.choice == idx {
			return exitChoiceActiveStyle.Render(label)
		}
		return exitChoiceInactiveStyle.Render(label)
	}
	field := func(f newSessionField, label, value string, secret bool) string {
		if secret && value != "" {
			value = strings.Repeat("*", len([]rune(value)))
		}
		cursor := ""
		if ns.field == f {
			cursor = "█"
		}
		return label + truncate(value, width-lipglossWidth(label)-1) + cursor
	}
	rows := []string{
		"New session",
		"",
		choice(0, "[ Same as current/default ]") + "   " + choice(1, "[ Local command ]") + "   " + choice(2, "[ Remote SSH ]"),
		"",
		field(newFieldLocal, "Local cmd:  ", ns.local, false),
		"",
		field(newFieldTarget, "SSH target: ", ns.target, false),
		field(newFieldPasswd, "Password:   ", ns.passwd, true),
		field(newFieldKey, "Key file:   ", ns.key, false),
		field(newFieldRemoteCmd, "Remote cmd: ", ns.cmd, false),
	}
	if ns.err != "" {
		rows = append(rows, "", "Error:")
		for _, line := range wrapText(ns.err, width, 4) {
			rows = append(rows, line)
		}
	}
	rows = append(rows,
		"",
		"Enter start   Esc cancel   ↑/↓ choose   Tab fields   1/2/3 choose",
	)
	return padBox(rows, width)
}

func lipglossWidth(s string) int { return len([]rune(s)) }

func wrapText(text string, width, minLines int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		words := strings.Fields(raw)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := ""
		for _, word := range words {
			if line == "" {
				for len([]rune(word)) > width {
					r := []rune(word)
					lines = append(lines, string(r[:width]))
					word = string(r[width:])
				}
				line = word
				continue
			}
			if len([]rune(line))+1+len([]rune(word)) <= width {
				line += " " + word
				continue
			}
			lines = append(lines, line)
			line = ""
			for len([]rune(word)) > width {
				r := []rune(word)
				lines = append(lines, string(r[:width]))
				word = string(r[width:])
			}
			line = word
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	for len(lines) < minLines {
		lines = append(lines, "")
	}
	return lines
}
