//go:build windows

package session

import (
	"fmt"
	"strings"

	"multicrum/pkg/console"
)

// Start opens the configured backend: SSH remote PTY when configured,
// otherwise a local Windows ConPTY.
func (s *Session) Start(cols, rows int) error {
	if s.sshClient != nil {
		return s.startSSH(cols, rows)
	}
	cmd := strings.Join(s.cmd, " ")
	wc, err := console.NewWinConsole(cmd, cols, rows, s.workDir)
	if err != nil {
		return fmt.Errorf("ConPTY start: %w", err)
	}

	s.mu.Lock()
	s.rw = wc
	s.processID = wc.PID()
	s.generation++
	generation := s.generation
	screen := s.screen
	s.resizeFn = func(cols, rows int) error {
		return wc.Resize(cols, rows)
	}
	s.mu.Unlock()
	s.screen.SetReplyWriter(wc)
	s.screen.SetTerminalReplies(true)

	go s.readLoop(wc, screen, generation)
	return nil
}
