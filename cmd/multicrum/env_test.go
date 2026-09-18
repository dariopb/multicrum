package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

func clearCLIEnvironment(t *testing.T) {
	t.Helper()
	root := newCLICommand()
	for _, command := range append([]*cli.Command{root}, root.Commands...) {
		for _, flag := range command.Flags {
			for _, env := range flag.(cli.DocGenerationFlag).GetEnvVars() {
				t.Setenv(env, "")
				if err := os.Unsetenv(env); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestPublicFlagsHaveEnvironmentSources(t *testing.T) {
	root := newCLICommand()
	for _, command := range append([]*cli.Command{root}, root.Commands...) {
		for _, flag := range command.Flags {
			envs := flag.(cli.DocGenerationFlag).GetEnvVars()
			if !flag.(cli.VisibleFlag).IsVisible() {
				if len(envs) != 0 {
					t.Errorf("internal flag %q has environment sources", flag.Names()[0])
				}
				continue
			}
			prefix := "MULTICRUM_"
			if command.Name == "call" {
				prefix += "CALL_"
			}
			name := flag.Names()[0]
			want := prefix + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
			if name == "token" {
				want = "MULTICRUM_WEB_TOKEN"
			}
			count := 1
			if name == "log-level" {
				count = 2
				if len(envs) != count || envs[1] != "MULTICRUM_LB_LOG_LEVEL" {
					t.Errorf("missing legacy logging environment source: %v", envs)
				}
			}
			if len(envs) != count || envs[0] != want {
				t.Errorf("%s: environment sources %v, want %s", name, envs, want)
			}
		}
	}
}

func TestHelpShowsEnvironmentSources(t *testing.T) {
	clearCLIEnvironment(t)
	for _, subcommand := range []string{"", "call"} {
		t.Run("help "+subcommand, func(t *testing.T) {
			root := newCLICommand()
			selected := root
			args := []string{"multicrum"}
			if subcommand != "" {
				args = append(args, subcommand)
				for _, child := range root.Commands {
					if child.Name == subcommand {
						selected = child
					}
				}
			}
			var envs []string
			for _, flag := range selected.Flags {
				envs = append(envs, flag.(cli.DocGenerationFlag).GetEnvVars()...)
			}
			var output bytes.Buffer
			root.Writer, root.ErrWriter = &output, &output
			if err := root.Run(context.Background(), append(args, "--help")); err != nil {
				t.Fatal(err)
			}
			for _, env := range envs {
				if !strings.Contains(output.String(), env) {
					t.Errorf("help omitted %s", env)
				}
			}
		})
	}
}

func TestRootEnvironmentAndCLIOverrides(t *testing.T) {
	clearCLIEnvironment(t)
	values := map[string]string{
		"cmd": "sh -l", "ssh": "user@example.com", "ssh-key": "/tmp/key",
		"ssh-passwd": "test-password", "ssh-use-default-keys": "true", "ssh-agent": "false",
		"ssh-known-hosts": "/tmp/known_hosts", "ssh-insecure-ignore-host-key": "true",
		"control-token": "test-control-token", "server": "env-server", "config": "/tmp/layout.yaml",
		"ws": "127.0.0.1:9999", "token": "test-web-token",
		"lb-api-endpoint": "lb.example:443", "lb-frontend-port": "8123", "lb-service-name": "env-service",
		"lb-instance-name": "env-instance",
		"lb-wrap-tls":      "true", "lb-wrap-ssh": "false", "log-level": "debug", "lb-token": "test-lb-token",
	}
	for _, flag := range newCLICommand().Flags {
		if value, ok := values[flag.Names()[0]]; ok {
			t.Setenv(flag.(cli.DocGenerationFlag).GetEnvVars()[0], value)
		}
	}
	for _, override := range []bool{false, true} {
		command := newCLICommand()
		args := []string{"multicrum"}
		if override {
			args = append(args, "--cmd", "sh", "--ssh-agent=true", "--lb-port", "auto", "--log-level", "info")
		}
		command.Action = func(_ context.Context, c *cli.Command) error {
			for name, want := range values {
				if override {
					switch name {
					case "cmd":
						want = "sh"
					case "ssh-agent":
						want = "true"
					case "lb-frontend-port":
						want = "auto"
					case "log-level":
						want = "info"
					}
				}
				if got := fmt.Sprint(c.Value(name)); got != want || !c.IsSet(name) {
					t.Errorf("%s did not use environment/CLI value (override=%t)", name, override)
				}
			}
			return nil
		}
		if err := command.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCallEnvironment(t *testing.T) {
	clearCLIEnvironment(t)
	t.Setenv("MULTICRUM_CALL_METHOD", "session.list")
	t.Setenv("MULTICRUM_CALL_PARAMS", `{"connection":"work"}`)
	t.Setenv("MULTICRUM_CALL_TIMEOUT", "7s")
	command := newCLICommand()
	called := false
	for _, child := range command.Commands {
		if child.Name == "call" {
			child.Action = func(_ context.Context, c *cli.Command) error {
				called = true
				if c.String("method") != "session.list" || c.String("params") != `{"connection":"work"}` || c.Duration("timeout") != 7*time.Second {
					t.Fatal("call options did not use environment sources")
				}
				return nil
			}
		}
	}
	if err := command.Run(context.Background(), []string{"multicrum", "call"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("call action was not invoked")
	}
}
