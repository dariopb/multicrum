package ui

import (
	"bytes"
	"io"
	"sync"
)

type InputMux struct {
	base io.Reader
	ch   chan []byte

	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
	once   sync.Once
}

func NewInputMux(base io.Reader) *InputMux {
	m := &InputMux{base: base, ch: make(chan []byte, 128)}
	if base != nil {
		go m.readBase()
	}
	return m
}

func (m *InputMux) readBase() {
	buf := make([]byte, 4096)
	for {
		n, err := m.base.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			m.Inject(chunk)
		}
		if err != nil {
			m.Close()
			return
		}
	}
}

func (m *InputMux) Inject(p []byte) {
	if len(p) == 0 {
		return
	}
	chunk := make([]byte, len(p))
	copy(chunk, p)
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return
	}
	select {
	case m.ch <- chunk:
	default:
		go func() { m.ch <- chunk }()
	}
}

func (m *InputMux) Read(p []byte) (int, error) {
	m.mu.Lock()
	if m.buf.Len() > 0 {
		n, _ := m.buf.Read(p)
		m.mu.Unlock()
		return n, nil
	}
	m.mu.Unlock()
	chunk, ok := <-m.ch
	if !ok {
		return 0, io.EOF
	}
	if len(chunk) <= len(p) {
		copy(p, chunk)
		return len(chunk), nil
	}
	copy(p, chunk[:len(p)])
	m.mu.Lock()
	_, _ = m.buf.Write(chunk[len(p):])
	m.mu.Unlock()
	return len(p), nil
}

func (m *InputMux) Write(p []byte) (int, error) {
	if w, ok := m.base.(io.Writer); ok {
		return w.Write(p)
	}
	return len(p), nil
}

func (m *InputMux) Close() error {
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		close(m.ch)
	})
	if c, ok := m.base.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (m *InputMux) Fd() uintptr {
	if f, ok := m.base.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return 0
}
