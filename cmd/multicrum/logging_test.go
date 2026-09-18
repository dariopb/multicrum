package main

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
	"multicrum/pkg/config"
)

func parseApplicationLogLevel(t *testing.T, cfg *config.Config, args ...string) (logrus.Level, error) {
	t.Helper()
	command := newCLICommand()
	command.Before = nil
	var level logrus.Level
	command.Action = func(_ context.Context, c *cli.Command) (err error) {
		level, err = resolveLogLevel(c, cfg)
		return err
	}
	err := command.Run(context.Background(), append([]string{"multicrum"}, args...))
	return level, err
}

func TestApplicationLogLevel(t *testing.T) {
	clearCLIEnvironment(t)
	for _, test := range []struct {
		name string
		cfg  *config.Config
		args []string
		want logrus.Level
	}{
		{name: "default", want: logrus.InfoLevel},
		{name: "CLI", args: []string{"--log-level", "debug"}, want: logrus.DebugLevel},
		{name: "loglevel alias", args: []string{"--loglevel", "debug"}, want: logrus.DebugLevel},
		{name: "legacy alias", args: []string{"--lb-log-level", "debug"}, want: logrus.DebugLevel},
		{name: "YAML without LB", cfg: &config.Config{LogLevel: "debug"}, want: logrus.DebugLevel},
		{name: "CLI overrides YAML", cfg: &config.Config{LogLevel: "debug"}, args: []string{"--log-level", "info"}, want: logrus.InfoLevel},
		{name: "legacy YAML", cfg: &config.Config{Web: &config.WebConfig{ReverseLB: &config.ReverseLBConfig{LogLevel: "debug"}}}, want: logrus.DebugLevel},
		{name: "app YAML wins", cfg: &config.Config{LogLevel: "info", Web: &config.WebConfig{ReverseLB: &config.ReverseLBConfig{LogLevel: "debug"}}}, want: logrus.InfoLevel},
	} {
		t.Run(test.name, func(t *testing.T) {
			level, err := parseApplicationLogLevel(t, test.cfg, test.args...)
			if err != nil || level != test.want {
				t.Fatalf("log level = %v, %v; want %v", level, err, test.want)
			}
		})
	}
	if _, err := parseApplicationLogLevel(t, nil, "--log-level", "invalid"); err == nil {
		t.Fatal("invalid app log level accepted without LB")
	}
}

func TestApplicationLogEnvironment(t *testing.T) {
	clearCLIEnvironment(t)
	t.Setenv("MULTICRUM_LB_LOG_LEVEL", "debug")
	level, err := parseApplicationLogLevel(t, nil)
	if err != nil || level != logrus.DebugLevel {
		t.Fatalf("legacy environment not honored: %v", err)
	}
	t.Setenv("MULTICRUM_LOG_LEVEL", "info")
	level, err = parseApplicationLogLevel(t, &config.Config{LogLevel: "debug"})
	if err != nil || level != logrus.InfoLevel {
		t.Fatalf("app environment did not override YAML/legacy environment: %v", err)
	}
	level, err = parseApplicationLogLevel(t, nil, "--log-level", "debug")
	if err != nil || level != logrus.DebugLevel {
		t.Fatalf("CLI did not override environment: %v", err)
	}
}

func TestApplicationDebugOutputWithoutLB(t *testing.T) {
	clearCLIEnvironment(t)
	previousOutput, previousLevel := logrus.StandardLogger().Out, logrus.GetLevel()
	previousAppOutput := log.Writer()
	t.Cleanup(func() {
		log.SetOutput(previousAppOutput)
		logrus.SetOutput(previousOutput)
		logrus.SetLevel(previousLevel)
	})
	for _, level := range []string{"info", "debug"} {
		t.Run(level, func(t *testing.T) {
			var output bytes.Buffer
			log.SetOutput(&output)
			command := newCLICommand()
			command.Action = func(context.Context, *cli.Command) error {
				logrus.Debug("application action debug marker")
				return nil
			}
			if err := command.Run(context.Background(), []string{"multicrum", "--log-level", level}); err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"application debug logging enabled", "application action debug marker"} {
				if got := strings.Contains(output.String(), marker); got != (level == "debug") {
					t.Fatalf("%s: debug output mismatch for %q: %s", level, marker, output.String())
				}
			}
		})
	}
}
