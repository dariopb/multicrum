package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/config"
)

func TestSettingsChangeSpinnerConfiguration(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	m.s.openSettings()
	if m.s.agentSpinnerStyle != config.AgentSpinnerStyleRectangle {
		t.Fatalf("default style = %q, want rectangle", m.s.agentSpinnerStyle)
	}

	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	if m.s.agentSpinnerStyle != config.AgentSpinnerStyleCircle {
		t.Fatalf("changed style = %q, want circle", m.s.agentSpinnerStyle)
	}
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	if m.s.agentSpinnerEnabled {
		t.Fatal("left did not disable spinner animation")
	}
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	if !m.s.agentSpinnerEnabled {
		t.Fatal("right did not enable spinner animation")
	}
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if m.s.agentSpinnerEnabled {
		t.Fatal("Enter did not toggle spinner animation")
	}
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	if m.s.copySelectionOnRelease {
		t.Fatal("left did not disable copy on selection")
	}

	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if m.s.mode != modeNormal {
		t.Fatalf("Esc left mode = %v, want normal", m.s.mode)
	}
}

func TestSettingsRenderAndMouseToggle(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	m.s.openSettings()
	modal := m.renderSettingsModal()
	for _, text := range []string{"Settings", "Spinner style", "rectangle", "Spinner animation", "on", "Copy on selection"} {
		if !strings.Contains(modal, text) {
			t.Fatalf("settings modal missing %q", text)
		}
	}
	m.s.handleSettingsModalMouse(2, 2)
	if m.s.agentSpinnerStyle != config.AgentSpinnerStyleCircle {
		t.Fatalf("mouse style = %q, want circle", m.s.agentSpinnerStyle)
	}
	m.s.handleSettingsModalMouse(2, 3)
	if m.s.agentSpinnerEnabled {
		t.Fatal("mouse did not toggle spinner animation")
	}
	m.s.handleSettingsModalMouse(2, 4)
	if m.s.copySelectionOnRelease {
		t.Fatal("mouse did not toggle copy on selection")
	}
}

func TestChangingSettingsPersistsToConfig(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	manager := contextMenuTestManager(t, 80, 24, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager
	path := filepath.Join(t.TempDir(), "multicrum.yaml")
	m.SetConfigPath(path)
	m.s.openSettings()

	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.s.handleSettingsKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load saved settings: %v", err)
	}
	if got := cfg.AgentSpinnerStyle(); got != config.AgentSpinnerStyleCircle {
		t.Fatalf("saved style = %q, want circle", got)
	}
	if cfg.AgentSpinnerAnimationEnabled() {
		t.Fatal("saved spinner animation is enabled")
	}
	if cfg.CopySelectionOnReleaseEnabled() {
		t.Fatal("saved copy on release is enabled")
	}
}

func TestRemoteSettingsChangeAndPersist(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	manager := contextMenuTestManager(t, 80, 24, 1)
	defer manager.CloseAll()
	m.s.manager = manager
	m.s.connections[0].manager = manager
	path := filepath.Join(t.TempDir(), "multicrum.yaml")
	m.SetConfigPath(path)

	m.s.applyRemoteSetting("spinnerStyle", config.AgentSpinnerStyleCircle)
	m.s.applyRemoteSetting("spinnerAnimation", "false")
	m.s.applyRemoteSetting("copyOnRelease", "false")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load saved settings: %v", err)
	}
	if got := cfg.AgentSpinnerStyle(); got != config.AgentSpinnerStyleCircle {
		t.Fatalf("saved style = %q, want circle", got)
	}
	if cfg.AgentSpinnerAnimationEnabled() {
		t.Fatal("saved spinner animation is enabled")
	}
	if cfg.CopySelectionOnReleaseEnabled() {
		t.Fatal("saved copy on release is enabled")
	}
}
