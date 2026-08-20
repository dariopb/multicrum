package ui

import (
	"encoding/json"
	"testing"

	"multicrum/pkg/session"
)

func TestControlSessionFocusActivatesContainingConnection(t *testing.T) {
	m := NewModel([]string{"sh"}, 80, 24)
	first := session.NewManager(64, 23, nil, nil)
	if _, err := first.New([]string{"sh", "-c", "sleep 5"}); err != nil {
		t.Fatal(err)
	}
	defer first.CloseAll()
	m.s.connections[0].manager = first
	m.s.syncActiveConnectionFields()

	secondConn := m.s.addConnection("second")
	second := session.NewManager(64, 23, nil, nil)
	if _, err := second.New([]string{"sh", "-c", "sleep 5"}); err != nil {
		t.Fatal(err)
	}
	target, err := second.New([]string{"sh", "-c", "sleep 5"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.CloseAll()
	secondConn.manager = second

	sessionID, _, _, _ := target.RuntimeSnapshot()
	raw, _ := json.Marshal(map[string]any{"sessionId": sessionID})
	result, controlErr := m.s.handleControlRequest(*m, "session.focus", raw, "ctl_test")
	if controlErr != nil {
		t.Fatal(controlErr)
	}
	if m.s.activeConnection() != secondConn {
		t.Fatal("target connection was not activated")
	}
	if second.FocusedIndex() != target.Index() {
		t.Fatalf("focused index = %d, want %d", second.FocusedIndex(), target.Index())
	}
	payload := result.(map[string]any)
	focused := payload["session"].(map[string]any)
	if focused["sessionId"] != sessionID {
		t.Fatalf("focused session = %#v, want %s", focused, sessionID)
	}
}
