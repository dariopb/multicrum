//go:build !windows

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	tunnel "github.com/dariopb/goreverselb/pkg"
	"github.com/hashicorp/yamux"
	"golang.org/x/term"
	"multicrum/pkg/config"
	"multicrum/pkg/control"
	"multicrum/pkg/localserver"
)

func TestStartupKeypressAndTerminalRestoration(t *testing.T) {
	for _, action := range []string{"key", "ctrl-c", "cancel"} {
		t.Run(action, func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			output := newStartupTestOutput()
			done := make(chan error, 1)
			go func() { done <- waitStartupKey(ctx, slave, output) }()
			awaitStartupOutput(t, output, "Press any key")
			select {
			case err := <-done:
				t.Fatalf("continued without a keypress: %v", err)
			default:
			}
			if action == "cancel" {
				cancel()
			} else {
				key := []byte("x")
				if action == "ctrl-c" {
					key = []byte{3}
				}
				if _, err := master.Write(key); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if action == "key" && err != nil || action != "key" && !errors.Is(err, context.Canceled) {
					t.Fatalf("keypress result: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("keypress wait did not finish")
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("terminal state not restored: %v", err)
			}
		})
	}
}

func startupTestLB(t *testing.T) (string, <-chan tunnel.TunnelData, chan<- struct{}) {
	t.Helper()
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	tlsConfig := certServer.TLS.Clone()
	certServer.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	requests := make(chan tunnel.TunnelData, 1)
	acknowledge := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		settings := yamux.DefaultConfig()
		settings.LogOutput = io.Discard
		session, err := yamux.Server(conn, settings)
		if err != nil {
			t.Error(err)
			return
		}
		defer session.Close()
		stream, err := session.AcceptStream()
		if err != nil {
			if ctx.Err() == nil {
				t.Error(err)
			}
			return
		}
		var request tunnel.TunnelData
		if err := json.NewDecoder(stream).Decode(&request); err != nil {
			if ctx.Err() == nil {
				t.Error(err)
			}
			return
		}
		requests <- request
		select {
		case <-ctx.Done():
			return
		case <-acknowledge:
		}
		if err := json.NewEncoder(stream).Encode(tunnel.TunnelDataResponse{
			ServiceName: request.ServiceName, FrontendPort: 8123, FrontendAddress: "127.0.0.1",
		}); err != nil {
			t.Error(err)
			return
		}
		<-session.CloseChan()
	}()
	t.Cleanup(func() {
		cancel()
		listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("test LB did not stop")
		}
	})
	return listener.Addr().String(), requests, acknowledge
}

func TestLBStartupPausesOnlyLaunchingTerminal(t *testing.T) {
	clearCLIEnvironment(t)
	t.Setenv("TMPDIR", t.TempDir())
	endpoint, requests, acknowledge := startupTestLB(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const server = "lb-startup"
	socket, err := localserver.SocketPath(server)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "layout.yaml")
	command := exec.Command(executable, "--server", server, "--config", configPath,
		"--cmd", "sh", "--ws", "127.0.0.1:0", "--lb-api", endpoint, "--lb-service", "startup-test")
	command.Env = append(os.Environ(), "MULTICRUM_TEST_DAEMON=1")
	master, err := pty.StartWithSize(command, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	output := newStartupTestOutput()
	go func() { _, _ = io.Copy(output, master) }()
	t.Cleanup(func() {
		if _, err := localserver.StopServer(socket, server); err != nil {
			t.Errorf("stop owner: %v", err)
		} else {
			waitForDaemonCondition(t, func() bool {
				_, err := os.Stat(socket)
				return os.IsNotExist(err)
			})
		}
		master.Close()
		command.Process.Kill()
		<-exited
	})
	var request tunnel.TunnelData
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not register with LB")
	}
	if request.FrontendData.Port != 0 {
		t.Fatalf("requested port = %d, want auto", request.FrontendData.Port)
	}
	awaitStartupOutput(t, output, "Waiting for reverse LB")
	if strings.Contains(output.String(), "Press any key") {
		t.Fatal("prompt appeared before LB acknowledgement")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("saved settings before LB readiness: %v", err)
	}
	acknowledge <- struct{}{}
	awaitStartupOutput(t, output, "port 8123")
	awaitStartupOutput(t, output, "Press any key")
	if strings.Contains(output.String(), "\x1b[?1049h") {
		t.Fatal("TUI attached before keypress")
	}
	saved, err := config.Load(configPath)
	if err != nil || saved == nil || saved.Web.ReverseLB.FrontendPort != 0 {
		t.Fatalf("requested auto setting not preserved after readiness: %v", err)
	}
	client, err := control.Dial(control.Options{Server: server})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var sessions struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := client.Call(ctx, "session.list", map[string]any{}, &sessions); err != nil || len(sessions.Sessions) != 1 {
		t.Fatalf("owner sessions blocked on client keypress: %v", err)
	}
	httpClient := &http.Client{Timeout: time.Second}
	response, err := httpClient.Get("http://127.0.0.1:" + strconv.Itoa(request.TargetPort) + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("web endpoint blocked on keypress: %d", response.StatusCode)
	}
	if _, err := master.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	awaitStartupOutput(t, output, "\x1b[?1049h")
}
