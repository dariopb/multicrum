//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"multicrum/pkg/control"
	"multicrum/pkg/localserver"
)

func TestMain(m *testing.M) {
	if os.Getenv("MULTICRUM_TEST_DAEMON") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDaemonLifecycle(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const server = "lifecycle"
	socket, err := localserver.SocketPath(server)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := os.Stat(socket); err == nil {
			if _, err := localserver.StopServer(socket, server); err != nil {
				t.Errorf("stop test daemon: %v", err)
			}
			waitForDaemonCondition(t, func() bool {
				_, err := os.Stat(socket)
				return os.IsNotExist(err)
			})
		}
		logPath, err := localserver.LogPath(server)
		if err == nil {
			data, err := os.ReadFile(logPath)
			if err == nil {
				t.Logf("daemon log:\n%s", data)
			}
		}
		if t.Failed() {
			return
		}
		tracePath := strings.TrimSuffix(logPath, ".log") + ".trace.log"
		data, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"source=local-attach", "old=100x28 new=80x18", "replay.begin", "replay.end",
			"session.output-ended", "session.respawn.begin", "generation=2",
		} {
			if !strings.Contains(string(data), want) {
				t.Errorf("daemon trace missing %q:\n%s", want, data)
			}
		}
		if strings.Contains(string(data), "reflow-ready") {
			t.Fatal("terminal contents leaked into diagnostics")
		}
	})

	attach := func(cols, rows uint16) func() {
		t.Helper()
		cmd := exec.Command(executable, "--server", server, "--config", "", "--cmd", "sh")
		cmd.Env = append(os.Environ(), "MULTICRUM_TEST_DAEMON=1")
		master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
		if err != nil {
			t.Fatal(err)
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		closed := false
		closeTerminal := func() {
			t.Helper()
			if closed {
				return
			}
			closed = true
			// Closing the outer PTY generates a real terminal hangup, not
			// an application-level detach request.
			_ = master.Close()
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-exited
				t.Error("attach client did not exit after terminal hangup")
			}
		}
		t.Cleanup(closeTerminal)
		return closeTerminal
	}

	closeTerminal := attach(100, 30)
	if err := waitForServer(socket, server, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	hello, err := localserver.ServerStatus(socket, server)
	if err != nil {
		t.Fatal(err)
	}
	ownerPID := hello.ServerPID
	t.Run("not-process-group-leader", func(t *testing.T) {
		group, err := syscall.Getpgid(ownerPID)
		if err != nil {
			t.Fatal(err)
		}
		if group == ownerPID {
			t.Fatal("final owner is still its own process-group leader; the second spawn must not call setsid")
		}
	})

	var client *control.Client
	waitForDaemonCondition(t, func() bool {
		client, err = control.Dial(control.Options{Server: server})
		return err == nil
	})
	defer client.Close()
	type sessionInfo struct {
		SessionID  string `json:"sessionId"`
		Generation uint64 `json:"generation"`
		PID        int    `json:"pid"`
		Cols       int    `json:"cols"`
		Rows       int    `json:"rows"`
		State      string `json:"state"`
	}
	currentSession := func() sessionInfo {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var result struct {
			Sessions []sessionInfo `json:"sessions"`
		}
		if err := client.Call(ctx, "session.list", map[string]any{}, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) != 1 {
			t.Fatalf("sessions = %#v, want one persistent session", result.Sessions)
		}
		return result.Sessions[0]
	}
	waitForDaemonCondition(t, func() bool {
		sess := currentSession()
		return sess.Cols == 100 && sess.Rows == 28
	})
	original := currentSession()
	// Record valid full-height margins followed by DL. Reconnecting with a
	// shorter terminal replays those now-oversized margins in the owner loop.
	reflowCtx, reflowCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer reflowCancel()
	if err := client.Call(reflowCtx, "session.sendText", map[string]any{
		"sessionId": original.SessionID, "generation": original.Generation,
		"text": `printf '\033[1;28r\033[H\033[13Mreflow-ready\033[r'`, "submit": true,
	}, nil); err != nil {
		t.Fatal(err)
	}
	waitForDaemonCondition(t, func() bool {
		var snapshot struct {
			Data []byte `json:"data"`
		}
		if err := client.Call(reflowCtx, "session.snapshot", map[string]any{
			"sessionId": original.SessionID, "generation": original.Generation,
		}, &snapshot); err != nil {
			t.Fatal(err)
		}
		return strings.Contains(string(snapshot.Data), "\x1b[13Mreflow-ready")
	})
	for _, size := range [][2]uint16{{80, 20}, {110, 35}, {130, 45}} {
		closeTerminal()
		hello, err := localserver.ServerStatus(socket, server)
		if err != nil || hello.ServerPID != ownerPID {
			t.Fatalf("owner did not survive terminal close: hello=%+v err=%v", hello, err)
		}
		cols, rows := size[0], size[1]
		closeTerminal = attach(cols, rows)
		waitForDaemonCondition(t, func() bool {
			sess := currentSession()
			if sess.SessionID != original.SessionID || sess.PID != original.PID || sess.Generation != original.Generation {
				t.Fatalf("reconnect replaced session: original=%+v current=%+v", original, sess)
			}
			return sess.Cols == int(cols) && sess.Rows == int(rows)-2
		})
	}

	if err := syscall.Kill(original.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitForDaemonCondition(t, func() bool { return currentSession().State == "exited" })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Call(ctx, "session.respawn", map[string]any{
		"sessionId": original.SessionID, "generation": original.Generation,
	}, nil); err != nil {
		t.Fatal(err)
	}
	waitForDaemonCondition(t, func() bool {
		sess := currentSession()
		return sess.State == "running" && sess.Generation == original.Generation+1
	})
	if err := syscall.Kill(ownerPID, syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	// A request after SIGHUP also verifies that the event loop, rather than
	// just the socket listener, is still serving the same session.
	if currentSession().SessionID != original.SessionID {
		t.Fatal("session identity changed after owner hangup")
	}
	closeTerminal()
}

func waitForDaemonCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for daemon lifecycle state")
}

func TestOwnerSignalLogging(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const server = "signal"
	socket, err := localserver.SocketPath(server)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "owner.log")
	output, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd := exec.Command(executable, "--owner", "--server", server, "--config", "", "--cmd", "sh")
	cmd.Env = append(os.Environ(), "MULTICRUM_TEST_DAEMON=1")
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	if err := waitForServer(socket, server, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	waitForDaemonCondition(t, func() bool {
		data, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(data), "owner started")
	})
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("owner SIGTERM shutdown: %v", waitErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not stop on SIGTERM")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		fmt.Sprintf("pid=%d", cmd.Process.Pid),
		"signal=terminated",
		"owner stopped",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("owner log missing %q:\n%s", want, data)
		}
	}
}
