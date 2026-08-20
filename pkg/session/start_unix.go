//go:build !windows

package session

import (
	"fmt"

	"multicrum/pkg/console"
)

// Start opens the configured backend: SSH remote PTY when configured,
// otherwise a local Unix PTY.
func (s *Session) Start(cols, rows int) error {
	if s.sshClient != nil {
		return s.startSSH(cols, rows)
	}
	uc, err := console.NewUnixConsole(s.cmd, cols, rows, s.workDir, s.agentEnvironment())
	if err != nil {
		return fmt.Errorf("Unix PTY start: %w", err)
	}

	s.mu.Lock()
	s.rw = uc
	s.processID = uc.PID()
	s.generation++
	generation := s.generation
	s.runToken++
	runToken := s.runToken
	screen := s.screen
	s.resizeFn = func(cols, rows int) error {
		return uc.Resize(cols, rows)
	}
	s.mu.Unlock()
	s.screen.SetReplyWriter(uc)
	s.screen.SetTerminalReplies(true)

	go s.readLoop(uc, screen, generation, runToken)
	return nil
}
