//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"multicrum/pkg/localserver"
)

func TestOwnerStopCancelsLBReadiness(t *testing.T) {
	clearCLIEnvironment(t)
	t.Setenv("TMPDIR", t.TempDir())
	endpoint, requests, _ := startupTestLB(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const server = "lb-cancel"
	socket, err := localserver.SocketPath(server)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "layout.yaml")
	command := exec.Command(executable, "--owner", "--server", server, "--config", configPath,
		"--cmd", "sh", "--ws", "127.0.0.1:0", "--lb-api", endpoint)
	command.Env = append(os.Environ(), "MULTICRUM_TEST_DAEMON=1")
	output := newStartupTestOutput()
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = command.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		command.Process.Kill()
		<-done
	})
	select {
	case <-requests:
	case <-time.After(5 * time.Second):
		t.Fatalf("no registration request:\n%s", output.String())
	}
	if _, err := localserver.StopServer(socket, server); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if waitErr == nil {
			t.Fatal("unconfirmed startup returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner stop did not cancel upstream readiness wait")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed settings were saved: %v", err)
	}
}
