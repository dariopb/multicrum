package ui

import (
	"reflect"
	"testing"

	"multicrum/pkg/config"
)

func TestSetConfigConnectionsParsesCmdLine(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	cfg := &config.Config{
		ActiveConnection: "work",
		Connections: []config.ConnectionEntry{
			{
				Name: "work",
				Sessions: []config.SessionEntry{
					{Title: "simple", CmdLine: "python -m http.server"},
					{Title: "shell", CmdLine: "echo $HOME"},
				},
			},
		},
	}
	m.SetConfigConnections(cfg)
	entries := m.s.connections[0].initialCfg
	if len(entries) != 2 {
		t.Fatalf("expected 2 startup entries, got %d", len(entries))
	}
	if want := []string{"python", "-m", "http.server"}; !reflect.DeepEqual(entries[0].Cmd, want) {
		t.Fatalf("simple cmd = %#v, want %#v", entries[0].Cmd, want)
	}
	if want := []string{"bash", "-c", "echo $HOME"}; !reflect.DeepEqual(entries[1].Cmd, want) {
		t.Fatalf("shell cmd = %#v, want %#v", entries[1].Cmd, want)
	}
}

func TestSetConfigConnectionsPreservesSSH(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	cfg := &config.Config{
		ActiveConnection: "work",
		Connections: []config.ConnectionEntry{
			{
				Name: "work",
				Sessions: []config.SessionEntry{
					{Title: "remote", CmdLine: "bash -l", SSH: &config.SSHEntry{Target: "user@example.com", Port: "2222", Key: "~/.ssh/id_ed25519", UseDefaultKeys: true, UseAgent: true}},
				},
			},
		},
	}
	m.SetConfigConnections(cfg)
	entries := m.s.connections[0].initialCfg
	if len(entries) != 1 || entries[0].SSH == nil {
		t.Fatalf("expected SSH startup entry, got %#v", entries)
	}
	if !reflect.DeepEqual(entries[0].SSH, cfg.Connections[0].Sessions[0].SSH) {
		t.Fatalf("ssh entry = %#v, want %#v", entries[0].SSH, cfg.Connections[0].Sessions[0].SSH)
	}
}
