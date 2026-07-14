//go:build !linux

package session

import "fmt"

func (s *Session) CurrentDirectory() (string, error) {
	return "", fmt.Errorf("session working-directory capture is unsupported on this platform")
}
