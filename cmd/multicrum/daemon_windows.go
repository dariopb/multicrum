//go:build windows

package main

import "fmt"

func startDetachedOwner(args []string, serverName string) error {
	return fmt.Errorf("local server daemonization is not implemented on Windows yet")
}
