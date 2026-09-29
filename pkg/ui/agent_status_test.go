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
	if got := m.s.connectionAgentLabel(m.s.connections[0]); got != "  blocked Copilot (2)" {
		t.Fatalf("aggregate label = %q, want %q", got, "  blocked Copilot (2)")
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

func newAgentSummaryModel(t *testing.T) (*Model, []*session.Session) {
	t.Helper()
	m := NewModel([]string{"sh"}, 100, 30)
	manager := session.NewManager(80, 24, nil, nil)
	t.Cleanup(func() { manager.CloseAll() })
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	var sessions []*session.Session
	for i := 0; i < 4; i++ {
		sess, err := manager.New([]string{"sh", "-c", "sleep 60"})
		if err != nil {
			t.Fatalf("start session: %v", err)
		}
		sessions = append(sessions, sess)
	}
	return m, sessions
}

func TestConnectionAgentSummaryCountsAllAgentsByActivity(t *testing.T) {
	m, sessions := newAgentSummaryModel(t)
	tests := []struct {
		name   string
		states []agentdetect.State
		want   agentdetect.State
	}{
		{"blocked wins", []agentdetect.State{agentdetect.StateWorking, agentdetect.StateBlocked, agentdetect.StateIdle}, agentdetect.StateBlocked},
		{"done cannot hide work", []agentdetect.State{agentdetect.StateDone, agentdetect.StateWorking, agentdetect.StateIdle}, agentdetect.StateWorking},
		{"work before done", []agentdetect.State{agentdetect.StateWorking, agentdetect.StateDone, agentdetect.StateIdle}, agentdetect.StateWorking},
		{"unknown prevents idle", []agentdetect.State{agentdetect.StateIdle, agentdetect.StateUnknown, agentdetect.StateDone}, agentdetect.StateUnknown},
		{"blocked before unknown", []agentdetect.State{agentdetect.StateUnknown, agentdetect.StateBlocked, agentdetect.StateDone}, agentdetect.StateBlocked},
		{"working before unknown", []agentdetect.State{agentdetect.StateUnknown, agentdetect.StateWorking, agentdetect.StateDone}, agentdetect.StateWorking},
		{"all idle", []agentdetect.State{agentdetect.StateIdle, agentdetect.StateIdle, agentdetect.StateIdle}, agentdetect.StateIdle},
		{"completed is idle", []agentdetect.State{agentdetect.StateDone, agentdetect.StateIdle, agentdetect.StateDone}, agentdetect.StateIdle},
		{"all completed", []agentdetect.State{agentdetect.StateDone, agentdetect.StateDone, agentdetect.StateDone}, agentdetect.StateIdle},
		{"one agent", []agentdetect.State{agentdetect.StateDone}, agentdetect.StateIdle},
		{"no agents", nil, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for i, sess := range sessions {
				id, generation, _, _ := sess.RuntimeSnapshot()
				update := agentdetect.Update{ID: id, Generation: generation}
				if i < len(test.states) {
					provider := agentdetect.ProviderCopilot
					if i == 1 {
						provider = agentdetect.ProviderCrush
					}
					update.Status = &agentdetect.Status{
						Provider: provider, State: test.states[i], Source: agentdetect.SourceNative,
					}
				}
				m.s.applyAgentUpdate(update)
			}
			// Reordering sessions must not change the count or winning state.
			for rotation := 0; rotation < len(sessions); rotation++ {
				status, count, ok := m.s.connectionAgentSummary(m.s.connections[0])
				if count != len(test.states) || status.State != test.want || ok != (count > 0) {
					t.Fatalf("summary = %s/%d/%v, want %s/%d", status.State, count, ok, test.want, len(test.states))
				}
				info := m.s.connectionAgentInfo(m.s.connections[0])
				if count == 0 {
					if info != nil {
						t.Fatalf("empty connection metadata = %#v", info)
					}
				} else if info == nil || info.State != string(test.want) || info.Count != count ||
					info.Animate != (test.want == agentdetect.StateWorking) {
					t.Fatalf("browser summary = %#v, want %s/%d", info, test.want, count)
				}
				m.s.manager.Move(0, len(sessions)-1)
			}
			for i, want := range test.states {
				if want == agentdetect.StateDone {
					if info := m.s.sessionAgentInfo(sessions[i]); info == nil || info.State != "done" {
						t.Fatalf("summary changed individual completion marker: %#v", info)
					}
				}
			}
		})
	}
}

func TestConnectionAgentCountTracksSessionLifecycle(t *testing.T) {
	m, sessions := newAgentSummaryModel(t)
	for _, sess := range sessions[:3] {
		setTestAgentStatus(t, m.s, sess, agentdetect.StateWorking)
	}
	assertCount := func(want int) {
		t.Helper()
		_, count, ok := m.s.connectionAgentSummary(m.s.connections[0])
		if count != want || ok != (want > 0) {
			t.Fatalf("summary count = %d/%v, want %d", count, ok, want)
		}
	}
	assertCount(3)
	id, generation, _, _ := sessions[0].RuntimeSnapshot()
	m.s.applyAgentUpdate(agentdetect.Update{ID: id, Generation: generation})
	assertCount(2)
	m.s.manager.Kill(sessionIndex(m.s.manager, sessions[1]))
	assertCount(1)
	if err := sessions[2].Close(); err != nil {
		t.Fatalf("close agent session: %v", err)
	}
	assertCount(0)
	if err := m.s.manager.Respawn(sessionIndex(m.s.manager, sessions[2])); err != nil {
		t.Fatalf("respawn agent session: %v", err)
	}
	assertCount(0)
	setTestAgentStatus(t, m.s, sessions[2], agentdetect.StateWorking)
	assertCount(1)
}

func TestNarrowConnectionRailKeepsTotalAgentCount(t *testing.T) {
	m, sessions := newAgentSummaryModel(t)
	m.s.connectionLayout = connectionLayoutLeft
	setTestAgentStatus(t, m.s, sessions[0], agentdetect.StateWorking)
	setTestAgentStatus(t, m.s, sessions[1], agentdetect.StateDone)
	setTestAgentStatus(t, m.s, sessions[2], agentdetect.StateIdle)
	rows := m.renderConnectionRail(m.s.geometry())
	row := ansi.Strip(rows[5])
	if !strings.Contains(row, "working") || !strings.HasSuffix(row, "(3)") ||
		ansi.StringWidth(rows[5]) != m.s.geometry().ConnectionRail.Width {
		t.Fatalf("narrow rail lost total count or active state: %q", row)
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
	m.s.evaluateAgentSession(sess)

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

func TestCrushAgentLabel(t *testing.T) {
	status := agentdetect.Status{
		Provider: agentdetect.ProviderCrush,
		State:    agentdetect.StateUnknown,
	}
	if got := agentLabel(status); got != "Crush" {
		t.Fatalf("agentLabel() = %q, want %q", got, "Crush")
	}
	status.State = agentdetect.StateWorking
	if got := agentLabel(status); got != "working Crush" {
		t.Fatalf("agentLabel() = %q, want %q", got, "working Crush")
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
	if info := m.s.connectionAgentInfo(m.s.connections[0]); info == nil || info.Count != 1 {
		t.Fatalf("connection metadata = %#v, want agent count 1", info)
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
	m.s.evaluateAgentSession(viewed)
	m.s.evaluateAgentSession(background)

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

func TestCrushNativeCompletionBecomesIdleOrDone(t *testing.T) {
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

	setTestProviderStatus(t, m.s, viewed, agentdetect.ProviderCrush, agentdetect.StateWorking, agentdetect.SourceNative)
	setTestProviderStatus(t, m.s, background, agentdetect.ProviderCrush, agentdetect.StateWorking, agentdetect.SourceNative)
	setTestProviderStatus(t, m.s, viewed, agentdetect.ProviderCrush, agentdetect.StateIdle, agentdetect.SourceNative)
	setTestProviderStatus(t, m.s, background, agentdetect.ProviderCrush, agentdetect.StateIdle, agentdetect.SourceNative)

	if status, _ := m.s.agentStatus(viewed); status.State != agentdetect.StateIdle {
		t.Fatalf("viewed completion = %q, want idle", status.State)
	}
	if status, _ := m.s.agentStatus(background); status.State != agentdetect.StateDone {
		t.Fatalf("background completion = %q, want done", status.State)
	}
}

func TestCrushScreenFallbackRecognizesReadyAndPermission(t *testing.T) {
	m := NewModel([]string{"sh"}, 120, 36)
	manager := session.NewManager(120, 36, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	setTestProviderStatus(t, m.s, sess, agentdetect.ProviderCrush, agentdetect.StateUnknown, agentdetect.SourceProcess)

	sess.Screen().Write([]byte("\r\n> Ready for instructions\r\n"))
	m.s.evaluateAgentSession(sess)
	if status, _ := m.s.agentStatus(sess); status.State != agentdetect.StateIdle ||
		status.Source != agentdetect.SourceScreen {
		t.Fatalf("ready status = %#v, want idle/screen", status)
	}

	sess.Screen().Write([]byte("\r\nPermission Required\r\nTool write\r\nAllow      Allow for Session      Deny\r\n←/→ choose • enter confirm • esc exit\r\n"))
	m.s.evaluateAgentSession(sess)
	if status, _ := m.s.agentStatus(sess); status.State != agentdetect.StateBlocked ||
		status.Source != agentdetect.SourceScreen {
		t.Fatalf("permission status = %#v, want blocked/screen", status)
	}
}

func TestNativeStateIsNotOverwrittenByProcessPresence(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	setTestProviderStatus(t, m.s, sess, agentdetect.ProviderCrush, agentdetect.StateBlocked, agentdetect.SourceNative)

	id, generation, _, _ := sess.RuntimeSnapshot()
	process := agentdetect.Status{
		Provider: agentdetect.ProviderCrush, State: agentdetect.StateUnknown,
		Source: agentdetect.SourceProcess, Confidence: agentdetect.ConfidenceHigh,
	}
	if m.s.applyAgentUpdate(agentdetect.Update{ID: id, Generation: generation, Status: &process}) {
		t.Fatal("lower-authority process update replaced native state")
	}
	if status, _ := m.s.agentStatus(sess); status.State != agentdetect.StateBlocked {
		t.Fatalf("status = %q, want blocked", status.State)
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
	setTestProviderStatus(t, s, sess, agentdetect.ProviderCopilot, agentState, agentdetect.SourceScreen)
}

func setTestProviderStatus(t *testing.T, s *state, sess *session.Session, provider agentdetect.Provider, agentState agentdetect.State, source agentdetect.Source) {
	t.Helper()
	id, generation, _, _ := sess.RuntimeSnapshot()
	status := agentdetect.Status{
		Provider: provider,
		State:    agentState, Source: source,
		Confidence: agentdetect.ConfidenceHigh, UpdatedAt: time.Now(),
	}
	if !s.applyAgentUpdate(agentdetect.Update{ID: id, Generation: generation, Status: &status}) {
		t.Fatalf("apply %s status returned unchanged", agentState)
	}
}
