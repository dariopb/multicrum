//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"multicrum/pkg/localserver"
)

func startDetachedOwner(args []string, serverName string) error {
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
	cmd := exec.Command(executable, ownerArgs(args)[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release detached owner: %w", err)
	}
	return nil
}
