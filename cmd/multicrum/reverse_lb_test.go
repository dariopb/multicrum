package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
	"multicrum/pkg/config"
)

func parseLBOptions(t *testing.T, cfg *config.Config, args ...string) (webOptions, error) {
	t.Helper()
	var options webOptions
	command := &cli.Command{
		Flags: append([]cli.Flag{&cli.StringFlag{Name: "config", Value: "layout.yaml"}}, webFlags()...),
		Action: func(_ context.Context, c *cli.Command) (err error) {
			options, err = resolveWebOptions(c, cfg, "work")
			return err
		},
	}
	err := command.Run(context.Background(), append([]string{"multicrum"}, args...))
	return options, err
}

func TestReverseLBOptions(t *testing.T) {
	t.Setenv("MULTICRUM_WEB_TOKEN", "")
	t.Setenv("MULTICRUM_LB_TOKEN", "test-secret")
	cfg := &config.Config{Web: &config.WebConfig{
		Address:   ":9999",
		ReverseLB: &config.ReverseLBConfig{APIEndpoint: "lb.example:443", FrontendPort: 8123, ServiceName: "saved"},
	}}
	got, err := parseLBOptions(t, cfg)
	if err != nil || !got.lbEnabled() || got.config.ReverseLB.FrontendPort != 8123 || got.lbToken != "test-secret" {
		t.Fatalf("saved settings/environment not loaded: %v", err)
	}
	got, err = parseLBOptions(t, cfg, "--ws", ":0", "--lb-api", "other.example:9999", "--lb-port", "auto", "--lb-service", "override")
	if err != nil || got.config.Address != ":0" || got.config.ReverseLB.APIEndpoint != "other.example:9999" ||
		got.config.ReverseLB.FrontendPort != 0 || got.config.ReverseLB.ServiceName != "override" {
		t.Fatalf("CLI overrides not applied: %+v, %v", got.config.ReverseLB, err)
	}
	if cfg.Web.Address != ":9999" || cfg.Web.ReverseLB.FrontendPort != 8123 {
		t.Fatal("resolving flags mutated the loaded config")
	}
	for _, args := range [][]string{{"--lb-api", ""}, {"--ws", ""}} {
		got, err := parseLBOptions(t, cfg, args...)
		if err != nil || got.lbEnabled() {
			t.Fatalf("explicit disabling failed for %v: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"--lb-api", "https://lb.example:443"}, {"--lb-port", "-1"},
		{"--lb-port", "65536"}, {"--lb-port", "bad"}, {"--config", ""},
	} {
		if _, err := parseLBOptions(t, cfg, args...); err == nil {
			t.Errorf("invalid options accepted: %v", args)
		}
	}
	got, err = parseLBOptions(t, nil, "--ws", ":0")
	if err != nil || got.lbEnabled() {
		t.Fatalf("LB enabled without an endpoint: %v", err)
	}
}

func TestReverseLBWrapTLS(t *testing.T) {
	t.Setenv("MULTICRUM_WEB_TOKEN", "")
	t.Setenv("MULTICRUM_LB_TOKEN", "")
	for _, test := range []struct {
		name  string
		saved bool
		args  []string
		want  bool
	}{
		{name: "default disabled"},
		{name: "CLI enabled", args: []string{"--lb-wrap-tls"}, want: true},
		{name: "YAML enabled", saved: true, want: true},
		{name: "CLI disables YAML", saved: true, args: []string{"--lb-wrap-tls=false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{Web: &config.WebConfig{
				Address: ":9999",
				ReverseLB: &config.ReverseLBConfig{
					APIEndpoint: "lb.example:443", ServiceName: "work", WrapTLS: test.saved,
				},
			}}
			options, err := parseLBOptions(t, cfg, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			data, err := reverseLBTunnelData("127.0.0.1:9999", options)
			if err != nil || data.FrontendData.TLSWrap != test.want || options.config.ReverseLB.WrapTLS != test.want {
				t.Fatalf("wrapTLS not passed through: %+v, %v", data.FrontendData, err)
			}
			if cfg.Web.ReverseLB.WrapTLS != test.saved {
				t.Fatal("CLI override mutated loaded YAML")
			}
		})
	}
}

func TestReverseLBWrapSSH(t *testing.T) {
	t.Setenv("MULTICRUM_WEB_TOKEN", "")
	t.Setenv("MULTICRUM_LB_TOKEN", "")
	for _, test := range []struct {
		name  string
		saved bool
		args  []string
		want  bool
	}{
		{name: "default disabled"},
		{name: "CLI enabled", args: []string{"--lb-wrap-ssh"}, want: true},
		{name: "YAML enabled", saved: true, want: true},
		{name: "CLI disables YAML", saved: true, args: []string{"--lb-wrap-ssh=false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{Web: &config.WebConfig{
				Address: ":9999",
				ReverseLB: &config.ReverseLBConfig{
					APIEndpoint: "lb.example:443", ServiceName: "work", WrapSSH: test.saved,
				},
			}}
			options, err := parseLBOptions(t, cfg, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			data, err := reverseLBTunnelData("127.0.0.1:9999", options)
			if err != nil || data.FrontendData.SSHWrap != test.want || options.config.ReverseLB.WrapSSH != test.want ||
				data.FrontendData.TLSWrap || options.config.ReverseLB.WrapTLS {
				t.Fatalf("wrapSSH not independently passed through: %+v, %v", data.FrontendData, err)
			}
			if cfg.Web.ReverseLB.WrapSSH != test.saved {
				t.Fatal("CLI override mutated loaded YAML")
			}
		})
	}
}

func TestReverseLBTunnelData(t *testing.T) {
	options := webOptions{
		config:  config.WebConfig{ReverseLB: &config.ReverseLBConfig{ServiceName: "work", FrontendPort: 0}},
		lbToken: "test-token",
	}
	for address, host := range map[string]string{
		"0.0.0.0:4321": "127.0.0.1", "[::]:4321": "::1", "127.0.0.1:4321": "127.0.0.1",
	} {
		data, err := reverseLBTunnelData(address, options)
		if err != nil || data.ServiceName != "work" || data.FrontendData.Port != 0 ||
			data.BackendAcceptBacklog != 1 || data.TargetPort != 4321 ||
			!reflect.DeepEqual(data.TargetAddresses, []string{host}) || data.Token != "test-token" {
			t.Fatalf("incorrect upstream tunnel parameters for %q: %v", address, err)
		}
	}
}

func TestReverseLBPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.yaml")
	loaded := &config.Config{
		Server: "work", LogLevel: "debug", ConnectionLayout: "left", ActiveConnection: "coding",
		Connections: []config.ConnectionEntry{{Name: "coding", Sessions: []config.SessionEntry{{CmdLine: "sh"}}}},
	}
	web := config.WebConfig{
		Address: ":9999", TokenRequired: true,
		ReverseLB: &config.ReverseLBConfig{APIEndpoint: "lb.example:443", ServiceName: "work", InstanceName: "work.example", FrontendPort: 0, WrapTLS: true, WrapSSH: true},
	}
	if err := persistWebConfig(path, loaded, web, "work"); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Web, &web) || !reflect.DeepEqual(saved.Connections, loaded.Connections) ||
		saved.ConnectionLayout != "left" || saved.LogLevel != "debug" || loaded.Web != nil {
		t.Fatal("web persistence changed existing layout or lost settings")
	}
	t.Setenv("MULTICRUM_WEB_TOKEN", "")
	if _, err := parseLBOptions(t, saved); err == nil {
		t.Fatal("restored protected web endpoint without its token")
	}
	t.Setenv("MULTICRUM_WEB_TOKEN", "test-web-secret")
	t.Setenv("MULTICRUM_LB_TOKEN", "test-lb-secret")
	if _, err := parseLBOptions(t, saved); err != nil {
		t.Fatalf("could not restore settings with environment tokens: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "test-web-secret") || strings.Contains(string(data), "test-lb-secret") {
		t.Fatalf("credentials persisted: %v", err)
	}
}
