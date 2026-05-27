// Package config persists and loads the multicrum session layout to a YAML file.
//
// A config file is a list of session entries, each with a display title and
// the command to execute. Loading is best-effort: missing files are not
// errors, so users can launch multicrum without prior persistence.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// SessionEntry is one persisted session.
//
// Exactly one of CmdLine or Cmd should normally be set. CmdLine is the
// user-supplied shell line (preferred when present: it preserves shell
// syntax like process substitution `<(...)`, pipes, env-var expansion,
// etc.). Cmd is an argv-style token list and is used as a fallback when
// no shell line is available.
type SessionEntry struct {
	// Title is the user-visible display name shown in the tab bar.
	// Empty means "use the command name".
	Title string `yaml:"title,omitempty"`
	// CmdLine is the original command line as the user typed it.
	// Loaded by callers through ui.ParseCmdLine so shell metacharacters
	// are interpreted correctly at startup.
	CmdLine string `yaml:"cmdline,omitempty"`
	// Cmd is an argv-style token list, used only when CmdLine is empty.
	Cmd []string `yaml:"cmd,omitempty"`
}

// Config is the on-disk layout.
type Config struct {
	Sessions []SessionEntry `yaml:"sessions"`
}

// Load reads path and returns the parsed Config. If the file does not exist
// it returns (nil, nil) so callers can treat "no config" as "regular
// behavior". Any other error (permission, parse) is returned.
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
	return &cfg, nil
}

// Save writes cfg to path atomically (via a tempfile + rename when possible).
func Save(path string, cfg *Config) error {
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
