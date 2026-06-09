package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/session"
	"multicrum/pkg/ssh_client"
)

type newSessionField int

type newSessionState struct {
	choice int // 0 same, 1 local, 2 remote
	field  newSessionField
	local  string
	target string
	port   string
	passwd string
	key    string
	cmd    string
	err    string

	// per-field rune cursor positions
	localCur  int
	targetCur int
	portCur   int
	passwdCur int
	keyCur    int
	cmdCur    int
}

const (
	newFieldLocal newSessionField = iota
	newFieldTarget
	newFieldPort
	newFieldPasswd
	newFieldKey
	newFieldRemoteCmd
)

func (s *state) openNewSessionModal(defaultCmd []string) {
	s.newSessionReturn = modeNormal
	s.openNewSessionModalWithReturn(defaultCmd, modeNormal)
}

func (s *state) openNewSessionModalWithReturn(defaultCmd []string, returnMode mode) {
	local := strings.Join(defaultCmd, " ")
	cfg := ssh_client.ResolvedConfig{}
	if s.sshClient != nil {
		cfg = s.sshClient.Config()
	}
	target := ""
	port := "22"
	remoteCmd := ""
	key := ""
	if cfg.Host != "" {
		target = cfg.User + "@" + cfg.Host
		if cfg.Port != "" {
			port = cfg.Port
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
		port:   port,
		key:    key,
		cmd:    remoteCmd,

		localCur:  len([]rune(local)),
		targetCur: len([]rune(target)),
		portCur:   len([]rune(port)),
		keyCur:    len([]rune(key)),
		cmdCur:    len([]rune(remoteCmd)),
	}
	s.newSessionReturn = returnMode
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
		s.mode = s.newSessionReturn
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
		s.moveNewSessionCursor(-1)
		return nil
	case tea.KeyRight:
		s.moveNewSessionCursor(+1)
		return nil
	case tea.KeyHome:
		s.setNewSessionCursor(0)
		return nil
	case tea.KeyEnd:
		s.setNewSessionCursor(-1)
		return nil
	case tea.KeyDelete:
		s.deleteNewSessionField()
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
	fields := []newSessionField{newFieldLocal, newFieldTarget, newFieldPort, newFieldPasswd, newFieldKey, newFieldRemoteCmd}
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
	ns := &s.newSession
	switch ns.field {
	case newFieldLocal:
		ns.local, ns.localCur = insertAt(ns.local, ns.localCur, text)
		ns.choice = 1
	case newFieldTarget:
		ns.target, ns.targetCur = insertAt(ns.target, ns.targetCur, text)
		ns.choice = 2
	case newFieldPort:
		ns.port, ns.portCur = insertAt(ns.port, ns.portCur, text)
		ns.choice = 2
	case newFieldPasswd:
		ns.passwd, ns.passwdCur = insertAt(ns.passwd, ns.passwdCur, text)
		ns.choice = 2
	case newFieldKey:
		ns.key, ns.keyCur = insertAt(ns.key, ns.keyCur, text)
		ns.choice = 2
	case newFieldRemoteCmd:
		ns.cmd, ns.cmdCur = insertAt(ns.cmd, ns.cmdCur, text)
		ns.choice = 2
	}
}

func (s *state) backspaceNewSessionField() {
	ns := &s.newSession
	switch ns.field {
	case newFieldLocal:
		ns.local, ns.localCur = backspaceAt(ns.local, ns.localCur)
	case newFieldTarget:
		ns.target, ns.targetCur = backspaceAt(ns.target, ns.targetCur)
	case newFieldPort:
		ns.port, ns.portCur = backspaceAt(ns.port, ns.portCur)
	case newFieldPasswd:
		ns.passwd, ns.passwdCur = backspaceAt(ns.passwd, ns.passwdCur)
	case newFieldKey:
		ns.key, ns.keyCur = backspaceAt(ns.key, ns.keyCur)
	case newFieldRemoteCmd:
		ns.cmd, ns.cmdCur = backspaceAt(ns.cmd, ns.cmdCur)
	}
}

func (s *state) deleteNewSessionField() {
	ns := &s.newSession
	switch ns.field {
	case newFieldLocal:
		ns.local, ns.localCur = deleteAt(ns.local, ns.localCur)
	case newFieldTarget:
		ns.target, ns.targetCur = deleteAt(ns.target, ns.targetCur)
	case newFieldPort:
		ns.port, ns.portCur = deleteAt(ns.port, ns.portCur)
	case newFieldPasswd:
		ns.passwd, ns.passwdCur = deleteAt(ns.passwd, ns.passwdCur)
	case newFieldKey:
		ns.key, ns.keyCur = deleteAt(ns.key, ns.keyCur)
	case newFieldRemoteCmd:
		ns.cmd, ns.cmdCur = deleteAt(ns.cmd, ns.cmdCur)
	}
}

func (s *state) clearNewSessionField() {
	ns := &s.newSession
	switch ns.field {
	case newFieldLocal:
		ns.local = ""
		ns.localCur = 0
	case newFieldTarget:
		ns.target = ""
		ns.targetCur = 0
	case newFieldPort:
		ns.port = ""
		ns.portCur = 0
	case newFieldPasswd:
		ns.passwd = ""
		ns.passwdCur = 0
	case newFieldKey:
		ns.key = ""
		ns.keyCur = 0
	case newFieldRemoteCmd:
		ns.cmd = ""
		ns.cmdCur = 0
	}
}

// moveNewSessionCursor moves the active field's cursor by delta (clamped).
func (s *state) moveNewSessionCursor(delta int) {
	ns := &s.newSession
	switch ns.field {
	case newFieldLocal:
		ns.localCur = clampCursor(ns.local, ns.localCur+delta)
	case newFieldTarget:
		ns.targetCur = clampCursor(ns.target, ns.targetCur+delta)
	case newFieldPort:
		ns.portCur = clampCursor(ns.port, ns.portCur+delta)
	case newFieldPasswd:
		ns.passwdCur = clampCursor(ns.passwd, ns.passwdCur+delta)
	case newFieldKey:
		ns.keyCur = clampCursor(ns.key, ns.keyCur+delta)
	case newFieldRemoteCmd:
		ns.cmdCur = clampCursor(ns.cmd, ns.cmdCur+delta)
	}
}

// setNewSessionCursor sets cursor to pos (or end-of-field if pos < 0).
func (s *state) setNewSessionCursor(pos int) {
	ns := &s.newSession
	end := func(v string) int { return len([]rune(v)) }
	switch ns.field {
	case newFieldLocal:
		if pos < 0 {
			ns.localCur = end(ns.local)
		} else {
			ns.localCur = clampCursor(ns.local, pos)
		}
	case newFieldTarget:
		if pos < 0 {
			ns.targetCur = end(ns.target)
		} else {
			ns.targetCur = clampCursor(ns.target, pos)
		}
	case newFieldPort:
		if pos < 0 {
			ns.portCur = end(ns.port)
		} else {
			ns.portCur = clampCursor(ns.port, pos)
		}
	case newFieldPasswd:
		if pos < 0 {
			ns.passwdCur = end(ns.passwd)
		} else {
			ns.passwdCur = clampCursor(ns.passwd, pos)
		}
	case newFieldKey:
		if pos < 0 {
			ns.keyCur = end(ns.key)
		} else {
			ns.keyCur = clampCursor(ns.key, pos)
		}
	case newFieldRemoteCmd:
		if pos < 0 {
			ns.cmdCur = end(ns.cmd)
		} else {
			ns.cmdCur = clampCursor(ns.cmd, pos)
		}
	}
}

func (s *state) resolveNewSession(m Model) tea.Cmd {
	ns := s.newSession
	returnMode := s.newSessionReturn
	s.mode = returnMode
	s.newSession = newSessionState{}
	var err error
	var sess *session.Session
	switch ns.choice {
	case 0:
		sess, err = s.manager.New(m.agentCmd)
		if err == nil && m.agentCmdLine != "" {
			sess.SetCmdLine(m.agentCmdLine)
		}
	case 1:
		line := strings.TrimSpace(ns.local)
		cmd := parseCmdLine(line)
		if len(cmd) == 0 {
			cmd = m.agentCmd
			line = m.agentCmdLine
		}
		sess, err = s.manager.NewWithSSH(cmd, nil)
		if err == nil && line != "" {
			sess.SetCmdLine(line)
		}
	case 2:
		line := strings.TrimSpace(ns.cmd)
		cmd := parseCmdLine(line)
		client, cfgErr := ssh_client.New(ssh_client.Options{
			Target:                strings.TrimSpace(ns.target),
			Port:                  strings.TrimSpace(ns.port),
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
			sess, err = s.manager.NewWithSSH(cmd, client)
			if err == nil && line != "" {
				sess.SetCmdLine(line)
			}
		}
	}
	if err != nil {
		ns.err = err.Error()
		s.mode = modeNewSession
		s.newSession = ns
		s.newSessionReturn = returnMode
		return nil
	}
	s.errMsg = ""
	s.resetViewport(s.manager.FocusedIndex(), s.width, s.height)
	s.notifyMeta()
	if returnMode == modeSelecting {
		s.selectCursor = s.filteredSessionCursorForIndex(s.manager.FocusedIndex())
	}
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
	field := func(f newSessionField, label, value string, cursor int, secret bool) string {
		if secret && value != "" {
			value = strings.Repeat("*", len([]rune(value)))
		}
		display := value
		if ns.field == f {
			display = renderWithCursor(value, cursor)
		}
		return label + truncate(display, width-lipglossWidth(label))
	}
	rows := []string{
		"New session",
		"",
		choice(0, "[ Same as current/default ]") + modalGapStyle.Render("   ") + choice(1, "[ Local command ]") + modalGapStyle.Render("   ") + choice(2, "[ Remote SSH ]"),
		"",
		field(newFieldLocal, "Local cmd:  ", ns.local, ns.localCur, false),
		"",
		field(newFieldTarget, "SSH target: ", ns.target, ns.targetCur, false),
		field(newFieldPort, "SSH port:   ", ns.port, ns.portCur, false),
		field(newFieldPasswd, "Password:   ", ns.passwd, ns.passwdCur, true),
		field(newFieldKey, "Key file:   ", ns.key, ns.keyCur, false),
		field(newFieldRemoteCmd, "Remote cmd: ", ns.cmd, ns.cmdCur, false),
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
