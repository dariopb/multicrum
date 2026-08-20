//go:build windows

package control

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"multicrum/pkg/localserver"
)

func Endpoint(server string) (string, error) {
	dir, err := localserver.SocketDir()
	if err != nil {
		return "", err
	}
	if server == "" {
		return "", fmt.Errorf("server name is required")
	}
	return filepath.Join(dir, sanitize(server)+".control.addr"), nil
}

func listenEndpoint(endpoint string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(endpoint, []byte(ln.Addr().String()), 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func dialEndpoint(endpoint string) (net.Conn, error) {
	address, err := os.ReadFile(endpoint)
	if err != nil {
		return nil, err
	}
	return net.Dial("tcp", strings.TrimSpace(string(address)))
}
