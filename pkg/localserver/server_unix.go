//go:build !windows

package localserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type InputSink interface{ Inject([]byte) }

type Owner struct {
	server string
	path   string
	ln     net.Listener
	input  InputSink

	mu       sync.Mutex
	clients  map[*client]struct{}
	onCount  func(int)
	onResize func(cols, rows int)
}

type client struct {
	conn net.Conn
	mu   sync.Mutex
}

func SocketPath(server string) (string, error) {
	safe := sanitize(server)
	if safe == "" {
		return "", fmt.Errorf("invalid server name %q", server)
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = filepath.Join(os.TempDir(), "multicrum-"+strconv.Itoa(os.Getuid()))
	}
	dir := filepath.Join(base, "multicrum")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, safe+".sock"), nil
}

func TryAttach(path, server string, stdin *os.File, stdout io.Writer) (bool, error) {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	cols, rows, _ := term.GetSize(int(stdin.Fd()))
	hello, _ := json.Marshal(ClientHello{Protocol: Protocol, Version: Version, Server: server, ClientKind: "tui-attach", Cols: cols, Rows: rows})
	if err := WriteFrame(conn, FrameClientHello, hello); err != nil {
		return true, err
	}
	typ, body, err := ReadFrame(conn)
	if err != nil {
		return true, err
	}
	if typ != FrameServerHello {
		return true, fmt.Errorf("unexpected hello frame %d", typ)
	}
	var sh ServerHello
	if err := json.Unmarshal(body, &sh); err != nil {
		return true, err
	}
	if sh.Protocol != Protocol || sh.Version != Version {
		return true, fmt.Errorf("incompatible server protocol %s/%d", sh.Protocol, sh.Version)
	}
	old, err := term.MakeRaw(int(stdin.Fd()))
	if err == nil {
		defer term.Restore(int(stdin.Fd()), old)
	}
	done := make(chan struct{}, 2)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	defer signal.Stop(sig)
	go func() {
		for range sig {
			cols, rows, _ := term.GetSize(int(stdin.Fd()))
			body, _ := json.Marshal(Resize{Cols: cols, Rows: rows})
			_ = WriteFrame(conn, FrameResize, body)
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				if isDetachSequence(chunk) {
					done <- struct{}{}
					return
				}
				_ = WriteFrame(conn, FrameInput, chunk)
			}
			if err != nil {
				done <- struct{}{}
				return
			}
		}
	}()
	go func() {
		for {
			typ, body, err := ReadFrame(conn)
			if err != nil {
				done <- struct{}{}
				return
			}
			if typ == FrameOutput {
				_, _ = stdout.Write(body)
			}
		}
	}()
	<-done
	return true, nil
}

func isDetachSequence(p []byte) bool {
	if len(p) == 1 && p[0] == 0x11 {
		return true
	}
	return len(p) == 2 && p[0] == 0x1b && p[1] == 0x11
}

func Listen(path, server string, input InputSink, onCount func(int), onResize func(cols, rows int)) (*Owner, error) {
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	o := &Owner{server: server, path: path, ln: ln, input: input, clients: make(map[*client]struct{}), onCount: onCount, onResize: onResize}
	go o.acceptLoop()
	return o, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket path %s", path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("refusing to remove socket not owned by current user: %s", path)
	}
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("server socket already active: %s", path)
	}
	if opErr, ok := err.(*net.OpError); ok {
		if sysErr, ok := opErr.Err.(*os.SyscallError); ok {
			if sysErr.Err != unix.ECONNREFUSED && sysErr.Err != unix.ENOENT {
				return fmt.Errorf("socket %s exists but is not stale: %w", path, err)
			}
		}
	}
	return os.Remove(path)
}

func (o *Owner) acceptLoop() {
	for {
		conn, err := o.ln.Accept()
		if err != nil {
			return
		}
		go o.handle(conn)
	}
}

func (o *Owner) handle(conn net.Conn) {
	defer conn.Close()
	typ, body, err := ReadFrame(conn)
	if err != nil || typ != FrameClientHello {
		return
	}
	var hello ClientHello
	if json.Unmarshal(body, &hello) != nil || hello.Protocol != Protocol || hello.Version != Version {
		return
	}
	sh, _ := json.Marshal(ServerHello{Protocol: Protocol, Version: Version, Server: o.server, ServerPID: os.Getpid()})
	if WriteFrame(conn, FrameServerHello, sh) != nil {
		return
	}
	c := &client{conn: conn}
	o.mu.Lock()
	o.clients[c] = struct{}{}
	n := len(o.clients)
	o.mu.Unlock()
	o.emitCount(n)
	defer func() {
		o.mu.Lock()
		delete(o.clients, c)
		n := len(o.clients)
		o.mu.Unlock()
		o.emitCount(n)
	}()
	for {
		typ, body, err := ReadFrame(conn)
		if err != nil {
			return
		}
		switch typ {
		case FrameInput:
			if o.input != nil {
				o.input.Inject(body)
			}
		case FrameResize:
			var resize Resize
			if json.Unmarshal(body, &resize) == nil && o.onResize != nil {
				o.onResize(resize.Cols, resize.Rows)
			}
		}
	}
}

func (o *Owner) emitCount(n int) {
	if o.onCount != nil {
		o.onCount(n)
	}
}

func (o *Owner) Write(p []byte) (int, error) {
	o.mu.Lock()
	clients := make([]*client, 0, len(o.clients))
	for c := range o.clients {
		clients = append(clients, c)
	}
	o.mu.Unlock()
	for _, c := range clients {
		c.mu.Lock()
		err := WriteFrame(c.conn, FrameOutput, p)
		c.mu.Unlock()
		if err != nil {
			_ = c.conn.Close()
		}
	}
	return len(p), nil
}

func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	_ = o.ln.Close()
	_ = os.Remove(o.path)
	return nil
}
