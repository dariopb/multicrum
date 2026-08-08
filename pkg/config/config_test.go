package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingReturnsNil(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil cfg for missing file, got %+v", cfg)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.yaml")
	in := &Config{
		Server:              "default",
		ActiveConnection:    "work",
		ConnectionLayout:    "left",
		ConnectionRailWidth: 22,
		Connections: []ConnectionEntry{
			{Name: "default", Sessions: []SessionEntry{{Cmd: []string{"bash"}, Cwd: "/tmp/project"}}},
			{Name: "work", Sessions: []SessionEntry{{Title: "logs", CmdLine: "tail -f /var/log/syslog"}}},
			{Name: "remote", Sessions: []SessionEntry{{Title: "ssh", CmdLine: "bash -l", SSH: &SSHEntry{Target: "user@example.com", Port: "2222", Key: "~/.ssh/id_ed25519", UseDefaultKeys: true, UseAgent: true}}}},
		},
	}

	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !reflect.DeepEqual(out, in) {
		t.Fatalf("roundtrip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestConnectionLayoutNormalizesToBottom(t *testing.T) {
	for _, layout := range []string{"", "sideways", "BOTTOM"} {
		cfg := (&Config{ConnectionLayout: layout}).Normalize()
		if cfg.ConnectionLayout != "bottom" {
			t.Fatalf("Normalize(%q) = %q, want bottom", layout, cfg.ConnectionLayout)
		}
	}
	for _, layout := range []string{"bottom", "left"} {
		cfg := (&Config{ConnectionLayout: layout}).Normalize()
		if cfg.ConnectionLayout != layout {
			t.Fatalf("Normalize(%q) = %q", layout, cfg.ConnectionLayout)
		}
	}
}

func TestLegacySessionsNormalizeToDefaultConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.yaml")
	if err := os.WriteFile(path, []byte("sessions:\n  - title: shell\n    cmd:\n      - bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveConnection != "default" || len(cfg.Connections) != 1 || cfg.Connections[0].Name != "default" {
		t.Fatalf("legacy normalization mismatch: %+v", cfg)
	}
	if len(cfg.Connections[0].Sessions) != 1 || cfg.Connections[0].Sessions[0].Title != "shell" {
		t.Fatalf("legacy sessions not preserved: %+v", cfg.Connections[0].Sessions)
	}
}

func TestLoadParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAgentSpinnerAnimationDefaultsOnAndCanBeDisabled(t *testing.T) {
	if !(&Config{}).AgentSpinnerAnimationEnabled() {
		t.Fatal("spinner animation should default to enabled")
	}
	disabled := false
	cfg := &Config{AgentDetection: &AgentDetectionConfig{SpinnerAnimation: &disabled}}
	if cfg.AgentSpinnerAnimationEnabled() {
		t.Fatal("explicitly disabled spinner animation reported enabled")
	}
	enabled := true
	cfg.AgentDetection.SpinnerAnimation = &enabled
	if !cfg.AgentSpinnerAnimationEnabled() {
		t.Fatal("explicitly enabled spinner animation reported disabled")
	}
}

func TestAgentSpinnerStyleDefaultsToRectangleAndSupportsCircle(t *testing.T) {
	if got := (&Config{}).AgentSpinnerStyle(); got != AgentSpinnerStyleRectangle {
		t.Fatalf("default spinner style = %q, want %q", got, AgentSpinnerStyleRectangle)
	}
	cfg := (&Config{AgentDetection: &AgentDetectionConfig{
		SpinnerStyle: AgentSpinnerStyleCircle,
	}}).Normalize()
	if got := cfg.AgentSpinnerStyle(); got != AgentSpinnerStyleCircle {
		t.Fatalf("configured spinner style = %q, want %q", got, AgentSpinnerStyleCircle)
	}
	cfg.AgentDetection.SpinnerStyle = "invalid"
	cfg = cfg.Normalize()
	if got := cfg.AgentSpinnerStyle(); got != AgentSpinnerStyleRectangle {
		t.Fatalf("invalid spinner style = %q, want fallback %q", got, AgentSpinnerStyleRectangle)
	}
}

func TestCopySelectionOnReleaseDefaultsOnAndCanBeDisabled(t *testing.T) {
	if !(&Config{}).CopySelectionOnReleaseEnabled() {
		t.Fatal("copy on release should default to enabled")
	}
	disabled := false
	cfg := &Config{Selection: &SelectionConfig{CopyOnRelease: &disabled}}
	if cfg.CopySelectionOnReleaseEnabled() {
		t.Fatal("explicitly disabled copy on release reported enabled")
	}
}
