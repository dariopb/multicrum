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
	SSH     *SSHEntry `yaml:"ssh,omitempty"`
}

type ConnectionEntry struct {
	Name     string         `yaml:"name"`
	Sessions []SessionEntry `yaml:"sessions"`
}

type Config struct {
	Server           string            `yaml:"server,omitempty"`
	ActiveConnection string            `yaml:"activeConnection,omitempty"`
	Connections      []ConnectionEntry `yaml:"connections,omitempty"`
	Sessions         []SessionEntry    `yaml:"sessions,omitempty"`
}

func (c *Config) Normalize() *Config {
	if c == nil {
		return nil
	}
	out := *c
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
