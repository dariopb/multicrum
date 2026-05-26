package transport

// Transport abstracts how PTY data is delivered to a viewer and how
// input events are received back.
//
// Phase 1 uses LocalTransport (no-op — Bubble Tea handles rendering).
// Phase 3 uses WebSocketTransport.
type Transport interface {
	// Send delivers raw PTY output for a session to the viewer.
	Send(sessionID int, data []byte) error
	// Recv returns a channel of input events from the viewer.
	Recv() <-chan InputEvent
	// Close shuts down the transport.
	Close() error
}

// InputEvent carries a keystroke from a remote viewer.
type InputEvent struct {
	SessionID int
	Data      []byte
}
