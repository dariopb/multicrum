package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	tunnel "github.com/dariopb/goreverselb/pkg"
	"github.com/urfave/cli/v3"
	"multicrum/pkg/config"
)

func webFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "ws", Sources: cli.EnvVars("MULTICRUM_WS"), Usage: "web listen address, overriding YAML; e.g. :9999"},
		&cli.StringFlag{Name: "token", Sources: cli.EnvVars("MULTICRUM_WEB_TOKEN"), Usage: "web authentication token (never saved)"},
		&cli.StringFlag{Name: "lb-api-endpoint", Aliases: []string{"lb-api"}, Sources: cli.EnvVars("MULTICRUM_LB_API_ENDPOINT"), Usage: "goreverselb API host:port; empty disables LB"},
		&cli.StringFlag{Name: "lb-frontend-port", Aliases: []string{"lb-port"}, Sources: cli.EnvVars("MULTICRUM_LB_FRONTEND_PORT"), Value: "auto", Usage: "frontend port; auto or 0 for automatic allocation"},
		&cli.StringFlag{Name: "lb-service-name", Aliases: []string{"lb-service"}, Sources: cli.EnvVars("MULTICRUM_LB_SERVICE_NAME"), Usage: "LB service name (default multicrum-HOST-SERVER)"},
		&cli.StringFlag{Name: "lb-instance-name", Aliases: []string{"lb-instance"}, Sources: cli.EnvVars("MULTICRUM_LB_INSTANCE_NAME"), Usage: "LB instance name (e.g. browser hostname for TLS/SNI routing)"},
		&cli.BoolFlag{Name: "lb-wrap-tls", Sources: cli.EnvVars("MULTICRUM_LB_WRAP_TLS"), Usage: "enable TLS termination on the LB frontend"},
		&cli.BoolFlag{Name: "lb-wrap-ssh", Sources: cli.EnvVars("MULTICRUM_LB_WRAP_SSH"), Usage: "enable SSH wrapping on the LB frontend"},
		&cli.StringFlag{Name: "lb-token", Sources: cli.EnvVars("MULTICRUM_LB_TOKEN"), Usage: "LB registration token (never saved)"},
	}
}

type webOptions struct {
	config  config.WebConfig
	token   string
	lbToken string
}

func (o webOptions) lbEnabled() bool {
	return o.config.Address != "" && o.config.ReverseLB != nil && o.config.ReverseLB.APIEndpoint != ""
}

func resolveWebOptions(c *cli.Command, cfg *config.Config, server string) (webOptions, error) {
	out := webOptions{token: c.String("token"), lbToken: c.String("lb-token")}
	if cfg != nil && cfg.Web != nil {
		out.config = *cfg.Web
	}
	lb := config.ReverseLBConfig{}
	if out.config.ReverseLB != nil {
		lb = *out.config.ReverseLB
	}
	out.config.ReverseLB = &lb
	if c.IsSet("ws") {
		out.config.Address = c.String("ws")
	}
	if out.config.Address != "" && out.config.TokenRequired && out.token == "" {
		return out, fmt.Errorf("saved web endpoint requires --token or MULTICRUM_WEB_TOKEN")
	}
	out.config.TokenRequired = out.config.TokenRequired || out.token != ""
	if c.IsSet("lb-api-endpoint") {
		lb.APIEndpoint = c.String("lb-api-endpoint")
	}
	if c.IsSet("lb-service-name") {
		lb.ServiceName = c.String("lb-service-name")
	}
	if c.IsSet("lb-instance-name") {
		lb.InstanceName = c.String("lb-instance-name")
	}
	if c.IsSet("lb-wrap-tls") {
		lb.WrapTLS = c.Bool("lb-wrap-tls")
	}
	if c.IsSet("lb-wrap-ssh") {
		lb.WrapSSH = c.Bool("lb-wrap-ssh")
	}
	if lb.APIEndpoint == "" {
		out.config.ReverseLB = nil
		return out, nil
	}
	if !out.lbEnabled() {
		return out, nil
	}
	if c.String("config") == "" {
		return out, fmt.Errorf("reverse LB requires a --config path to save settings")
	}
	host, port, err := net.SplitHostPort(lb.APIEndpoint)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, "/@?# \t\r\n") {
		return out, fmt.Errorf("LB API endpoint must be host:port, not an HTTP URL")
	}
	if c.IsSet("lb-frontend-port") {
		value := c.String("lb-frontend-port")
		lb.FrontendPort = 0
		if value != "auto" {
			lb.FrontendPort, err = strconv.Atoi(value)
			if err != nil {
				return out, fmt.Errorf("LB frontend port must be auto, 0, or 1-65535")
			}
		}
	}
	if lb.FrontendPort < 0 || lb.FrontendPort > 65535 {
		return out, fmt.Errorf("LB frontend port must be 0 (auto) or 1-65535")
	}
	if lb.ServiceName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return out, fmt.Errorf("derive LB service name: %w", err)
		}
		lb.ServiceName = strings.ReplaceAll("multicrum-"+hostname+"-"+server, ":", "-")
	}
	return out, nil
}

func reverseLBTunnelData(address string, options webOptions) (tunnel.TunnelData, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return tunnel.TunnelData{}, fmt.Errorf("web backend address: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return tunnel.TunnelData{}, fmt.Errorf("web backend port: %w", err)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	lb := options.config.ReverseLB
	serviceName := lb.ServiceName
	if lb.InstanceName != "" {
		serviceName += ":" + lb.InstanceName
	}
	return tunnel.TunnelData{
		ServiceName: serviceName, BackendAcceptBacklog: 1,
		FrontendData: tunnel.FrontendData{Port: lb.FrontendPort, TLSWrap: lb.WrapTLS, SSHWrap: lb.WrapSSH},
		TargetPort:   n, TargetAddresses: []string{host}, Token: options.lbToken,
	}, nil
}

func persistWebConfig(path string, loaded *config.Config, web config.WebConfig, server string) error {
	saved := config.Config{Server: server}
	if loaded != nil {
		saved = *loaded
	}
	saved.Web = &web
	if err := config.Save(path, &saved); err != nil {
		return fmt.Errorf("save reverse LB settings: %w", err)
	}
	return nil
}
