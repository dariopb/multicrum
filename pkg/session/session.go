package session

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"multicrum/pkg/agentdetect"
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

var nextRuntimeSessionID atomic.Uint64

// Session owns a PTY/ConPTY and the process running inside it.
type Session struct {
	mu            sync.Mutex
	runtimeID     string
	index         int
	cmd           []string
	cmdLine       string
	workDir       string
	title         string
	screen        *VTScreen
	exited        bool
	processID     int
	generation    uint64
	agentEndpoint string

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
		runtimeID: fmt.Sprintf("%d-%d", os.Getpid(), nextRuntimeSessionID.Add(1)),
		index:     index,
		cmd:       cmd,
		screen:    NewVTScreen(cols, rows),
		sshClient: sshClient,
	}
	return s, nil
}

func (s *Session) readLoop(rw io.Reader, screen *VTScreen, generation uint64) {
	buf := make([]byte, 4096)
	for {
		n, err := rw.Read(buf)
		if n > 0 {
			s.mu.Lock()
			if s.generation != generation {
				s.mu.Unlock()
				return
			}
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			screen.Write(chunk)
			sendOutput := s.SendOutput
			index := s.index
			s.mu.Unlock()
			if sendOutput != nil {
				sendOutput(OutputMsg{Index: index, Data: chunk})
			}
		}
		if err != nil {
			s.mu.Lock()
			if s.generation != generation {
				s.mu.Unlock()
				return
			}
			already := s.exited
			s.exited = true
			s.processID = 0
			sendExit := s.SendExit
			index := s.index
			s.mu.Unlock()
			if !already && sendExit != nil {
				sendExit(ExitMsg{Index: index})
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
	s.generation++
	s.processID = 0
	if s.rw != nil {
		return s.rw.Close()
	}
	return nil
}

// Index returns the session's slot in the manager.
func (s *Session) Index() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index
}

// RuntimeSnapshot returns immutable process identity used by owner-level
// monitors. RuntimeID remains stable while indexes may change after moves.
func (s *Session) RuntimeSnapshot() (runtimeID string, generation uint64, processID int, local bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeID, s.generation, s.processID, s.sshClient == nil
}

func (s *Session) agentEnvironment() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentEndpoint == "" || s.sshClient != nil {
		return nil
	}
	target := agentdetect.NativeTarget(s.runtimeID, s.generation+1)
	if target == "" {
		return nil
	}
	return []string{
		"HERDR_ENV=1",
		"HERDR_SOCKET_PATH=" + s.agentEndpoint,
		"HERDR_PANE_ID=" + target,
	}
}

func (s *Session) setIndex(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index = index
}

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

func (s *Session) IsInteractiveShell() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sshClient != nil || len(s.cmd) == 0 {
		return false
	}
	name := strings.ToLower(filepath.Base(s.cmd[0]))
	shellName := strings.ToLower(filepath.Base(os.Getenv("SHELL")))
	comspecName := strings.ToLower(filepath.Base(os.Getenv("COMSPEC")))
	known := false
	switch name {
	case "sh", "bash", "dash", "zsh", "fish", "ksh", "csh", "tcsh", "nu", "nu.exe", "xonsh", "elvish",
		"pwsh", "pwsh.exe", "powershell", "powershell.exe", "cmd", "cmd.exe":
		known = true
	}
	if !known && name != shellName && name != comspecName {
		return false
	}
	for _, arg := range s.cmd[1:] {
		arg = strings.ToLower(arg)
		switch {
		case name == "cmd" || name == "cmd.exe":
			if arg == "/c" {
				return false
			}
		case name == "pwsh" || name == "pwsh.exe" || name == "powershell" || name == "powershell.exe":
			if arg == "-c" || arg == "-command" || strings.HasPrefix(arg, "-command=") ||
				arg == "-encodedcommand" || strings.HasPrefix(arg, "-encodedcommand=") {
				return false
			}
		case arg == "--command", strings.HasPrefix(arg, "--command="),
			strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(strings.TrimLeft(arg, "-"), "c"):
			return false
		}
	}
	return true
}

func (s *Session) ConfiguredWorkingDirectory() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workDir
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
	s.generation++
	s.processID = 0
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
	s.generation++
	generation := s.generation
	screen := s.screen
	s.resizeFn = func(cols, rows int) error {
		return rs.Resize(cols, rows)
	}
	s.mu.Unlock()
	s.screen.SetReplyWriter(rs)
	s.screen.SetTerminalReplies(true)
	go s.readLoop(rs, screen, generation)
	return nil
}
