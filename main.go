package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/urfave/cli/v3"
	"multiagent/ssh_client"
	"multiagent/ui"
)

func main() {
	cmd := &cli.Command{
		Name:  "multiagent",
		Usage: "run multiple persistent agent sessions in a terminal UI",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "cmd",
				Value: "bash",
				Usage: "command to run in each session (space-separated)",
			},
			&cli.StringFlag{
				Name:  "ssh",
				Usage: "SSH target for remote sessions, e.g. user@host or user@host:2222",
			},
			&cli.StringFlag{
				Name:    "ssh-key",
				Aliases: []string{"i"},
				Usage:   "SSH identity file (OpenSSH -i equivalent)",
			},
			&cli.StringFlag{
				Name:  "ssh-passwd",
				Usage: "SSH password for password/keyboard-interactive authentication",
			},
			&cli.BoolFlag{
				Name:  "ssh-use-default-keys",
				Usage: "try standard keys from ~/.ssh (id_ed25519, id_ecdsa, id_rsa, id_dsa)",
			},
			&cli.BoolFlag{
				Name:  "ssh-agent",
				Usage: "use SSH agent authentication when SSH_AUTH_SOCK is available",
				Value: true,
			},
			&cli.StringFlag{
				Name:  "ssh-known-hosts",
				Usage: "known_hosts file path override",
			},
			&cli.BoolFlag{
				Name:  "ssh-insecure-ignore-host-key",
				Usage: "disable SSH host key verification (unsafe; testing only)",
			},
			&cli.StringFlag{
				Name:  "ws",
				Usage: "if set, serve xterm.js WebSocket on this address, e.g. :9999",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "optional auth token for the WebSocket endpoint",
			},
		},
		Action: run,
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(_ context.Context, c *cli.Command) error {
	agentCmd := strings.Fields(c.String("cmd"))
	if len(agentCmd) == 0 {
		agentCmd = []string{"bash"}
	}

	cols, rows := 220, 48
	if w, h, err := termSize(); err == nil {
		cols, rows = w, h
	}

	var sshClient *ssh_client.Client
	if target := c.String("ssh"); target != "" {
		client, err := ssh_client.New(ssh_client.Options{
			Target:                target,
			IdentityFile:          c.String("ssh-key"),
			Password:              c.String("ssh-passwd"),
			UseDefaultKeys:        c.Bool("ssh-use-default-keys"),
			UseAgent:              c.Bool("ssh-agent"),
			KnownHosts:            c.String("ssh-known-hosts"),
			InsecureIgnoreHostKey: c.Bool("ssh-insecure-ignore-host-key"),
			Command:               agentCmd,
		})
		if err != nil {
			return fmt.Errorf("ssh config: %w", err)
		}
		sshClient = client
	}

	model := ui.NewModelWithSSH(agentCmd, cols, rows, sshClient)

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseAllMotion())
	model.SetProgram(p)

	wsAddr := c.String("ws")
	if wsAddr != "" {
		wst, err := ui.StartWSTransport(wsAddr, c.String("token"), model)
		if err != nil {
			return fmt.Errorf("ws transport: %w", err)
		}
		_ = wst
		fmt.Fprintf(os.Stderr, "xterm.js UI on http://%s/\n", wsAddr)
	}

	_, err := p.Run()
	return err
}
