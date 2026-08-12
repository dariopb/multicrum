//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"multicrum/pkg/localserver"
)

func startDetachedOwner(args []string, serverName string) error {
	return startDetachedProcess(daemonBootstrapArgs(args), serverName, true)
}

func finishDetachedOwner(args []string, serverName string) error {
	// Preserve an ignored SIGHUP across exec so there is no window where a
	// closing shell can terminate the final owner before main starts.
	signal.Ignore(syscall.SIGHUP)
	return startDetachedProcess(ownerArgs(args), serverName, false)
}

func startDetachedProcess(args []string, serverName string, wait bool) error {
	logPath, err := localserver.LogPath(serverName)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if wait {
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("daemon bootstrap: %w", err)
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release detached owner: %w", err)
	}
	return nil
}
