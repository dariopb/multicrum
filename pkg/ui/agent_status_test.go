package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"multicrum/pkg/agentdetect"
	"multicrum/pkg/config"
	"multicrum/pkg/session"
)

func TestAgentStatusFollowsStableSessionAcrossReindex(t *testing.T) {
	m := NewModel([]string{"sh"}, 100, 30)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	first, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start first session: %v", err)
	}
	second, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start second session: %v", err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()

	setTestAgentStatus(t, m.s, first, agentdetect.StateWorking)
	setTestAgentStatus(t, m.s, second, agentdetect.StateBlocked)
	if got := m.s.connectionAgentLabel(m.s.connections[0]); got != "  blocked Copilot" {
		t.Fatalf("aggregate label = %q, want %q", got, "  blocked Copilot")
	}

	manager.Move(0, 1)
	if got := m.s.sessionAgentLabel(first); got != "⠋ working Copilot" {
		t.Fatalf("moved session label = %q, want %q", got, "⠋ working Copilot")
	}
	manager.Kill(0)
	if got := m.s.sessionAgentLabel(first); got != "⠋ working Copilot" {
		t.Fatalf("reindexed session label = %q, want %q", got, "⠋ working Copilot")
	}
}

func TestCopilotScreenPromotesProcessPresenceToWorking(t *testing.T) {
	m := NewModel([]string{"sh"}, 100, 30)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()

	id, generation, _, _ := sess.RuntimeSnapshot()
	status := agentdetect.Status{
		Provider: agentdetect.ProviderCopilot,
		State:    agentdetect.StateUnknown, Source: agentdetect.SourceProcess,
		Confidence: agentdetect.ConfidenceHigh, UpdatedAt: time.Now(),
	}
	if !m.s.applyAgentUpdate(agentdetect.Update{ID: id, Generation: generation, Status: &status}) {
		t.Fatal("process presence update was not applied")
	}
	sess.Screen().Write([]byte("\r\n◉ Working · 26.1 KiB esc interrupt   GPT-5\r\n"))
	m.s.evaluateCopilotSession(sess)

	detected, ok := m.s.agentStatus(sess)
	if !ok {
		t.Fatal("Copilot status missing")
	}
	if detected.State != agentdetect.StateWorking || detected.Source != agentdetect.SourceScreen {
		t.Fatalf("status = %q/%q, want working/screen; lines=%#v", detected.State, detected.Source, sess.Screen().VisibleLines())
	}
	if label := m.s.sessionAgentLabel(sess); !strings.Contains(label, "working") {
		t.Fatalf("label = %q, want working", label)
	}
}

func TestAgentStateStyles(t *testing.T) {
	base := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	tests := []struct {
		state agentdetect.State
		want  string
	}{
		{agentdetect.StateWorking, "220"},
		{agentdetect.StateBlocked, "218"},
		{agentdetect.StateIdle, "151"},
	}
	for _, test := range tests {
		status := agentdetect.Status{State: test.state}
		if got := fmt.Sprint(agentStateStyle(base, status).GetForeground()); got != test.want {
			t.Fatalf("%s foreground = %q, want %q", test.state, got, test.want)
		}
	}
	if !agentProviderStyle(base).GetFaint() {
		t.Fatal("agent provider style is not faint")
	}
}

func TestAgentSpinnerUsesFixedPrefixAndCanBeDisabled(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	working := agentdetect.Status{Provider: agentdetect.ProviderCopilot, State: agentdetect.StateWorking}
	blocked := agentdetect.Status{Provider: agentdetect.ProviderCopilot, State: agentdetect.StateBlocked}
	if got := m.s.agentSpinnerSlot(working); got != "⠋ " {
		t.Fatalf("working spinner slot = %q, want %q", got, "⠋ ")
	}
	if got := m.s.agentSpinnerSlot(blocked); got != "  " {
		t.Fatalf("blocked spinner slot = %q, want two spaces", got)
	}
	if got := ansi.Strip(m.s.renderAgentLine(railInactiveStyle, working, 1, 30)); !strings.HasPrefix(got, "⠋ working Copilot") {
		t.Fatalf("working line = %q, spinner is not in column one", got)
	}
	if got := ansi.Strip(m.s.renderAgentLine(railInactiveStyle, blocked, 1, 30)); !strings.HasPrefix(got, "  blocked Copilot") {
		t.Fatalf("blocked line = %q, state text did not retain its column", got)
	}

	m.s.agentStatuses["working"] = detectedAgent{status: working}
	if cmd := m.s.startAgentSpinner(); cmd == nil {
		t.Fatal("enabled working spinner did not schedule a tick")
	}
	m.s.agentSpinnerRunning = false
	m.s.agentSpinnerEnabled = false
	if cmd := m.s.startAgentSpinner(); cmd != nil {
		t.Fatal("disabled spinner scheduled a tick")
	}
}

func TestAgentSpinnerSupportsRectangleAndCircleFrames(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	working := agentdetect.Status{Provider: agentdetect.ProviderCopilot, State: agentdetect.StateWorking}
	if got := m.s.agentSpinnerFrames(); !reflect.DeepEqual(got, []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}) {
		t.Fatalf("rectangle spinner frames = %#v", got)
	}
	m.s.agentSpinnerStyle = config.AgentSpinnerStyleCircle
	if got := m.s.agentSpinnerFrames(); !reflect.DeepEqual(got, []string{"●", "◉", "◎", "○"}) {
		t.Fatalf("circle spinner frames = %#v", got)
	}
	if got := m.s.agentSpinnerSlot(working); got != "● " {
		t.Fatalf("circle spinner slot = %q, want %q", got, "● ")
	}
	m.s.agentSpinnerFrame = 6
	m.s.agentSpinnerEnabled = false
	if got := m.s.agentSpinnerSlot(working); got != "● " {
		t.Fatalf("disabled circle spinner slot = %q, want static first frame", got)
	}
}

func TestAgentMetadataAnimationFollowsConfiguration(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	setTestAgentStatus(t, m.s, sess, agentdetect.StateWorking)
	if info := m.s.sessionAgentInfo(sess); info == nil || !info.Animate ||
		info.Spinner != config.AgentSpinnerStyleRectangle {
		t.Fatalf("enabled metadata = %#v, want animated", info)
	}
	m.s.agentSpinnerStyle = config.AgentSpinnerStyleCircle
	if info := m.s.sessionAgentInfo(sess); info == nil ||
		info.Spinner != config.AgentSpinnerStyleCircle {
		t.Fatalf("circle metadata = %#v", info)
	}
	m.s.agentSpinnerEnabled = false
	if info := m.s.sessionAgentInfo(sess); info == nil || info.Animate {
		t.Fatalf("disabled metadata = %#v, want static", info)
	}
}

func TestCopilotReadyScreenBecomesIdleOrDone(t *testing.T) {
	m := NewModel([]string{"sh"}, 100, 30)
	firstManager := session.NewManager(80, 24, nil, nil)
	secondManager := session.NewManager(80, 24, nil, nil)
	defer firstManager.CloseAll()
	defer secondManager.CloseAll()
	viewed, err := firstManager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start viewed session: %v", err)
	}
	background, err := secondManager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start background session: %v", err)
	}
	m.s.connections[0].manager = firstManager
	backgroundConn := m.s.addConnection("background")
	backgroundConn.manager = secondManager
	m.s.syncActiveConnectionFields()
	setTestAgentStatus(t, m.s, viewed, agentdetect.StateWorking)
	setTestAgentStatus(t, m.s, background, agentdetect.StateWorking)

	ready := []byte("\r\n← open sidebar · / commands · ? help · tab next tab   Claude Haiku 4.5\r\n")
	viewed.Screen().Write(ready)
	background.Screen().Write(ready)
	m.s.evaluateCopilotSession(viewed)
	m.s.evaluateCopilotSession(background)

	if status, _ := m.s.agentStatus(viewed); status.State != agentdetect.StateIdle {
		t.Fatalf("viewed ready state = %q, want idle", status.State)
	}
	if status, _ := m.s.agentStatus(background); status.State != agentdetect.StateDone {
		t.Fatalf("background ready state = %q, want done", status.State)
	}

	m.s.focusConnection(1)
	if status, _ := m.s.agentStatus(background); status.State != agentdetect.StateIdle {
		t.Fatalf("focused done state = %q, want idle", status.State)
	}
}

func TestShortConnectionRailKeepsFooterWithAgentStatus(t *testing.T) {
	m := NewModel([]string{"sh"}, 100, 6)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	m.s.connectionLayout = connectionLayoutLeft
	setTestAgentStatus(t, m.s, sess, agentdetect.StateWorking)

	rows := m.renderConnectionRail(m.s.geometry())
	if len(rows) == 0 || !strings.Contains(rows[len(rows)-1], "New") ||
		!strings.Contains(rows[len(rows)-1], "Alt+`") {
		t.Fatalf("short rail footer was overwritten: %#v", rows)
	}
}

func TestConnectionRailAgentRowsAlignWithoutNumberPrefix(t *testing.T) {
	m := NewModel([]string{"sh"}, 100, 12)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	m.s.connectionLayout = connectionLayoutLeft
	setTestAgentStatus(t, m.s, sess, agentdetect.StateWorking)

	rows := m.renderConnectionRail(m.s.geometry())
	if got := ansi.Strip(rows[3]); !strings.HasPrefix(got, "default") || strings.Contains(got, "[1]") {
		t.Fatalf("connection row = %q, want name without number prefix", got)
	}
	if got := ansi.Strip(rows[4]); !strings.HasPrefix(got, "  1 sessions") {
		t.Fatalf("session row = %q, want text at column two", got)
	}
	if got := ansi.Strip(rows[5]); !strings.HasPrefix(got, "⠋ working ") {
		t.Fatalf("state row = %q, want spinner at column zero and text at column two", got)
	}
}

func setTestAgentStatus(t *testing.T, s *state, sess *session.Session, agentState agentdetect.State) {
	t.Helper()
	id, generation, _, _ := sess.RuntimeSnapshot()
	status := agentdetect.Status{
		Provider: agentdetect.ProviderCopilot,
		State:    agentState, Source: agentdetect.SourceScreen,
		Confidence: agentdetect.ConfidenceHigh, UpdatedAt: time.Now(),
	}
	if !s.applyAgentUpdate(agentdetect.Update{ID: id, Generation: generation, Status: &status}) {
		t.Fatalf("apply %s status returned unchanged", agentState)
	}
}
