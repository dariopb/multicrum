package ui

import (
	"fmt"
	"testing"

	"multicrum/pkg/agentdetect"
	"multicrum/pkg/session"
)

func TestCopilotDetectionAcrossTerminalWidths(t *testing.T) {
	m := NewModel([]string{"sh"}, 100, 30)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	for _, cols := range []int{8, 12, 19, 27, 40, 80} {
		for _, tt := range []struct {
			state agentdetect.State
			text  string
		}{
			{agentdetect.StateWorking, "◉ Working · 26.1 KiB esc interrupt   GPT"},
			{agentdetect.StateWorking, " ◉ Validating standalone atectl · 177.5 KiB esc interrupt     "},
			{agentdetect.StateIdle, "← open sidebar · / commands · ? help · tab next tab   GPT"},
			{agentdetect.StateBlocked, "Permission required\r\n────────────────\r\nenter to select  esc to cancel"},
			{agentdetect.StateUnknown, "◉ Validating · 177.5 KiB esc inter\r\nrupt"},
		} {
			t.Run(fmt.Sprintf("%d/%s", cols, tt.state), func(t *testing.T) {
				id, generation, _, _ := sess.RuntimeSnapshot()
				m.s.applyAgentUpdate(agentdetect.Update{ID: id, Generation: generation})
				sess.Screen().Resize(cols, 24)
				sess.Screen().Write([]byte("\x1bc" + tt.text))
				setTestProviderStatus(t, m.s, sess, agentdetect.ProviderCopilot, agentdetect.StateUnknown, agentdetect.SourceProcess)
				m.s.evaluateAgentSession(sess)
				status, ok := m.s.agentStatus(sess)
				if !ok || status.State != tt.state {
					t.Fatalf("status = %s, want %s; screen=%#v", status.State, tt.state, sess.Screen().VisibleLines())
				}
			})
		}
	}
}

func TestSingleCopilotAgentResumesWithTaskSpecificFooter(t *testing.T) {
	m := NewModel([]string{"sh"}, 100, 30)
	manager := session.NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	sess, err := manager.New([]string{"sh", "-c", "sleep 60"})
	if err != nil {
		t.Fatal(err)
	}
	m.s.connections[0].manager = manager
	m.s.syncActiveConnectionFields()
	for _, cols := range []int{8, 12, 27, 80, 120} {
		t.Run(fmt.Sprintf("cols=%d", cols), func(t *testing.T) {
			id, generation, _, _ := sess.RuntimeSnapshot()
			m.s.applyAgentUpdate(agentdetect.Update{ID: id, Generation: generation})
			sess.Screen().Resize(cols, 24)
			sess.Screen().Write([]byte("\x1bcPermission required\r\n────────────────\r\nenter to select  esc to cancel"))
			setTestProviderStatus(t, m.s, sess, agentdetect.ProviderCopilot, agentdetect.StateUnknown, agentdetect.SourceProcess)
			if status, _ := m.s.agentStatus(sess); status.State != agentdetect.StateBlocked {
				t.Fatalf("initial state = %q, want blocked", status.State)
			}

			sess.Screen().Write([]byte("\x1b[2J\x1b[H ◉ Validating standalone atectl · 177.5 KiB esc interrupt     "))
			m.Update(OutputMsg{Index: sess.Index()})
			if status, _ := m.s.agentStatus(sess); status.State != agentdetect.StateWorking {
				t.Fatalf("resumed state = %q, want working without resize or focus change", status.State)
			}
			info := m.s.connectionAgentInfo(m.s.connections[0])
			if info == nil || info.Count != 1 || info.State != "working" || !info.Animate {
				t.Fatalf("single-agent rail/browser metadata = %#v, want working", info)
			}
			m.Update(renderTickMsg{})
		})
	}
}
