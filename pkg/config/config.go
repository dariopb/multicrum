package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

type SSHEntry struct {
	Target                string `yaml:"target,omitempty"`
	Port                  string `yaml:"port,omitempty"`
	Key                   string `yaml:"key,omitempty"`
	UseDefaultKeys        bool   `yaml:"useDefaultKeys,omitempty"`
	UseAgent              bool   `yaml:"useAgent,omitempty"`
	KnownHosts            string `yaml:"knownHosts,omitempty"`
	InsecureIgnoreHostKey bool   `yaml:"insecureIgnoreHostKey,omitempty"`
}

type SessionEntry struct {
	Title   string    `yaml:"title,omitempty"`
	CmdLine string    `yaml:"cmdline,omitempty"`
	Cmd     []string  `yaml:"cmd,omitempty"`
	Cwd     string    `yaml:"cwd,omitempty"`
	SSH     *SSHEntry `yaml:"ssh,omitempty"`
}

type ConnectionEntry struct {
	Name     string         `yaml:"name"`
	Sessions []SessionEntry `yaml:"sessions"`
}

const (
	AgentSpinnerStyleRectangle = "rectangle"
	AgentSpinnerStyleCircle    = "circle"
)

type AgentDetectionConfig struct {
	SpinnerAnimation *bool  `yaml:"spinnerAnimation,omitempty" json:"spinnerAnimation,omitempty"`
	SpinnerStyle     string `yaml:"spinnerStyle,omitempty" json:"spinnerStyle,omitempty"`
}

type SelectionConfig struct {
	CopyOnRelease *bool `yaml:"copyOnRelease,omitempty" json:"copyOnRelease,omitempty"`
}

type Config struct {
	Server              string                `yaml:"server,omitempty"`
	ActiveConnection    string                `yaml:"activeConnection,omitempty"`
	ConnectionLayout    string                `yaml:"connectionLayout,omitempty" json:"connectionLayout,omitempty"`
	ConnectionRailWidth int                   `yaml:"connectionRailWidth,omitempty" json:"connectionRailWidth,omitempty"`
	AgentDetection      *AgentDetectionConfig `yaml:"agentDetection,omitempty" json:"agentDetection,omitempty"`
	Selection           *SelectionConfig      `yaml:"selection,omitempty" json:"selection,omitempty"`
	Connections         []ConnectionEntry     `yaml:"connections,omitempty"`
	Sessions            []SessionEntry        `yaml:"sessions,omitempty"`
}

func (c *Config) AgentSpinnerAnimationEnabled() bool {
	return c == nil || c.AgentDetection == nil ||
		c.AgentDetection.SpinnerAnimation == nil ||
		*c.AgentDetection.SpinnerAnimation
}

func (c *Config) AgentSpinnerStyle() string {
	if c != nil && c.AgentDetection != nil {
		switch c.AgentDetection.SpinnerStyle {
		case AgentSpinnerStyleCircle:
			return AgentSpinnerStyleCircle
		case AgentSpinnerStyleRectangle:
			return AgentSpinnerStyleRectangle
		}
	}
	return AgentSpinnerStyleRectangle
}

func (c *Config) CopySelectionOnReleaseEnabled() bool {
	return c == nil || c.Selection == nil ||
		c.Selection.CopyOnRelease == nil ||
		*c.Selection.CopyOnRelease
}

func (c *Config) Normalize() *Config {
	if c == nil {
		return nil
	}
	out := *c
	switch out.ConnectionLayout {
	case "left", "bottom":
	default:
		out.ConnectionLayout = "bottom"
	}
	if out.AgentDetection != nil {
		agentDetection := *out.AgentDetection
		switch agentDetection.SpinnerStyle {
		case AgentSpinnerStyleRectangle, AgentSpinnerStyleCircle:
		default:
			agentDetection.SpinnerStyle = AgentSpinnerStyleRectangle
		}
		out.AgentDetection = &agentDetection
	}
	if len(out.Connections) == 0 && len(out.Sessions) > 0 {
		out.Connections = []ConnectionEntry{{Name: "default", Sessions: out.Sessions}}
		if out.ActiveConnection == "" {
			out.ActiveConnection = "default"
		}
	}
	for i := range out.Connections {
		if out.Connections[i].Name == "" {
			out.Connections[i].Name = fmt.Sprintf("connection-%d", i+1)
		}
	}
	if out.ActiveConnection == "" && len(out.Connections) > 0 {
		out.ActiveConnection = out.Connections[0].Name
	}
	return &out
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg.Normalize(), nil
}

func Save(path string, cfg *Config) error {
	if cfg != nil && len(cfg.Connections) > 0 {
		cfg = cfg.Normalize()
		cfg.Sessions = nil
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename config %q: %w", path, err)
	}
	return nil
}
