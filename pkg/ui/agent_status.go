package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"multicrum/pkg/agentdetect"
	"multicrum/pkg/config"
	"multicrum/pkg/session"
	"multicrum/pkg/transport"
)

var (
	agentRectangleSpinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	agentCircleSpinnerFrames    = [...]string{"●", "◉", "◎", "○"}
)

const agentSpinnerInterval = 450 * time.Millisecond

func (s *state) syncAgentSessions() {
	if s.agentMonitor == nil {
		return
	}
	targets := make([]agentdetect.Target, 0)
	valid := make(map[string]uint64)
	for _, conn := range s.connections {
		if conn == nil || conn.manager == nil {
			continue
		}
		for _, sess := range conn.manager.Sessions() {
			id, generation, processID, local := sess.RuntimeSnapshot()
			if id == "" {
				continue
			}
			if local && processID > 0 {
				valid[id] = generation
			}
			targets = append(targets, agentdetect.Target{
				ID: id, Generation: generation, ProcessID: processID, Local: local,
			})
		}
	}
	s.agentMu.Lock()
	for id, detected := range s.agentStatuses {
		if generation, ok := valid[id]; !ok || generation != detected.generation {
			delete(s.agentStatuses, id)
		}
	}
	s.agentMu.Unlock()
	s.agentMonitor.Sync(targets)
}

func (s *state) applyAgentUpdate(update agentdetect.Update) bool {
	sess := s.sessionByRuntimeID(update.ID)
	if sess == nil {
		return false
	}
	_, generation, _, _ := sess.RuntimeSnapshot()
	if generation != update.Generation {
		return false
	}

	s.agentMu.Lock()
	if update.Status == nil {
		_, changed := s.agentStatuses[update.ID]
		delete(s.agentStatuses, update.ID)
		s.agentMu.Unlock()
		return changed
	}
	previous, exists := s.agentStatuses[update.ID]
	next := detectedAgent{status: *update.Status, generation: update.Generation}
	changed := !exists || previous.generation != next.generation ||
		previous.status.Provider != next.status.Provider ||
		previous.status.State != next.status.State ||
		previous.status.Source != next.status.Source ||
		previous.status.Confidence != next.status.Confidence
	s.agentStatuses[update.ID] = next
	s.agentMu.Unlock()

	if update.Status.Provider == agentdetect.ProviderCopilot {
		s.evaluateCopilotSession(sess)
	}
	return changed
}

func (s *state) sessionByRuntimeID(id string) *session.Session {
	for _, conn := range s.connections {
		if conn == nil || conn.manager == nil {
			continue
		}
		for _, sess := range conn.manager.Sessions() {
			runtimeID, _, _, _ := sess.RuntimeSnapshot()
			if runtimeID == id {
				return sess
			}
		}
	}
	return nil
}

func (s *state) agentStatus(sess *session.Session) (agentdetect.Status, bool) {
	if sess == nil {
		return agentdetect.Status{}, false
	}
	id, generation, _, _ := sess.RuntimeSnapshot()
	s.agentMu.RLock()
	detected, ok := s.agentStatuses[id]
	s.agentMu.RUnlock()
	if !ok || detected.generation != generation {
		return agentdetect.Status{}, false
	}
	return detected.status, true
}

func (s *state) evaluateCopilotScreen(conn *connectionState, index int) {
	if conn == nil || conn.manager == nil {
		return
	}
	s.evaluateCopilotSession(conn.manager.ByID(index))
}

func (s *state) evaluateCopilotSession(sess *session.Session) {
	status, ok := s.agentStatus(sess)
	if !ok || status.Provider != agentdetect.ProviderCopilot ||
		(sourcePriority(status.Source) > sourcePriority(agentdetect.SourceScreen) &&
			status.State != agentdetect.StateUnknown) {
		return
	}
	lines := sess.Screen().VisibleLines()
	text := make([]string, len(lines))
	for i, line := range lines {
		text[i] = line.Text
	}
	nextState, matched := agentdetect.DetectCopilotScreen(text)
	if !matched {
		return
	}
	if nextState == agentdetect.StateIdle && !s.isViewedSession(sess) {
		nextState = agentdetect.StateDone
	}
	id, generation, _, _ := sess.RuntimeSnapshot()
	next := detectedAgent{
		generation: generation,
		status: agentdetect.Status{
			Provider: agentdetect.ProviderCopilot,
			State:    nextState, Source: agentdetect.SourceScreen,
			Confidence: agentdetect.ConfidenceHigh, UpdatedAt: time.Now(),
		},
	}
	s.agentMu.Lock()
	previous := s.agentStatuses[id]
	changed := previous.status.State != next.status.State ||
		previous.status.Source != next.status.Source
	s.agentStatuses[id] = next
	s.agentMu.Unlock()
	if changed {
		s.notifyMeta()
	}
}

func sourcePriority(source agentdetect.Source) int {
	switch source {
	case agentdetect.SourceHook:
		return 3
	case agentdetect.SourceScreen:
		return 2
	case agentdetect.SourceProcess:
		return 1
	default:
		return 0
	}
}

func (s *state) isViewedSession(sess *session.Session) bool {
	active := s.activeConnection()
	return active != nil && active.manager != nil &&
		active.manager.Focused() == sess
}

func (s *state) acknowledgeFocusedAgent(sess *session.Session) {
	if sess == nil {
		return
	}
	id, generation, _, _ := sess.RuntimeSnapshot()
	s.agentMu.Lock()
	detected, ok := s.agentStatuses[id]
	if ok && detected.generation == generation &&
		detected.status.State == agentdetect.StateDone {
		detected.status.State = agentdetect.StateIdle
		detected.status.UpdatedAt = time.Now()
		s.agentStatuses[id] = detected
	}
	s.agentMu.Unlock()
}

func (s *state) hasWorkingAgent() bool {
	s.agentMu.RLock()
	defer s.agentMu.RUnlock()
	for _, detected := range s.agentStatuses {
		if detected.status.State == agentdetect.StateWorking {
			return true
		}
	}
	return false
}

func (s *state) startAgentSpinner() tea.Cmd {
	if !s.agentSpinnerEnabled || s.agentSpinnerRunning || !s.hasWorkingAgent() {
		return nil
	}
	s.agentSpinnerRunning = true
	return agentSpinnerTick()
}

func agentSpinnerTick() tea.Cmd {
	return tea.Tick(agentSpinnerInterval, func(time.Time) tea.Msg {
		return agentSpinnerTickMsg{}
	})
}

func (s *state) agentSpinnerFrames() []string {
	if s.agentSpinnerStyle == config.AgentSpinnerStyleCircle {
		return agentCircleSpinnerFrames[:]
	}
	return agentRectangleSpinnerFrames[:]
}

func agentLabel(status agentdetect.Status) string {
	provider := "Copilot"
	if status.Provider != agentdetect.ProviderCopilot {
		provider = string(status.Provider)
	}
	if status.State == agentdetect.StateUnknown {
		return provider
	}
	return string(status.State) + " " + provider
}

func (s *state) agentSpinnerSlot(status agentdetect.Status) string {
	if status.State == agentdetect.StateWorking {
		frames := s.agentSpinnerFrames()
		if !s.agentSpinnerEnabled {
			return frames[0] + " "
		}
		return frames[s.agentSpinnerFrame%len(frames)] + " "
	}
	return "  "
}

func (s *state) renderAgentLabel(base lipgloss.Style, status agentdetect.Status, count int) string {
	state := string(status.State)
	provider := "Copilot"
	if status.Provider != agentdetect.ProviderCopilot {
		provider = string(status.Provider)
	}
	if status.State == agentdetect.StateUnknown {
		state = ""
	}
	slot := base.UnsetPadding().Render(s.agentSpinnerSlot(status))
	stateStyle := agentStateStyle(base.UnsetPadding(), status)
	providerStyle := agentProviderStyle(base.UnsetPadding())
	out := slot
	if state != "" {
		out += stateStyle.Render(state) + base.UnsetPadding().Render(" ")
	}
	out += providerStyle.Render(provider)
	if count > 1 {
		out += base.UnsetPadding().Render(fmt.Sprintf(" (%d)", count))
	}
	return out
}

func (s *state) sessionAgentLabel(sess *session.Session) string {
	status, ok := s.agentStatus(sess)
	if !ok {
		return ""
	}
	return s.agentSpinnerSlot(status) + agentLabel(status)
}

func (s *state) connectionAgentLabel(conn *connectionState) string {
	selected, count, ok := s.connectionAgentSummary(conn)
	if !ok {
		return ""
	}
	label := s.agentSpinnerSlot(selected) + agentLabel(selected)
	if count > 1 {
		label = fmt.Sprintf("%s (%d)", label, count)
	}
	return label
}

func (s *state) renderAgentContainer(base lipgloss.Style, prefix, suffix string, status agentdetect.Status, count int) string {
	contentStyle := base.UnsetPadding()
	left := strings.Repeat(" ", base.GetPaddingLeft())
	right := strings.Repeat(" ", base.GetPaddingRight())
	return contentStyle.Render(left+prefix) +
		s.renderAgentLabel(contentStyle, status, count) +
		contentStyle.Render(suffix+right)
}

func (s *state) renderAgentLine(base lipgloss.Style, status agentdetect.Status, count, width int) string {
	contentStyle := base.UnsetPadding()
	line := s.renderAgentLabel(contentStyle, status, count)
	lineWidth := lipgloss.Width(line)
	if lineWidth > width {
		return ansi.Truncate(line, width, "")
	}
	return line + contentStyle.Render(strings.Repeat(" ", width-lineWidth))
}

func (s *state) connectionAgentStatus(conn *connectionState) (agentdetect.Status, bool) {
	selected, _, ok := s.connectionAgentSummary(conn)
	return selected, ok
}

func (s *state) connectionAgentSummary(conn *connectionState) (agentdetect.Status, int, bool) {
	if conn == nil || conn.manager == nil {
		return agentdetect.Status{}, 0, false
	}
	priority := map[agentdetect.State]int{
		agentdetect.StateBlocked: 5,
		agentdetect.StateDone:    4,
		agentdetect.StateWorking: 3,
		agentdetect.StateIdle:    2,
		agentdetect.StateUnknown: 1,
	}
	var selected agentdetect.Status
	count := 0
	for _, sess := range conn.manager.Sessions() {
		status, ok := s.agentStatus(sess)
		if !ok {
			continue
		}
		if count == 0 || priority[status.State] > priority[selected.State] {
			selected = status
			count = 1
		} else if status.State == selected.State {
			count++
		}
	}
	if count == 0 {
		return agentdetect.Status{}, 0, false
	}
	return selected, count, true
}

func (s *state) sessionAgentInfo(sess *session.Session) *transport.AgentInfo {
	status, ok := s.agentStatus(sess)
	if !ok {
		return nil
	}
	return &transport.AgentInfo{
		Provider: string(status.Provider),
		State:    string(status.State),
		Source:   string(status.Source),
		Animate:  s.agentSpinnerEnabled && status.State == agentdetect.StateWorking,
		Spinner:  s.agentSpinnerStyle,
	}
}

func (s *state) connectionAgentInfo(conn *connectionState) *transport.AgentInfo {
	selected, ok := s.connectionAgentStatus(conn)
	if !ok {
		return nil
	}
	return &transport.AgentInfo{
		Provider: string(selected.Provider),
		State:    string(selected.State),
		Source:   string(selected.Source),
		Animate:  s.agentSpinnerEnabled && selected.State == agentdetect.StateWorking,
		Spinner:  s.agentSpinnerStyle,
	}
}
