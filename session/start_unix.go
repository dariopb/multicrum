//go:build !windows

package session

import (
	"fmt"

	"multiagent/console"
)

// Start forks the process into a Unix PTY.
func (s *Session) Start(cols, rows int) error {
	uc, err := console.NewUnixConsole(s.cmd, cols, rows)
	if err != nil {
		return fmt.Errorf("Unix PTY start: %w", err)
	}

	s.mu.Lock()
	s.rw = uc
	s.resizeFn = func(cols, rows int) error {
		return uc.Resize(cols, rows)
	}
	s.mu.Unlock()
	s.screen.SetReplyWriter(uc)

	go s.readLoop()
	return nil
}
