package ui

import (
	"testing"
	"time"

	"multicrum/pkg/agentdetect"
	"multicrum/pkg/session"
)

func TestCoalescedOutputRefreshesBackgroundAgentState(t *testing.T) {
	for _, scenario := range []string{"focused-output", "background-output", "background-connection", "connection-switch"} {
		t.Run(scenario, func(t *testing.T) {
			m, sessions := newAgentSummaryModel(t)
			conn := m.s.connections[0]
			conn.manager.Focus(0)
			agent := sessions[2]
			agent.Screen().Write([]byte("Permission required\r\n────────────────\r\nenter to select  esc to cancel"))
			setTestAgentStatus(t, m.s, agent, agentdetect.StateBlocked)

			focusOtherConnection := func() {
				other := m.s.addConnection("other")
				other.manager = session.NewManager(80, 24, nil, nil)
				t.Cleanup(func() { other.manager.CloseAll() })
				m.s.activeConn = 1
				m.s.syncActiveConnectionFields()
			}
			if scenario == "background-connection" {
				focusOtherConnection()
			}
			index := 0
			if scenario == "background-output" {
				index = 1
			}
			conn.outputPending.Store(true)
			msg := connectionOutputMsg{Conn: conn, Msg: session.OutputMsg{Index: index}}
			if scenario == "focused-output" || scenario == "connection-switch" {
				// The foreground notification holds the per-connection flag
				// until the render tick; the agent's final redraw is coalesced.
				if _, cmd := m.Update(msg); cmd == nil {
					t.Fatal("focused output did not schedule a render")
				}
				if scenario == "connection-switch" {
					focusOtherConnection()
				}
			}
			agent.Screen().Write([]byte("\x1b[2J\x1b[H◉ Working · 26.1 KiB esc interrupt   GPT"))
			if scenario == "focused-output" || scenario == "connection-switch" {
				m.Update(renderTickMsg{})
			} else {
				// Two sessions wrote before the background notification was
				// consumed. Only the first session appears in the message.
				m.Update(msg)
			}
			status, ok := m.s.agentStatus(agent)
			if !ok || status.State != agentdetect.StateWorking {
				t.Fatalf("coalesced agent remained %q, want working", status.State)
			}
			info := m.s.connectionAgentInfo(conn)
			if info == nil || info.State != "working" || info.Count != 1 || !info.Animate {
				t.Fatalf("rail/browser metadata stayed stale: %#v", info)
			}
			if conn.outputPending.Load() {
				t.Fatal("output notifications were not re-armed")
			}
		})
	}
}

func TestCoalescedAgentRefreshPreservesNativeStatus(t *testing.T) {
	m, sessions := newAgentSummaryModel(t)
	conn := m.s.connections[0]
	conn.manager.Focus(0)
	agent := sessions[2]
	setTestProviderStatus(t, m.s, agent, agentdetect.ProviderCopilot, agentdetect.StateBlocked, agentdetect.SourceNative)
	agent.Screen().Write([]byte("◉ Working · 26.1 KiB esc interrupt   GPT"))
	conn.outputPending.Store(true)
	m.Update(renderTickMsg{})
	status, ok := m.s.agentStatus(agent)
	if !ok || status.State != agentdetect.StateBlocked || status.Source != agentdetect.SourceNative {
		t.Fatalf("screen refresh overrode authoritative native report: %#v", status)
	}
}

func TestRenderDelayUsesLeadingEdgeThenFrameCap(t *testing.T) {
	now := time.Unix(100, 0)
	if got := renderDelay(time.Time{}, now); got != 0 {
		t.Fatalf("idle render delay = %v, want immediate", got)
	}
	if got := renderDelay(now.Add(-4*time.Millisecond), now); got != 12*time.Millisecond {
		t.Fatalf("busy render delay = %v, want 12ms", got)
	}
	if got := renderDelay(now.Add(-renderInterval), now); got != 0 {
		t.Fatalf("elapsed-frame render delay = %v, want immediate", got)
	}
}

func TestRenderTickRearmsOutputAfterConnectionSwitch(t *testing.T) {
	m := NewModel([]string{"bash"}, 80, 24)
	first := m.s.connections[0]
	first.manager = session.NewManager(80, 23, nil, nil)
	second := m.s.addConnection("work")
	second.manager = session.NewManager(80, 23, nil, nil)
	m.s.syncActiveConnectionFields()

	first.outputPending.Store(true)
	if _, cmd := m.Update(connectionOutputMsg{
		Conn: first,
		Msg:  session.OutputMsg{Index: 0},
	}); cmd == nil {
		t.Fatal("first connection output did not schedule a render tick")
	}

	m.s.activeConn = 1
	m.s.syncActiveConnectionFields()
	second.outputPending.Store(true)
	if _, cmd := m.Update(connectionOutputMsg{
		Conn: second,
		Msg:  session.OutputMsg{Index: 0},
	}); cmd != nil {
		t.Fatal("second connection scheduled a duplicate render tick")
	}

	m.Update(renderTickMsg{})

	if first.outputPending.Load() {
		t.Fatal("first connection remained stuck pending after the shared render tick")
	}
	if second.outputPending.Load() {
		t.Fatal("second connection remained stuck pending after the shared render tick")
	}
}
