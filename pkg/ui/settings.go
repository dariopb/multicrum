package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/config"
)

const (
	settingsSpinnerStyle = iota
	settingsSpinnerAnimation
	settingsCount
)

func (s *state) openSettings() {
	s.mode = modeSettings
	s.settingsCursor = 0
	s.settingsDirty = false
}

func (s *state) closeSettings() {
	s.mode = modeNormal
	if s.settingsDirty && s.configPath != "" {
		s.saveLayout()
	}
	s.settingsDirty = false
}

func (s *state) handleSettingsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Key().Code {
	case tea.KeyEscape:
		s.closeSettings()
		return nil
	case tea.KeyUp, tea.KeyKpUp:
		s.settingsCursor = (s.settingsCursor - 1 + settingsCount) % settingsCount
		return nil
	case tea.KeyDown, tea.KeyKpDown, tea.KeyTab:
		s.settingsCursor = (s.settingsCursor + 1) % settingsCount
		return nil
	case tea.KeyLeft, tea.KeyKpLeft:
		return s.changeSetting(-1)
	case tea.KeyRight, tea.KeyKpRight:
		return s.changeSetting(1)
	case tea.KeyEnter, ' ':
		return s.changeSetting(0)
	}
	return nil
}

func (s *state) changeSetting(direction int) tea.Cmd {
	switch s.settingsCursor {
	case settingsSpinnerStyle:
		if s.agentSpinnerStyle == config.AgentSpinnerStyleRectangle {
			s.agentSpinnerStyle = config.AgentSpinnerStyleCircle
		} else {
			s.agentSpinnerStyle = config.AgentSpinnerStyleRectangle
		}
		s.agentSpinnerFrame = 0
	case settingsSpinnerAnimation:
		switch direction {
		case -1:
			s.agentSpinnerEnabled = false
		case 1:
			s.agentSpinnerEnabled = true
		default:
			s.agentSpinnerEnabled = !s.agentSpinnerEnabled
		}
		s.agentSpinnerFrame = 0
		if !s.agentSpinnerEnabled {
			s.agentSpinnerRunning = false
		}
	}
	s.settingsDirty = true
	s.notifyMeta()
	if s.configPath != "" {
		s.saveLayout()
		s.settingsDirty = false
	}
	return s.startAgentSpinner()
}

func (m Model) renderSettingsModal() string {
	s := m.s
	animation := "off"
	if s.agentSpinnerEnabled {
		animation = "on"
	}
	rows := []string{
		"Settings",
		"",
		fmt.Sprintf("  Spinner style       < %s >", s.agentSpinnerStyle),
		fmt.Sprintf("  Spinner animation   < %s >", animation),
		"",
		"↑/↓ select   ←/→ change   Esc close",
	}
	for i := 0; i < settingsCount; i++ {
		row := i + 2
		if i == s.settingsCursor {
			rows[row] = selectorActiveStyle.Render("▶" + rows[row][1:])
		}
	}
	return padBox(rows, 42)
}

func (s *state) handleSettingsModalMouse(x, y int) tea.Cmd {
	switch y {
	case 2, 3:
		s.settingsCursor = y - 2
		return s.changeSetting(0)
	case 5:
		if _, ok := modalActionAt(x, "↑/↓ select   ←/→ change   Esc close", []modalAction{
			{label: "Esc close", key: escapeKey()},
		}); ok {
			s.closeSettings()
		}
	}
	return nil
}
