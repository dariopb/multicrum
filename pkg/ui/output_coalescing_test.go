package ui

import (
	"testing"

	"multicrum/pkg/session"
)

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
