//go:build !windows

package console

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type UnixConsole struct {
	pty     *os.File
	command *exec.Cmd
	done    chan struct{}
}

func NewUnixConsole(args []string, cols, rows int) (*UnixConsole, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	uc := &UnixConsole{
		pty:     ptmx,
		command: cmd,
		done:    make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(uc.done)
	}()
	return uc, nil
}

func (uc *UnixConsole) Read(p []byte) (int, error)  { return uc.pty.Read(p) }
func (uc *UnixConsole) Write(p []byte) (int, error) { return uc.pty.Write(p) }

func (uc *UnixConsole) Close() error {
	if uc.command != nil && uc.command.Process != nil {
		_ = uc.command.Process.Kill()
	}
	if uc.pty != nil {
		return uc.pty.Close()
	}
	return nil
}

func (uc *UnixConsole) Resize(cols, rows int) error {
	return pty.Setsize(uc.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (uc *UnixConsole) Done() <-chan struct{} { return uc.done }

var _ io.ReadWriteCloser = (*UnixConsole)(nil)
