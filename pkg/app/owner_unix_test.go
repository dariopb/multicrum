//go:build !windows

package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/control"
	"multicrum/pkg/localserver"
)

func TestOwnerStartsAtPaneGeometryAndCleansUp(t *testing.T) {
	server := fmt.Sprintf("app-test-%d", os.Getpid())
	owner, err := Start(context.Background(), Options{
		Server: server, Command: []string{"sh"},
		ConnectionLayout: "left", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}

	client, err := control.Dial(control.Options{Server: server})
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	var listed struct {
		Sessions []struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		} `json:"sessions"`
	}
	if err := client.Call(context.Background(), "session.list", map[string]any{}, &listed); err != nil {
		client.Close()
		owner.Close()
		t.Fatal(err)
	}
	client.Close()
	if len(listed.Sessions) != 1 || listed.Sessions[0].Cols != 64 || listed.Sessions[0].Rows != 23 {
		t.Fatalf("initial sessions = %#v, want one 64x23 pane", listed.Sessions)
	}

	socket, _ := localserver.SocketPath(server)
	endpoint, _ := control.Endpoint(server)
	token := control.TokenPath(endpoint)
	owner.Close()
	for _, path := range []string{socket, endpoint, token} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("runtime path still exists after Close: %s", path)
		}
	}
}

func TestProgramExitCleansRuntimePaths(t *testing.T) {
	server := fmt.Sprintf("app-quit-test-%d", os.Getpid())
	owner, err := Start(context.Background(), Options{
		Server: server, Command: []string{"sh"},
		ConnectionLayout: "left", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	socket, _ := localserver.SocketPath(server)
	endpoint, _ := control.Endpoint(server)
	token := control.TokenPath(endpoint)

	owner.program.Send(tea.Quit())
	select {
	case <-owner.Done():
	case <-time.After(3 * time.Second):
		owner.Close()
		t.Fatal("owner program did not exit")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		clean := true
		for _, path := range []string{socket, endpoint, token} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				clean = false
				break
			}
		}
		if clean {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	owner.Close()
	t.Fatal("runtime paths remained after program exit")
}

func TestProgramExitDisconnectsAttachedClient(t *testing.T) {
	server := fmt.Sprintf("app-attach-quit-test-%d", os.Getpid())
	owner, err := Start(context.Background(), Options{
		Server: server, Command: []string{"sh"},
		ConnectionLayout: "left", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	stdin, input, err := os.Pipe()
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	defer stdin.Close()
	defer input.Close()
	attachDone := make(chan error, 1)
	go func() {
		attachDone <- owner.Attach(stdin, io.Discard)
	}()

	time.Sleep(50 * time.Millisecond)
	owner.program.Send(tea.Quit())
	select {
	case err := <-attachDone:
		if err != nil {
			t.Fatalf("Attach() after owner exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		owner.Close()
		t.Fatal("attached client remained blocked after owner exit")
	}
}
