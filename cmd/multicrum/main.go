package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	tunnel "github.com/dariopb/goreverselb/pkg"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
	"multicrum/pkg/config"
	"multicrum/pkg/control"
	"multicrum/pkg/diagnostics"
	"multicrum/pkg/localserver"
	"multicrum/pkg/ssh_client"
	"multicrum/pkg/ui"
)

func newCLICommand() *cli.Command {
	return &cli.Command{
		Name:  "multicrum",
		Usage: "run multiple persistent agent sessions in a terminal UI",
		Flags: append([]cli.Flag{
			&cli.StringFlag{
				Name:    "log-level",
				Aliases: []string{"loglevel", "lb-log-level"},
				Sources: cli.EnvVars("MULTICRUM_LOG_LEVEL", "MULTICRUM_LB_LOG_LEVEL"),
				Value:   "info",
				Usage:   "application and library log level (e.g. info, debug, trace)",
			},
			&cli.StringFlag{
				Name:    "cmd",
				Sources: cli.EnvVars("MULTICRUM_CMD"),
				Value:   "bash",
				Usage:   "command to run in each session (space-separated)",
			},
			&cli.StringFlag{
				Name:    "ssh",
				Sources: cli.EnvVars("MULTICRUM_SSH"),
				Usage:   "SSH target for remote sessions, e.g. user@host or user@host:2222",
			},
			&cli.StringFlag{
				Name:    "ssh-key",
				Sources: cli.EnvVars("MULTICRUM_SSH_KEY"),
				Aliases: []string{"i"},
				Usage:   "SSH identity file (OpenSSH -i equivalent)",
			},
			&cli.StringFlag{
				Name:    "ssh-passwd",
				Sources: cli.EnvVars("MULTICRUM_SSH_PASSWD"),
				Usage:   "SSH password for password/keyboard-interactive authentication",
			},
			&cli.BoolFlag{
				Name:    "ssh-use-default-keys",
				Sources: cli.EnvVars("MULTICRUM_SSH_USE_DEFAULT_KEYS"),
				Usage:   "try standard keys from ~/.ssh (id_ed25519, id_ecdsa, id_rsa, id_dsa)",
			},
			&cli.BoolFlag{
				Name:    "ssh-agent",
				Sources: cli.EnvVars("MULTICRUM_SSH_AGENT"),
				Usage:   "use SSH agent authentication when SSH_AUTH_SOCK is available",
				Value:   true,
			},
			&cli.StringFlag{
				Name:    "ssh-known-hosts",
				Sources: cli.EnvVars("MULTICRUM_SSH_KNOWN_HOSTS"),
				Usage:   "known_hosts file path override",
			},
			&cli.BoolFlag{
				Name:    "ssh-insecure-ignore-host-key",
				Sources: cli.EnvVars("MULTICRUM_SSH_INSECURE_IGNORE_HOST_KEY"),
				Usage:   "disable SSH host key verification (unsafe; testing only)",
			},
			&cli.StringFlag{
				Name:    "control-token",
				Sources: cli.EnvVars("MULTICRUM_CONTROL_TOKEN"),
				Usage:   "full-control automation token (random token file when omitted)",
			},
			&cli.StringFlag{
				Name:    "server",
				Sources: cli.EnvVars("MULTICRUM_SERVER"),
				Aliases: []string{"srv", "S"},
				Value:   "default",
				Usage:   "named local multicrum server to attach/create",
			},
			&cli.StringFlag{
				Name:    "config",
				Sources: cli.EnvVars("MULTICRUM_CONFIG"),
				Value:   "multicrum.yaml",
				Usage:   "path to layout YAML file; loaded on startup if it exists, saved with Ctrl+Alt+P",
			},
			&cli.BoolFlag{
				Name:   "owner",
				Hidden: true,
			},
			&cli.BoolFlag{
				Name:   "daemon-bootstrap",
				Hidden: true,
			},
		}, webFlags()...),
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
			{
				Name:  "call",
				Usage: "call a control protocol method",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "method",
						Sources: cli.EnvVars("MULTICRUM_CALL_METHOD"),
						Usage:   "control protocol method",
					},
					&cli.StringFlag{
						Name:    "params",
						Sources: cli.EnvVars("MULTICRUM_CALL_PARAMS"),
						Value:   "{}",
						Usage:   "JSON request parameters",
					},
					&cli.DurationFlag{
						Name:    "timeout",
						Sources: cli.EnvVars("MULTICRUM_CALL_TIMEOUT"),
						Value:   2 * time.Minute,
						Usage:   "request timeout",
					},
				},
				Action: callControl,
			},
		},
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			level, err := resolveLogLevel(c, nil)
			if err != nil {
				return ctx, err
			}
			configureAppLogging(level)
			return ctx, nil
		},
		Action: run,
	}
}

func main() {
	if err := newCLICommand().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func callControl(ctx context.Context, c *cli.Command) error {
	method := strings.TrimSpace(c.String("method"))
	if method == "" {
		return fmt.Errorf("--method is required")
	}
	var params any
	if err := json.Unmarshal([]byte(c.String("params")), &params); err != nil {
		return fmt.Errorf("invalid --params JSON: %w", err)
	}
	server := ""
	if c.IsSet("server") {
		server = normalizedServerName(c)
	}
	client, err := control.Dial(control.Options{
		Server: server, ClientName: "multicrum-call",
	})
	if err != nil {
		return err
	}
	defer client.Close()

	callCtx, cancel := context.WithTimeout(ctx, c.Duration("timeout"))
	defer cancel()
	var result json.RawMessage
	if err := client.Call(callCtx, method, params, &result); err != nil {
		return err
	}
	if len(result) == 0 {
		fmt.Fprintln(os.Stdout, "null")
		return nil
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, result, "", "  "); err != nil {
		return fmt.Errorf("format control response: %w", err)
	}
	fmt.Fprintln(os.Stdout, formatted.String())
	return nil
}

func run(ctx context.Context, c *cli.Command) error {
	serverName := normalizedServerName(c)
	socketPath, err := localserver.SocketPath(serverName)
	if err != nil {
		return err
	}
	if c.Bool("daemon-bootstrap") {
		return finishDetachedOwner(os.Args, serverName)
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
	if err := announceNewOwnerLB(ctx, socketPath, serverName, os.Stdin, os.Stdout); err != nil {
		return err
	}
	if attached, attachErr := localserver.TryAttach(socketPath, serverName, os.Stdin, os.Stdout); attached {
		return attachErr
	}
	return fmt.Errorf("server %q started but could not be attached", serverName)
}

func runOwner(ctx context.Context, c *cli.Command, serverName, socketPath string, detachedOwner bool) error {
	configPath := c.String("config")
	var cfg *config.Config
	var configErr error
	if configPath != "" {
		cfg, configErr = config.Load(configPath)
		if configErr != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", configErr)
		}
	}
	level, err := resolveLogLevel(c, cfg)
	if err != nil {
		return err
	}
	configureAppLogging(level)
	logrus.Debugf("owner startup: server=%q pid=%d detached=%t", serverName, os.Getpid(), detachedOwner)
	effectiveConfig := config.Config{Server: serverName}
	if cfg != nil {
		effectiveConfig = *cfg
	}
	effectiveConfig.LogLevel = level.String()
	cfg = &effectiveConfig

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
	defer model.CloseAgentDetection()
	model.SetAgentCmdLine(agentCmdLine)
	model.SetServerName(serverName)

	model.SetConfigPath(configPath)
	if configErr == nil {
		model.SetConfigConnections(cfg)
	}
	model.SetLogLevel(level.String())
	webOptions, err := resolveWebOptions(c, cfg, serverName)
	if err != nil {
		return err
	}
	if webOptions.lbEnabled() && configErr != nil {
		return fmt.Errorf("reverse LB configuration: %w", configErr)
	}
	startupCtx, cancelStartup := context.WithCancel(ctx)
	defer cancelStartup()

	var input io.Reader
	if !detachedOwner {
		input = os.Stdin
	}
	inputMux := ui.NewInputMux(input)
	model.SetInputMux(inputMux)
	settings := serverSettings(c, agentCmdLine)
	settings.WS = webOptions.config.Address
	settings.TokenSet = webOptions.token != ""
	if webOptions.lbEnabled() {
		settings.ReverseLB = &localserver.LBStatus{}
	}
	owner, err := localserver.ListenWithSettings(socketPath, serverName, inputMux, settings)
	if err != nil {
		return err
	}
	defer owner.Close()
	logPath, err := localserver.LogPath(serverName)
	if err != nil {
		return err
	}
	tracePath := strings.TrimSuffix(logPath, ".log") + ".trace.log"
	trace, err := diagnostics.New(tracePath, log.Default())
	if err != nil {
		return fmt.Errorf("owner diagnostics: %w", err)
	}
	defer trace.Close()
	model.SetDiagnostics(trace)
	trace.Record("owner.init server=%q pid=%d detached=%t terminal=%dx%d", serverName, os.Getpid(), detachedOwner, cols, rows)
	log.Printf("owner diagnostics: server=%q pid=%d checkpoint=%q", serverName, os.Getpid(), tracePath)

	var output io.Writer = owner
	if !detachedOwner {
		output = localserver.FanoutWriter{Primary: os.Stdout, Mirror: owner}
	}
	model.SetClipboardOutput(output)
	model.SetClipboardHandler(owner.WriteClipboard)
	model.SetDetachHandler(owner.DetachActiveClient)

	var p *tea.Program
	p = tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithColorProfile(colorprofile.TrueColor),
		tea.WithInput(inputMux),
		tea.WithOutput(ui.NewKeyboardStripWriter(output)),
		tea.WithoutSignalHandler(),
	)
	model.SetProgram(p)

	controlEndpoint, err := control.Endpoint(serverName)
	if err != nil {
		return fmt.Errorf("control endpoint: %w", err)
	}
	controlToken := c.String("control-token")
	if controlToken == "" {
		controlToken = control.NewToken()
	}
	tokenPath := control.TokenPath(controlEndpoint)
	if err := os.WriteFile(tokenPath, []byte(controlToken+"\n"), 0o600); err != nil {
		return fmt.Errorf("write control token: %w", err)
	}
	defer os.Remove(tokenPath)
	controlService := control.NewService(serverName, controlEndpoint, controlToken, model)
	model.SetControlService(controlService)
	if err := controlService.Start(); err != nil {
		return fmt.Errorf("control service: %w", err)
	}
	defer controlService.Close()
	owner.SetCallbacks(func(n int) {
		log.Printf("owner clients: server=%q pid=%d attached=%d", serverName, os.Getpid(), n)
		trace.Record("client.count source=local-attach attached=%d", n)
		if p != nil {
			go p.Send(ui.LocalClientCountMsg(n))
		}
	}, func(cols, rows int) {
		trace.Record("resize.request source=local-attach terminal=%dx%d", cols, rows)
		if p != nil {
			go p.Send(tea.WindowSizeMsg{Width: cols, Height: rows})
		}
	}, func(action string) {
		if action == "stop" && p != nil {
			log.Printf("owner stop requested: server=%q pid=%d source=local-control", serverName, os.Getpid())
			trace.Record("owner.stop-request source=local-control")
			cancelStartup()
			go p.Kill()
		}
	})

	if webOptions.config.Address != "" || webOptions.config.ReverseLB != nil || cfg != nil && cfg.Web != nil {
		model.SetWebConfig(&webOptions.config)
	}
	if wsAddr := webOptions.config.Address; wsAddr != "" {
		wst, err := ui.StartWSTransport(wsAddr, webOptions.token, model)
		if err != nil {
			return fmt.Errorf("ws transport: %w", err)
		}
		defer wst.Close()
		if webOptions.lbEnabled() {
			data, err := reverseLBTunnelData(wst.Addr().String(), webOptions)
			if err != nil {
				return err
			}
			lb, err := tunnel.NewMuxTunnelClient(webOptions.config.ReverseLB.APIEndpoint, data)
			if err != nil {
				return fmt.Errorf("reverse LB client: %w", err)
			}
			defer lb.Close()
			waitCtx, stopSignals := signal.NotifyContext(startupCtx, syscall.SIGINT, syscall.SIGTERM)
			ready, err := waitReverseLBReady(waitCtx, lb)
			stopSignals()
			if err != nil {
				return err
			}
			if err := persistWebConfig(configPath, cfg, webOptions.config, serverName); err != nil {
				return err
			}
			owner.SetLBStatus(ready)
			log.Printf("reverse LB ready: frontend=%q port=%d publication=%q", ready.FrontendAddress, ready.FrontendPort, ready.PublicationMode)
		}
		fmt.Fprintf(os.Stderr, "xterm.js UI on http://%s/\n", wst.Addr())
	}

	err = runOwnerProgram(p, serverName, trace)
	_, _ = io.WriteString(output, ui.TerminalCleanupSequence)
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
	if lb := settings.ReverseLB; lb != nil {
		if !lb.Ready {
			parts = append(parts, "lb=connecting")
		} else if lb.FrontendPort > 0 {
			parts = append(parts, fmt.Sprintf("lb-port=%d", lb.FrontendPort))
		} else {
			parts = append(parts, "lb=bindings-only")
		}
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
	out := make([]string, 0, len(args)+1)
	for _, arg := range args {
		if arg != "--daemon-bootstrap" {
			out = append(out, arg)
		}
	}
	for _, arg := range out[1:] {
		if arg == "--owner" {
			return out
		}
	}
	return append(out, "--owner")
}

func daemonBootstrapArgs(args []string) []string {
	out := make([]string, 0, len(args)+1)
	for _, arg := range args {
		if arg != "--owner" && arg != "--daemon-bootstrap" {
			out = append(out, arg)
		}
	}
	return append(out, "--daemon-bootstrap")
}
