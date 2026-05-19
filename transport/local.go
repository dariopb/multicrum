package transport

// LocalTransport is a no-op transport used when the TUI renders locally
// via Bubble Tea. PTY output is fed directly into the Bubble Tea event
// loop via tea.Program.Send, so this transport does nothing.
type LocalTransport struct {
	ch chan InputEvent
}

// NewLocalTransport creates a LocalTransport.
func NewLocalTransport() *LocalTransport {
	return &LocalTransport{ch: make(chan InputEvent, 64)}
}

func (t *LocalTransport) Send(_ int, _ []byte) error { return nil }
func (t *LocalTransport) Recv() <-chan InputEvent     { return t.ch }
func (t *LocalTransport) Close() error               { close(t.ch); return nil }
