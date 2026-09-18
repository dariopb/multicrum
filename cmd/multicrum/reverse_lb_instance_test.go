package main

import (
	"testing"

	"multicrum/pkg/config"
)

func TestReverseLBInstanceName(t *testing.T) {
	clearCLIEnvironment(t)
	for _, test := range []struct {
		name  string
		saved string
		args  []string
		want  string
	}{
		{name: "default"},
		{name: "YAML", saved: "saved.example", want: "saved.example"},
		{name: "CLI", saved: "saved.example", args: []string{"--lb-instance-name", "cli.example"}, want: "cli.example"},
		{name: "alias", args: []string{"--lb-instance", "alias.example"}, want: "alias.example"},
		{name: "clear", saved: "saved.example", args: []string{"--lb-instance-name", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{Web: &config.WebConfig{
				Address:   ":9999",
				ReverseLB: &config.ReverseLBConfig{APIEndpoint: "lb.example:443", ServiceName: "work", InstanceName: test.saved},
			}}
			options, err := parseLBOptions(t, cfg, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			data, err := reverseLBTunnelData("127.0.0.1:9999", options)
			wantService := "work"
			if test.want != "" {
				wantService += ":" + test.want
			}
			if err != nil || data.ServiceName != wantService || options.config.ReverseLB.InstanceName != test.want {
				t.Fatalf("instance not passed to upstream: service=%q, error=%v", data.ServiceName, err)
			}
			if cfg.Web.ReverseLB.InstanceName != test.saved {
				t.Fatal("CLI override mutated loaded configuration")
			}
		})
	}
}

func TestReverseLBEnvironmentOverridesYAML(t *testing.T) {
	clearCLIEnvironment(t)
	t.Setenv("MULTICRUM_WS", ":8001")
	t.Setenv("MULTICRUM_LB_API_ENDPOINT", "env.example:443")
	t.Setenv("MULTICRUM_LB_FRONTEND_PORT", "auto")
	t.Setenv("MULTICRUM_LB_SERVICE_NAME", "env-service")
	t.Setenv("MULTICRUM_LB_INSTANCE_NAME", "env-instance")
	t.Setenv("MULTICRUM_LB_WRAP_TLS", "false")
	t.Setenv("MULTICRUM_LB_WRAP_SSH", "false")
	cfg := &config.Config{Web: &config.WebConfig{
		Address: ":9999",
		ReverseLB: &config.ReverseLBConfig{
			APIEndpoint: "yaml.example:443", ServiceName: "yaml-service", InstanceName: "yaml-instance",
			FrontendPort: 8123, WrapTLS: true, WrapSSH: true,
		},
	}}
	options, err := parseLBOptions(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	lb := options.config.ReverseLB
	if options.config.Address != ":8001" || lb.APIEndpoint != "env.example:443" || lb.FrontendPort != 0 ||
		lb.ServiceName != "env-service" || lb.InstanceName != "env-instance" || lb.WrapTLS || lb.WrapSSH {
		t.Fatal("environment did not override persisted settings")
	}
	options, err = parseLBOptions(t, cfg, "--lb-wrap-tls=true", "--lb-wrap-ssh=true", "--lb-instance-name", "cli-instance")
	if err != nil || !options.config.ReverseLB.WrapTLS || !options.config.ReverseLB.WrapSSH || options.config.ReverseLB.InstanceName != "cli-instance" {
		t.Fatalf("CLI did not override environment: %v", err)
	}
	options, err = parseLBOptions(t, cfg, "--lb-api", "")
	if err != nil || options.lbEnabled() {
		t.Fatalf("explicit empty CLI endpoint did not override environment: %v", err)
	}
}
