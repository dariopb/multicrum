package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/urfave/cli/v3"
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

	model := ui.NewModel(agentCmd, cols, rows)

	p := tea.NewProgram(model, tea.WithAltScreen())
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
