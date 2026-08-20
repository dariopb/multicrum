//go:build !windows

package control

import (
	"fmt"
	"net"
	"path/filepath"

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
	return filepath.Join(dir, sanitize(server)+".control.sock"), nil
}

func listenEndpoint(endpoint string) (net.Listener, error) {
	return net.Listen("unix", endpoint)
}

func dialEndpoint(endpoint string) (net.Conn, error) {
	return net.Dial("unix", endpoint)
}
