//go:build windows

package session

import (
	"fmt"
	"strings"

	"multiagent/console"
)

// Start launches the process inside a Windows ConPTY.
func (s *Session) Start(cols, rows int) error {
	cmd := strings.Join(s.cmd, " ")
	wc, err := console.NewWinConsole(cmd, cols, rows)
	if err != nil {
		return fmt.Errorf("ConPTY start: %w", err)
	}

	s.mu.Lock()
	s.rw = wc
	s.resizeFn = func(cols, rows int) error {
		return wc.Resize(cols, rows)
	}
	s.mu.Unlock()
	s.screen.SetReplyWriter(wc)

	go s.readLoop()
	return nil
}
