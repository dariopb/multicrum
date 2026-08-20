package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"multicrum/pkg/app"
	"multicrum/pkg/control"
	"multicrum/pkg/localserver"
)

type connectionResult struct {
	Connection struct {
		ID string `json:"connectionId"`
	} `json:"connection"`
}

type sessionResult struct {
	Session struct {
		ID         string `json:"sessionId"`
		Generation uint64 `json:"generation"`
		Title      string `json:"title"`
	} `json:"session"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "call" {
		if err := runCall(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "agents call:", err)
			os.Exit(1)
		}
		return
	}
	if err := runController(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agents:", err)
		os.Exit(1)
	}
}

func runController(args []string) error {
	flags := flag.NewFlagSet("agents", flag.ContinueOnError)
	server := flags.String("server", "default", "multicrum server name")
	cwd := flags.String("cwd", "", "working directory for the controlling Copilot")
	name := flags.String("connection", "agent-orchestrator", "visible connection name")
	trustCwd := flags.Bool("trust-cwd", false, "approve Copilot folder trust for this run")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *cwd == "" {
		var err error
		*cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	absoluteCwd, err := filepath.Abs(*cwd)
	if err != nil {
		return err
	}
	if info, err := os.Stat(absoluteCwd); err != nil || !info.IsDir() {
		return fmt.Errorf("cwd is not an accessible directory: %s", absoluteCwd)
	}
	fmt.Printf("starting controller: server=%s connection=%s cwd=%s\n", *server, *name, absoluteCwd)
	client, embeddedOwner, err := connectControl(*server)
	if err != nil {
		return err
	}
	defer client.Close()
	if embeddedOwner != nil {
		defer embeddedOwner.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var connection connectionResult
	createdConnection := false
	err = client.Call(ctx, "connection.create", map[string]any{
		"name":   *name,
		"labels": map[string]string{"sample": "agents", "role": "orchestrator"},
	}, &connection)
	if controlErr, ok := err.(*control.Error); ok && controlErr.Code == "already_exists" {
		var listed struct {
			Connections []struct {
				ID   string `json:"connectionId"`
				Name string `json:"name"`
			} `json:"connections"`
		}
		if err := client.Call(ctx, "connection.list", map[string]any{}, &listed); err != nil {
			return err
		}
		for _, candidate := range listed.Connections {
			if candidate.Name == *name {
				connection.Connection.ID = candidate.ID
				break
			}
		}
		if connection.Connection.ID == "" {
			return err
		}
	} else if err != nil {
		return err
	} else {
		createdConnection = true
	}

	var parent sessionResult
	shell := defaultShell()
	if err := client.Call(ctx, "session.create", map[string]any{
		"connectionId": connection.Connection.ID,
		"title":        "copilot-controller",
		"backend":      map[string]any{"kind": "local"},
		"cmd":          []string{shell},
		"cwd":          absoluteCwd,
		"focus":        true,
		"ownership":    map[string]any{"cleanup": "retain"},
		"labels":       map[string]string{"role": "controller", "sample": "agents"},
	}, &parent); err != nil {
		return err
	}
	setupComplete := false
	defer func() {
		if setupComplete {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		var current sessionResult
		if client.Call(cleanupCtx, "session.get", map[string]any{
			"sessionId": parent.Session.ID,
		}, &current) == nil {
			_ = client.Call(cleanupCtx, "session.close", map[string]any{
				"sessionId": current.Session.ID, "generation": current.Session.Generation, "mode": "remove",
			}, nil)
		}
		if createdConnection {
			_ = client.Call(cleanupCtx, "connection.remove", map[string]any{
				"connectionId": connection.Connection.ID, "sessionPolicy": "terminate",
			}, nil)
		}
	}()

	var delegated struct {
		ExpiresAt string `json:"expiresAt"`
	}
	if err := client.Call(ctx, "capability.delegate", map[string]any{
		"subjectSessionId": parent.Session.ID,
		"expiresInMs":      int64((4 * time.Hour) / time.Millisecond),
		"permissions": []string{
			"server.get", "connection.list", "session.create", "session.list",
			"session.get", "session.focus", "session.sendText", "session.paste", "session.subscribe",
			"session.await", "session.snapshot", "session.close",
		},
		"scope": map[string]any{
			"createdBySubject": true, "maxConnections": 0, "maxSessions": 12,
			"allowedCwdRoots": []string{absoluteCwd}, "allowSSH": false,
		},
	}, &delegated); err != nil {
		return err
	}

	var current sessionResult
	if err := client.Call(ctx, "session.get", map[string]any{"sessionId": parent.Session.ID}, &current); err != nil {
		return err
	}
	parent.Session.Generation = current.Session.Generation

	if err := sendShellCommand(ctx, client, parent.Session.ID, parent.Session.Generation, "copilot --no-mouse"); err != nil {
		return fmt.Errorf("start Copilot in controller shell: %w", err)
	}
	if err := waitForCopilotInput(ctx, client, parent.Session.ID, parent.Session.Generation, *trustCwd, absoluteCwd); err != nil {
		return err
	}

	root := sampleRepositoryRoot()
	tool := "multicrum call"
	skill := filepath.Join(root, "skills", "multicrum-control", "SKILL.md")
	skillContent, err := os.ReadFile(skill)
	if err != nil {
		return fmt.Errorf("read bundled multicrum-control skill: %w", err)
	}
	boot := strings.Join([]string{
		"Apply the bundled multicrum-control skill instructions included below; do not open the skill directory.",
		"You are the controlling agent for this visible multicrum workspace.",
		"For control requests run `" + tool + " --method METHOD --params 'JSON'`.",
		"Create all child sessions in connectionId " + connection.Connection.ID + "; do not create another connection.",
		"When the user asks for Copilot agents, create an ordinary shell session with cmd [" + strconvQuote(shell) + "], then call session.sendText with text \"copilot --no-mouse\" and submit true. Do not launch Copilot as the session root process and do not run the agents sample app.",
		"To terminate or remove a child, call session.close with mode \"terminate\" or \"remove\"; do not invent session.terminate or session.remove methods.",
		"According to each user request, create as many child agent or command sessions in that connection as useful.",
		"When the user asks to view or activate a child, call session.focus with its sessionId.",
		"Subscribe before prompting child agents, use stable IDs and generations, and retain sessions for human inspection.",
		"Do not perform delegated work yourself when an appropriate child session can do it.",
		"--- BEGIN MULTICRUM-CONTROL SKILL ---",
		string(skillContent),
		"--- END MULTICRUM-CONTROL SKILL ---",
	}, "\n")
	if err := sendPrompt(ctx, client, parent.Session.ID, parent.Session.Generation, boot); err != nil {
		return err
	}
	setupComplete = true

	fmt.Printf("attaching multicrum: server=%s connection=%s\n", *server, *name)
	if embeddedOwner != nil {
		return embeddedOwner.Attach(os.Stdin, os.Stdout)
	}
	socketPath, err := localserver.SocketPath(*server)
	if err != nil {
		return err
	}
	attached, err := localserver.TryAttach(socketPath, *server, os.Stdin, os.Stdout)
	if !attached && err == nil {
		return fmt.Errorf("multicrum server %q is not available", *server)
	}
	return err
}

func waitForCopilotInput(ctx context.Context, client *control.Client, sessionID string, generation uint64, trustCwd bool, cwd string) error {
	trustHandled := false
	var readySince time.Time
	for {
		var snapshot struct {
			Lines []string `json:"lines"`
		}
		if err := client.Call(ctx, "session.snapshot", map[string]any{
			"sessionId": sessionID, "generation": generation, "format": "plain-screen",
		}, &snapshot); err != nil {
			return err
		}
		screen := strings.Join(snapshot.Lines, "\n")
		if strings.Contains(screen, "Do you trust the files in this folder?") {
			readySince = time.Time{}
			if !trustCwd {
				return fmt.Errorf("Copilot requires folder trust for %s; review the directory and rerun with --trust-cwd", cwd)
			}
			if !trustHandled {
				if err := client.Call(ctx, "session.sendText", map[string]any{
					"sessionId": sessionID, "generation": generation, "text": "1", "submit": true,
				}, nil); err != nil {
					return err
				}
				trustHandled = true
			}
		} else if strings.Contains(screen, "Copilot v") || strings.Contains(screen, "Tip: /skills") {
			if readySince.IsZero() {
				readySince = time.Now()
			} else if time.Since(readySince) >= time.Second {
				return nil
			}
		} else {
			readySince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for Copilot input: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func connectControl(server string) (*control.Client, *app.Owner, error) {
	options := control.Options{Server: server, ClientName: "samples/agents"}
	client, firstErr := control.Dial(options)
	if firstErr == nil {
		return client, nil, nil
	}
	owner, err := app.Start(context.Background(), app.Options{
		Server: server, Command: []string{defaultShell()},
		ConnectionLayout: "left", Terminal: os.Stdout,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("%w; cannot start embedded owner: %v", firstErr, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		client, lastErr = control.Dial(options)
		if lastErr == nil {
			return client, owner, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	owner.Close()
	return nil, nil, fmt.Errorf("embedded multicrum server %q did not expose control endpoint: %w", server, lastErr)
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		if shell := os.Getenv("COMSPEC"); shell != "" {
			return shell
		}
		return "cmd.exe"
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "sh"
}

func sampleRepositoryRoot() string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func sendPrompt(ctx context.Context, client *control.Client, sessionID string, generation uint64, text string) error {
	if err := client.Call(ctx, "session.paste", map[string]any{
		"sessionId": sessionID, "generation": generation, "text": text,
	}, nil); err != nil {
		return err
	}
	return client.Call(ctx, "session.sendText", map[string]any{
		"sessionId": sessionID, "generation": generation, "text": "", "submit": true,
	}, nil)
}

func sendShellCommand(ctx context.Context, client *control.Client, sessionID string, generation uint64, command string) error {
	return client.Call(ctx, "session.sendText", map[string]any{
		"sessionId": sessionID, "generation": generation,
		"text": command, "submit": true,
	}, nil)
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func runCall(args []string) error {
	flags := flag.NewFlagSet("call", flag.ContinueOnError)
	method := flags.String("method", "", "control protocol method")
	params := flags.String("params", "{}", "JSON request parameters")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *method == "" {
		return fmt.Errorf("--method is required")
	}
	var value any
	if err := json.Unmarshal([]byte(*params), &value); err != nil {
		return fmt.Errorf("invalid --params JSON: %w", err)
	}
	client, err := control.Dial(control.Options{ClientName: "samples/agents-call"})
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var result json.RawMessage
	if err := client.Call(ctx, *method, value, &result); err != nil {
		return err
	}
	var formatted any
	if json.Unmarshal(result, &formatted) == nil {
		encoded, _ := json.MarshalIndent(formatted, "", "  ")
		fmt.Println(string(encoded))
	} else {
		fmt.Println(string(result))
	}
	return nil
}
