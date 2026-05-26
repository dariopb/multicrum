package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/urfave/cli/v3"
	"multicrum/pkg/session"
	"multicrum/pkg/ssh_client"
)

type controlMsg struct {
	Action   string   `json:"action"`
	Cmd      []string `json:"cmd,omitempty"`
	Target   string   `json:"target,omitempty"`
	Password string   `json:"password,omitempty"`
	KeyFile  string   `json:"keyFile,omitempty"`
}

func main() {
	cmd := &cli.Command{
		Name:  "parent-child-example",
		Usage: "demonstrates parent/child bootstrapping with multicrum/session",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "child"},
			&cli.StringFlag{Name: "mux-control"},
		},
		Action: run,
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c *cli.Command) error {
	if c.Bool("child") {
		return runChild(c.String("mux-control"))
	}
	return runParent(ctx)
}

func runParent(ctx context.Context) error {
	manager := session.NewManager(
		120,
		40,
		func(msg session.OutputMsg) {
			fmt.Printf("session %d output: %d bytes\n", msg.Index, len(msg.Data))
		},
		func(msg session.ExitMsg) {
			fmt.Printf("session %d exited\n", msg.Index)
		},
	)

	sockPath := os.TempDir() + "/multicrum-parent-child-example.sock"
	_ = os.Remove(sockPath)
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(sockPath)

	go serveControl(listener, manager)

	mainSession, err := manager.New([]string{os.Args[0], "--child", "--mux-control", sockPath})
	if err != nil {
		return err
	}
	mainSession.SetTitle("main")

	<-ctx.Done()
	return ctx.Err()
}

func runChild(controlSock string) error {
	fmt.Println("child app running inside managed PTY")
	requestSecondarySession(controlSock)
	select {}
}

func requestSecondarySession(sockPath string) {
	if sockPath == "" {
		return
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = json.NewEncoder(conn).Encode(controlMsg{
		Action: "new-local",
		Cmd:    []string{"bash"},
	})
}

func serveControl(listener net.Listener, manager *session.SessionManager) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			var msg controlMsg
			if err := json.NewDecoder(conn).Decode(&msg); err != nil {
				return
			}
			switch msg.Action {
			case "new-local":
				if len(msg.Cmd) == 0 {
					msg.Cmd = []string{"bash"}
				}
				sess, err := manager.NewWithSSH(msg.Cmd, nil)
				if err == nil {
					sess.SetTitle("secondary")
				}
			case "new-ssh":
				sshClient, err := ssh_client.New(ssh_client.Options{
					Target:       msg.Target,
					Password:     msg.Password,
					IdentityFile: msg.KeyFile,
					Command:      msg.Cmd,
				})
				if err != nil {
					return
				}
				sess, err := manager.NewWithSSH(msg.Cmd, sshClient)
				if err == nil {
					sess.SetTitle("secondary")
				}
			}
		}()
	}
}
