package ssh_client

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// RemoteSession is an interactive SSH PTY session that behaves like the local
// console backends: Read returns remote output, Write sends terminal input,
// Resize forwards terminal window changes, and Done closes when the remote
// process exits.
type RemoteSession struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	reader  *io.PipeReader
	writer  *io.PipeWriter
	done    chan struct{}

	mu            sync.Mutex
	closed        bool
	atLineStart   bool
	escapePending bool
}

// Start opens a new SSH connection and interactive PTY session.
func (c *Client) Start(cols, rows int) (*RemoteSession, error) {
	sshClient, err := c.dial()
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", c.config.Addr, err)
	}
	sshSession, err := sshClient.NewSession()
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("ssh new session: %w", err)
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sshSession.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sshSession.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("ssh request pty: %w", err)
	}
	stdin, err := sshSession.StdinPipe()
	if err != nil {
		_ = sshSession.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("ssh stdin pipe: %w", err)
	}
	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		_ = sshSession.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("ssh stdout pipe: %w", err)
	}
	stderr, err := sshSession.StderrPipe()
	if err != nil {
		_ = sshSession.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("ssh stderr pipe: %w", err)
	}
	if len(c.config.Command) == 0 {
		err = sshSession.Shell()
	} else {
		err = sshSession.Start(strings.Join(c.config.Command, " "))
	}
	if err != nil {
		_ = sshSession.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("ssh start remote session: %w", err)
	}

	reader, writer := io.Pipe()
	rs := &RemoteSession{
		client:      sshClient,
		session:     sshSession,
		stdin:       stdin,
		reader:      reader,
		writer:      writer,
		done:        make(chan struct{}),
		atLineStart: true,
	}
	go rs.copyOutput(stdout)
	go rs.copyOutput(stderr)
	go rs.wait()
	return rs, nil
}

func (s *RemoteSession) copyOutput(r io.Reader) {
	_, _ = io.Copy(s.writer, r)
}

func (s *RemoteSession) wait() {
	err := s.session.Wait()
	if err == nil {
		err = io.EOF
	}
	_ = s.writer.CloseWithError(err)
	_ = s.client.Close()
	close(s.done)
}

func (s *RemoteSession) Read(p []byte) (int, error) { return s.reader.Read(p) }

func (s *RemoteSession) Write(p []byte) (int, error) {
	out, disconnect := s.filterEscapes(p)
	if len(out) > 0 {
		if _, err := s.stdin.Write(out); err != nil {
			return 0, err
		}
	}
	if disconnect {
		_ = s.Close()
	}
	return len(p), nil
}

func (s *RemoteSession) filterEscapes(p []byte) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, 0, len(p))
	disconnect := false
	for _, b := range p {
		if s.escapePending {
			s.escapePending = false
			switch b {
			case '.':
				disconnect = true
				continue
			case '~':
				out = append(out, '~')
				s.atLineStart = false
				continue
			default:
				out = append(out, '~')
			}
		}
		if s.atLineStart && b == '~' {
			s.escapePending = true
			continue
		}
		out = append(out, b)
		s.atLineStart = b == '\n' || b == '\r'
	}
	return out, disconnect
}

func (s *RemoteSession) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("ssh session closed")
	}
	return s.session.WindowChange(rows, cols)
}

func (s *RemoteSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.session != nil {
		_ = s.session.Close()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.writer != nil {
		_ = s.writer.Close()
	}
	if s.reader != nil {
		return s.reader.Close()
	}
	return nil
}

func (s *RemoteSession) Done() <-chan struct{} { return s.done }

var _ io.ReadWriteCloser = (*RemoteSession)(nil)
