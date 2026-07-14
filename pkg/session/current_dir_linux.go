//go:build linux

package session

import (
	"fmt"
	"os"
)

func (s *Session) CurrentDirectory() (string, error) {
	s.mu.Lock()
	pid := s.processID
	s.mu.Unlock()
	if pid <= 0 {
		return "", fmt.Errorf("session process is not running")
	}
	return os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
}
