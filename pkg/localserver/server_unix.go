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
	"strings"
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

	mu         sync.Mutex
	clients    map[*client]struct{}
	onCount    func(int)
	onResize   func(cols, rows int)
	onControl  func(action string)
	settings   ServerSettings
	latestCols int
	latestRows int
}

type client struct {
	conn net.Conn
	mu   sync.Mutex
}

func SocketDir() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = filepath.Join(os.TempDir(), "multicrum-"+strconv.Itoa(os.Getuid()))
	}
	dir := filepath.Join(base, "multicrum")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func SocketPath(server string) (string, error) {
	safe := sanitize(server)
	if safe == "" {
		return "", fmt.Errorf("invalid server name %q", server)
	}
	dir, err := SocketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, safe+".sock"), nil
}

func LogPath(server string) (string, error) {
	safe := sanitize(server)
	if safe == "" {
		return "", fmt.Errorf("invalid server name %q", server)
	}
	dir, err := SocketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, safe+".log"), nil
}

func ListServers() ([]string, error) {
	dir, err := SocketDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSocket == 0 || !strings.HasSuffix(name, ".sock") {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".sock"))
	}
	return names, nil
}

func TryAttach(path, server string, stdin *os.File, stdout io.Writer) (bool, error) {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	cols, rows, _ := term.GetSize(int(stdin.Fd()))
	if _, err := clientHandshake(conn, server, "tui-attach", cols, rows); err != nil {
		return true, err
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
				_, _ = stdout.Write(mapBareLF(body))
			}
		}
	}()
	<-done
	return true, nil
}

func mapBareLF(p []byte) []byte {
	var out []byte
	for i, b := range p {
		if b == '\n' && (i == 0 || p[i-1] != '\r') {
			if out == nil {
				out = make([]byte, 0, len(p)+1)
				out = append(out, p[:i]...)
			}
			out = append(out, '\r', '\n')
			continue
		}
		if out != nil {
			out = append(out, b)
		}
	}
	if out == nil {
		return p
	}
	return out
}

func ServerStatus(path, server string) (*ServerHello, error) {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return clientHandshake(conn, server, "status", 0, 0)
}

func StopServer(path, server string) (*ServerHello, error) {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	sh, err := clientHandshake(conn, server, "control", 0, 0)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(ControlRequest{Action: "stop"})
	if err := WriteFrame(conn, FrameControl, body); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	typ, body, err := ReadFrame(conn)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return sh, nil
		}
		if err == io.EOF || err == net.ErrClosed {
			return sh, nil
		}
		return nil, err
	}
	if typ != FrameControlAck {
		return nil, fmt.Errorf("unexpected control ack frame %d", typ)
	}
	var ack ControlAck
	if err := json.Unmarshal(body, &ack); err != nil {
		return nil, err
	}
	if !ack.OK {
		if ack.Error == "" {
			ack.Error = "control request failed"
		}
		return nil, fmt.Errorf("%s", ack.Error)
	}
	return sh, nil
}

func clientHandshake(conn net.Conn, server, kind string, cols, rows int) (*ServerHello, error) {
	hello, _ := json.Marshal(ClientHello{Protocol: Protocol, Version: Version, Server: server, ClientKind: kind, Cols: cols, Rows: rows})
	if err := WriteFrame(conn, FrameClientHello, hello); err != nil {
		return nil, err
	}
	typ, body, err := ReadFrame(conn)
	if err != nil {
		return nil, err
	}
	if typ != FrameServerHello {
		return nil, fmt.Errorf("unexpected hello frame %d", typ)
	}
	var sh ServerHello
	if err := json.Unmarshal(body, &sh); err != nil {
		return nil, err
	}
	if sh.Protocol != Protocol || sh.Version != Version {
		return nil, fmt.Errorf("incompatible server protocol %s/%d", sh.Protocol, sh.Version)
	}
	return &sh, nil
}

func isDetachSequence(p []byte) bool {
	if len(p) == 1 && p[0] == 0x11 {
		return true
	}
	return len(p) == 2 && p[0] == 0x1b && p[1] == 0x11
}

func Listen(path, server string, input InputSink) (*Owner, error) {
	return ListenWithSettings(path, server, input, ServerSettings{})
}

func ListenWithSettings(path, server string, input InputSink, settings ServerSettings) (*Owner, error) {
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	o := &Owner{server: server, path: path, ln: ln, input: input, settings: settings, clients: make(map[*client]struct{})}
	go o.acceptLoop()
	return o, nil
}

func (o *Owner) SetCallbacks(onCount func(int), onResize func(cols, rows int), onControl func(action string)) {
	o.mu.Lock()
	o.onCount = onCount
	o.onResize = onResize
	o.onControl = onControl
	n := len(o.clients)
	cols, rows := o.latestCols, o.latestRows
	o.mu.Unlock()
	if onCount != nil {
		go onCount(n)
	}
	if onResize != nil && cols > 0 && rows > 0 {
		go onResize(cols, rows)
	}
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
	sh, _ := json.Marshal(ServerHello{Protocol: Protocol, Version: Version, Server: o.server, ServerPID: os.Getpid(), Settings: o.settings})
	if WriteFrame(conn, FrameServerHello, sh) != nil {
		return
	}
	c := &client{conn: conn}
	isAttach := hello.ClientKind == "tui-attach"
	if isAttach {
		o.mu.Lock()
		o.clients[c] = struct{}{}
		n := len(o.clients)
		if hello.Cols > 0 && hello.Rows > 0 {
			o.latestCols = hello.Cols
			o.latestRows = hello.Rows
		}
		onResize := o.onResize
		o.mu.Unlock()
		o.emitCount(n)
		if onResize != nil && hello.Cols > 0 && hello.Rows > 0 {
			onResize(hello.Cols, hello.Rows)
		}
		defer func() {
			o.mu.Lock()
			delete(o.clients, c)
			n := len(o.clients)
			o.mu.Unlock()
			o.emitCount(n)
		}()
	}
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
			if json.Unmarshal(body, &resize) == nil {
				o.mu.Lock()
				if resize.Cols > 0 && resize.Rows > 0 {
					o.latestCols = resize.Cols
					o.latestRows = resize.Rows
				}
				onResize := o.onResize
				o.mu.Unlock()
				if onResize != nil {
					onResize(resize.Cols, resize.Rows)
				}
			}
		case FrameControl:
			var req ControlRequest
			if err := json.Unmarshal(body, &req); err != nil {
				o.writeControlAck(c, false, err.Error())
				continue
			}
			if req.Action == "" {
				o.writeControlAck(c, false, "missing control action")
				continue
			}
			o.writeControlAck(c, true, "")
			o.mu.Lock()
			onControl := o.onControl
			o.mu.Unlock()
			if onControl != nil {
				go onControl(req.Action)
			}
		}
	}
}

func (o *Owner) writeControlAck(c *client, ok bool, msg string) {
	ack, _ := json.Marshal(ControlAck{OK: ok, Error: msg})
	c.mu.Lock()
	_ = WriteFrame(c.conn, FrameControlAck, ack)
	c.mu.Unlock()
}

func (o *Owner) emitCount(n int) {
	o.mu.Lock()
	onCount := o.onCount
	o.mu.Unlock()
	if onCount != nil {
		onCount(n)
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
