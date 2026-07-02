package session

import (
	"io"
	"testing"
)

type fakeRW struct{}

func (fakeRW) Read(p []byte) (int, error)  { return 0, io.EOF }
func (fakeRW) Write(p []byte) (int, error) { return len(p), nil }
func (fakeRW) Close() error                { return nil }

func newTestSession(index int) *Session {
	return &Session{
		index:  index,
		screen: NewVTScreen(80, 24),
		rw:     fakeRW{},
	}
}

func TestSessionManagerKillReindexesAndKeepsFocusedSession(t *testing.T) {
	m := &SessionManager{sessions: []*Session{newTestSession(0), newTestSession(1), newTestSession(2)}, focused: 2}
	m.updateTerminalRepliesLocked()

	m.Kill(1)

	if got := len(m.sessions); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
	if got := m.focused; got != 1 {
		t.Fatalf("focused = %d, want 1", got)
	}
	if got := m.sessions[0].Index(); got != 0 {
		t.Fatalf("first index = %d, want 0", got)
	}
	if got := m.sessions[1].Index(); got != 1 {
		t.Fatalf("second index = %d, want 1", got)
	}
}

func TestSessionManagerMovePreservesFocusedSession(t *testing.T) {
	m := &SessionManager{sessions: []*Session{newTestSession(0), newTestSession(1), newTestSession(2)}, focused: 0}
	m.updateTerminalRepliesLocked()

	m.Move(0, 2)

	if got := m.focused; got != 2 {
		t.Fatalf("focused = %d, want 2", got)
	}
	if got := m.sessions[2].Index(); got != 2 {
		t.Fatalf("moved session index = %d, want 2", got)
	}
	if got := m.sessions[0].Index(); got != 0 {
		t.Fatalf("first session index = %d, want 0", got)
	}
}

func TestSessionManagerRespawnUsesCachedSize(t *testing.T) {
	m := &SessionManager{cols: 100, rows: 40, sessions: []*Session{newTestSession(0)}}

	if got := m.cols; got != 100 {
		t.Fatalf("initial cols = %d, want 100", got)
	}
	if got := m.rows; got != 40 {
		t.Fatalf("initial rows = %d, want 40", got)
	}

	m.ResizeAll(120, 50)
	if got := m.cols; got != 120 || m.rows != 50 {
		t.Fatalf("after ResizeAll cols/rows = %d/%d, want 120/50", m.cols, m.rows)
	}
}

func TestSessionManagerCloseAllClearsSessions(t *testing.T) {
	m := &SessionManager{sessions: []*Session{newTestSession(0), newTestSession(1)}, focused: 1}

	if err := m.CloseAll(); err != nil {
		t.Fatalf("CloseAll() error = %v", err)
	}
	if got := len(m.sessions); got != 0 {
		t.Fatalf("len = %d, want 0", got)
	}
	if got := m.focused; got != 0 {
		t.Fatalf("focused = %d, want 0", got)
	}
}
