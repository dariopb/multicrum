package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/urfave/cli/v3"
	"multicrum/pkg/config"
	"multicrum/pkg/localserver"
	"multicrum/pkg/ssh_client"
	"multicrum/pkg/ui"
)

func main() {
	cmd := &cli.Command{
		Name:  "multicrum",
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
			&cli.StringFlag{
				Name:    "server",
				Aliases: []string{"srv", "S"},
				Value:   "default",
				Usage:   "named local multicrum server to attach/create",
			},
			&cli.StringFlag{
				Name:  "config",
				Value: "multicrum.yaml",
				Usage: "path to layout YAML file; loaded on startup if it exists, saved with Ctrl+Alt+P",
			},
			&cli.BoolFlag{
				Name:   "owner",
				Hidden: true,
			},
		},
		Commands: []*cli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Usage:   "list local multicrum servers",
				Action:  listServers,
			},
			{
				Name:   "status",
				Usage:  "show the status of a local multicrum server",
				Action: statusServer,
			},
			{
				Name:   "stop",
				Usage:  "stop a local multicrum server and its sessions",
				Action: stopServer,
			},
		},
		Action: run,
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c *cli.Command) error {
	serverName := normalizedServerName(c)
	socketPath, err := localserver.SocketPath(serverName)
	if err != nil {
		return err
	}
	if c.Bool("owner") {
		return runOwner(ctx, c, serverName, socketPath, true)
	}
	if attached, attachErr := localserver.TryAttach(socketPath, serverName, os.Stdin, os.Stdout); attached {
		return attachErr
	}
	if err := startDetachedOwner(os.Args, serverName); err != nil {
		return err
	}
	if err := waitForServer(socketPath, serverName, 5*time.Second); err != nil {
		return err
	}
	if attached, attachErr := localserver.TryAttach(socketPath, serverName, os.Stdin, os.Stdout); attached {
		return attachErr
	}
	return fmt.Errorf("server %q started but could not be attached", serverName)
}

func runOwner(ctx context.Context, c *cli.Command, serverName, socketPath string, detachedOwner bool) error {
	agentCmdLine := c.String("cmd")
	agentCmd := ui.ParseCmdLine(agentCmdLine)
	if len(agentCmd) == 0 {
		agentCmd = []string{"bash"}
		agentCmdLine = ""
	}

	cols, rows := 220, 48
	if !detachedOwner {
		if w, h, err := termSize(); err == nil {
			cols, rows = w, h
		}
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
	model.SetAgentCmdLine(agentCmdLine)
	model.SetServerName(serverName)

	configPath := c.String("config")
	model.SetConfigPath(configPath)
	if configPath != "" {
		cfg, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		} else if cfg != nil {
			model.SetConfigConnections(cfg)
		}
	}

	var input io.Reader
	if !detachedOwner {
		input = os.Stdin
	}
	inputMux := ui.NewInputMux(input)
	model.SetInputMux(inputMux)
	owner, err := localserver.ListenWithSettings(socketPath, serverName, inputMux, serverSettings(c, agentCmdLine))
	if err != nil {
		return err
	}
	defer owner.Close()

	var output io.Writer = owner
	if !detachedOwner {
		output = localserver.FanoutWriter{Primary: os.Stdout, Mirror: owner}
	}

	var p *tea.Program
	p = tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithColorProfile(colorprofile.TrueColor),
		tea.WithInput(inputMux),
		tea.WithOutput(ui.NewKeyboardStripWriter(output)),
	)
	model.SetProgram(p)
	owner.SetCallbacks(func(n int) {
		if p != nil {
			go p.Send(ui.LocalClientCountMsg(n))
		}
	}, func(cols, rows int) {
		if p != nil {
			go p.Send(tea.WindowSizeMsg{Width: cols, Height: rows})
		}
	}, func(action string) {
		if action == "stop" && p != nil {
			go p.Kill()
		}
	})

	wsAddr := c.String("ws")
	if wsAddr != "" {
		wst, err := ui.StartWSTransport(wsAddr, c.String("token"), model)
		if err != nil {
			return fmt.Errorf("ws transport: %w", err)
		}
		_ = wst
		fmt.Fprintf(os.Stderr, "xterm.js UI on http://%s/\n", wsAddr)
	}

	_, err = p.Run()
	return err
}

func serverSettings(c *cli.Command, command string) localserver.ServerSettings {
	return localserver.ServerSettings{
		Command:                  command,
		SSH:                      c.String("ssh"),
		SSHKey:                   c.String("ssh-key"),
		SSHUseDefaultKeys:        c.Bool("ssh-use-default-keys"),
		SSHAgent:                 c.Bool("ssh-agent"),
		SSHKnownHosts:            c.String("ssh-known-hosts"),
		SSHInsecureIgnoreHostKey: c.Bool("ssh-insecure-ignore-host-key"),
		WS:                       c.String("ws"),
		TokenSet:                 c.String("token") != "",
		Config:                   c.String("config"),
	}
}

func listServers(_ context.Context, _ *cli.Command) error {
	names, err := localserver.ListServers()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stdout, "no local servers")
		return nil
	}
	for _, name := range names {
		path, err := localserver.SocketPath(name)
		if err != nil {
			fmt.Fprintf(os.Stdout, "%s\tunknown\n", name)
			continue
		}
		status, err := localserver.ServerStatus(path, name)
		if err != nil {
			fmt.Fprintf(os.Stdout, "%s\tstale\t%s\n", name, path)
			continue
		}
		fmt.Fprintf(os.Stdout, "%s\tactive\tpid=%d\t%s\t%s\n", name, status.ServerPID, path, formatSettings(status.Settings))
	}
	return nil
}

func statusServer(_ context.Context, c *cli.Command) error {
	serverName := normalizedServerName(c)
	path, err := localserver.SocketPath(serverName)
	if err != nil {
		return err
	}
	status, err := localserver.ServerStatus(path, serverName)
	if err != nil {
		return fmt.Errorf("server %q is not running", serverName)
	}
	fmt.Fprintf(os.Stdout, "%s active pid=%d socket=%s\n", status.Server, status.ServerPID, path)
	if settings := formatSettings(status.Settings); settings != "" {
		fmt.Fprintf(os.Stdout, "settings: %s\n", settings)
	}
	return nil
}

func formatSettings(settings localserver.ServerSettings) string {
	var parts []string
	if settings.Command != "" {
		parts = append(parts, "cmd="+settings.Command)
	}
	if settings.Config != "" {
		parts = append(parts, "config="+settings.Config)
	}
	if settings.WS != "" {
		parts = append(parts, "ws="+settings.WS)
	}
	if settings.TokenSet {
		parts = append(parts, "token=set")
	}
	if settings.SSH != "" {
		parts = append(parts, "ssh="+settings.SSH)
	}
	if settings.SSHKey != "" {
		parts = append(parts, "ssh-key="+settings.SSHKey)
	}
	if settings.SSHUseDefaultKeys {
		parts = append(parts, "ssh-use-default-keys=true")
	}
	if settings.SSHAgent {
		parts = append(parts, "ssh-agent=true")
	}
	if settings.SSHKnownHosts != "" {
		parts = append(parts, "ssh-known-hosts="+settings.SSHKnownHosts)
	}
	if settings.SSHInsecureIgnoreHostKey {
		parts = append(parts, "ssh-insecure-ignore-host-key=true")
	}
	return strings.Join(parts, " ")
}

func stopServer(_ context.Context, c *cli.Command) error {
	serverName := normalizedServerName(c)
	path, err := localserver.SocketPath(serverName)
	if err != nil {
		return err
	}
	status, err := localserver.StopServer(path, serverName)
	if err != nil {
		return fmt.Errorf("server %q is not running", serverName)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := localserver.ServerStatus(path, serverName); err != nil {
			fmt.Fprintf(os.Stdout, "%s stopped pid=%d\n", status.Server, status.ServerPID)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if process, err := os.FindProcess(status.ServerPID); err == nil {
		_ = process.Kill()
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := localserver.ServerStatus(path, serverName); err != nil {
			_ = os.Remove(path)
			fmt.Fprintf(os.Stdout, "%s stopped pid=%d\n", status.Server, status.ServerPID)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server %q did not stop before timeout", serverName)
}

func waitForServer(path, server string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := localserver.ServerStatus(path, server); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server %q did not become ready: %w", server, lastErr)
}

func normalizedServerName(c *cli.Command) string {
	serverName := c.String("server")
	if serverName == "" {
		serverName = "default"
	}
	return serverName
}

func ownerArgs(args []string) []string {
	out := append([]string(nil), args...)
	for _, arg := range out[1:] {
		if arg == "--owner" {
			return out
		}
	}
	return append(out, "--owner")
}
