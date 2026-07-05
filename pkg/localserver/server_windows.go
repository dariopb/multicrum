//go:build windows

package localserver

import (
	"fmt"
	"io"
	"os"
)

type InputSink interface{ Inject([]byte) }

type Owner struct{}

func SocketDir() (string, error) {
	return "", fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func SocketPath(server string) (string, error) {
	return "", fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func LogPath(server string) (string, error) {
	return "", fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func ListServers() ([]string, error) {
	return nil, fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func TryAttach(path, server string, stdin *os.File, stdout io.Writer) (bool, error) {
	return false, fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func ServerStatus(path, server string) (*ServerHello, error) {
	return nil, fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func StopServer(path, server string) (*ServerHello, error) {
	return nil, fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func Listen(path, server string, input InputSink) (*Owner, error) {
	return ListenWithSettings(path, server, input, ServerSettings{})
}
func ListenWithSettings(path, server string, input InputSink, settings ServerSettings) (*Owner, error) {
	return nil, fmt.Errorf("local server sockets are not implemented on Windows yet")
}
func (o *Owner) SetCallbacks(onCount func(int), onResize func(cols, rows int), onControl func(action string)) {
}
func (o *Owner) Write(p []byte) (int, error) { return len(p), nil }
func (o *Owner) Close() error                { return nil }
