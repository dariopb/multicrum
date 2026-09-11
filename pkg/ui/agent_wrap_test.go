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
			{agentdetect.StateIdle, "← open sidebar · / commands · ? help · tab next tab   GPT"},
			{agentdetect.StateBlocked, "Permission required\r\n────────────────\r\nenter to select  esc to cancel"},
			{agentdetect.StateUnknown, "◉ Wor\r\nking esc interrupt"},
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
