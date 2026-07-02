package session

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"multicrum/pkg/ssh_client"
)

// OutputMsg is sent to the Bubble Tea program whenever the session produces output.
type OutputMsg struct {
	Index int
	Data  []byte
}

// ExitMsg is sent once when the child process inside a session exits.
type ExitMsg struct {
	Index int
}

// Session owns a PTY/ConPTY and the process running inside it.
type Session struct {
	mu      sync.Mutex
	index   int
	cmd     []string
	cmdLine string
	title   string
	screen  *VTScreen
	exited  bool

	// rw is the bidirectional channel to the child process (unix pty master,
	// Windows ConPTY pipe pair, or SSH remote PTY). Set by Start().
	rw        io.ReadWriteCloser
	resizeFn  func(cols, rows int) error
	sshClient *ssh_client.Client

	// SendOutput is injected by SessionManager to route output into the TUI.
	SendOutput func(msg OutputMsg)
	// SendExit is injected by SessionManager and fires once when the child
	// process exits so the UI can prompt the user.
	SendExit func(msg ExitMsg)
}

func newSession(index int, cmd []string, cols, rows int, sshClient *ssh_client.Client) (*Session, error) {
	s := &Session{
		index:     index,
		cmd:       cmd,
		screen:    NewVTScreen(cols, rows),
		sshClient: sshClient,
	}
	return s, nil
}

func (s *Session) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.rw.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.screen.Write(chunk)
			if s.SendOutput != nil {
				s.SendOutput(OutputMsg{Index: s.index, Data: chunk})
			}
		}
		if err != nil {
			s.mu.Lock()
			already := s.exited
			s.exited = true
			s.mu.Unlock()
			if !already && s.SendExit != nil {
				s.SendExit(ExitMsg{Index: s.index})
			}
			return
		}
	}
}

// Write sends bytes into the child process (keyboard input).
func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rw == nil {
		return 0, fmt.Errorf("session not started")
	}
	return s.rw.Write(p)
}

// Resize notifies the child process of a new terminal size.
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.screen.Resize(cols, rows)
	if s.resizeFn != nil {
		return s.resizeFn(cols, rows)
	}
	return nil
}

// Close kills the child process.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exited = true
	if s.rw != nil {
		return s.rw.Close()
	}
	return nil
}

// Index returns the session's slot in the manager.
func (s *Session) Index() int { return s.index }

// Title returns a short label for the tab bar.
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.title != "" {
		return s.title
	}
	if strings.TrimSpace(s.cmdLine) != "" {
		return strings.TrimSpace(s.cmdLine)
	}
	if len(s.cmd) == 0 {
		return "?"
	}
	return filepath.Base(s.cmd[0])
}

// SetTitle overrides the tab label for this session.
func (s *Session) SetTitle(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.title = title
}

// Exited reports whether the child process has terminated.
func (s *Session) Exited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Screen returns the VT screen buffer for this session.
func (s *Session) Screen() *VTScreen { return s.screen }

// Cmd returns the command the session was started with.
func (s *Session) Cmd() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.cmd))
	copy(out, s.cmd)
	return out
}

// CmdLine returns the original user-supplied command line for the session,
// if one was provided via SetCmdLine. Empty when the session was started
// directly with an argv slice (no shell-parsing layer above it).
func (s *Session) CmdLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmdLine
}

func (s *Session) SSHConfig() (ssh_client.ResolvedConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sshClient == nil {
		return ssh_client.ResolvedConfig{}, false
	}
	return s.sshClient.Config(), true
}

// SetCmdLine records the original user-supplied command line so callers
// that build argv via a shell-aware parser can round-trip the original
// string back to disk (e.g. config save) without exposing the
// "bash -c <line>" expansion.
func (s *Session) SetCmdLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmdLine = line
}

// Respawn relaunches the original command inside this session, reusing the
// existing index, title, and screen size. The old VT screen is cleared.
func (s *Session) Respawn(cols, rows int) error {
	s.mu.Lock()
	if s.rw != nil {
		_ = s.rw.Close()
	}
	s.rw = nil
	s.resizeFn = nil
	s.exited = false
	s.screen = NewVTScreen(cols, rows)
	s.mu.Unlock()
	return s.Start(cols, rows)
}

func (s *Session) startSSH(cols, rows int) error {
	rs, err := s.sshClient.Start(cols, rows)
	if err != nil {
		return fmt.Errorf("SSH start: %w", err)
	}
	s.mu.Lock()
	s.rw = rs
	s.resizeFn = func(cols, rows int) error {
		return rs.Resize(cols, rows)
	}
	s.mu.Unlock()
	s.screen.SetReplyWriter(rs)
	go s.readLoop()
	return nil
}
