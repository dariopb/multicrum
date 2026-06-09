package ui

import (
	"strings"

	"multicrum/pkg/ssh_client"
	"multicrum/pkg/transport"
)

func (s *state) handleWSNew(m Model, msg transport.ControlMsg) {
	mode := msg.Mode
	if mode == "" {
		mode = "same"
	}
	var err error
	switch mode {
	case "same":
		_, err = s.manager.New(m.agentCmd)
	case "local":
		cmd := strings.Fields(strings.TrimSpace(msg.Cmd))
		if len(cmd) == 0 {
			cmd = m.agentCmd
		}
		_, err = s.manager.NewWithSSH(cmd, nil)
	case "remote":
		cmd := strings.Fields(strings.TrimSpace(msg.Cmd))
		client, cfgErr := ssh_client.New(ssh_client.Options{
			Target:         strings.TrimSpace(msg.Target),
			Port:           strings.TrimSpace(msg.Port),
			IdentityFile:   strings.TrimSpace(msg.Key),
			Password:       msg.Password,
			UseDefaultKeys: strings.TrimSpace(msg.Key) == "" && msg.Password == "",
			UseAgent:       strings.TrimSpace(msg.Key) == "" && msg.Password == "",
			Command:        cmd,
		})
		if cfgErr != nil {
			err = cfgErr
		} else {
			_, err = s.manager.NewWithSSH(cmd, client)
		}
	}
	if err != nil {
		return
	}
	s.resetViewport(s.manager.FocusedIndex(), s.width, s.height)
	s.notifyMeta()
}

func (s *state) handleWSExit(msg transport.ControlMsg) {
	id := msg.ID
	choice := msg.Choice
	if choice == "" {
		choice = "respawn"
	}
	if choice == "respawn" {
		if err := s.manager.Respawn(id); err == nil {
			s.resetViewport(id, s.width, s.height)
			s.notifyMeta()
		}
		return
	}
	if choice == "remove" {
		if s.manager.Len() <= 1 {
			return
		}
		delete(s.viewports, id)
		s.manager.Kill(id)
		s.refreshFocused()
		s.notifyMeta()
	}
}
