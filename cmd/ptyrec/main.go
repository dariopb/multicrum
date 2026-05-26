// ptyrec is a tiny PTY recorder + replay tool used to diagnose terminal
// emulator bugs in multicrum. It runs the requested command in a PTY,
// proxies stdin/stdout transparently (you interact with it exactly like
// running the command directly), and writes every byte the child emits
// to a capture file.
//
// Usage:
//
//	ptyrec record --out btop.bin -- btop
//	ptyrec replay --in btop.bin [--cols 220 --rows 48]
//
// "replay" feeds the captured bytes through github.com/charmbracelet/x/vt
// at the given size and prints the resulting screen with ANSI styling.
// Diffing that against what your real terminal showed isolates whether a
// rendering mismatch comes from the vt emulator (handler bug) or from
// multicrum's UI layer.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "record":
		if err := record(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "ptyrec record:", err)
			os.Exit(1)
		}
	case "replay":
		if err := replay(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "ptyrec replay:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(`
usage:
  ptyrec record --out FILE -- CMD [ARGS...]
  ptyrec replay --in FILE [--cols N --rows N]
`))
}

// record runs CMD in a PTY sized to the current terminal, mirrors all bytes
// to/from the real stdin/stdout, and additionally writes every child-emitted
// byte to --out.
func record(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	out := fs.String("out", "", "capture file (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmdArgs := fs.Args()
	if *out == "" || len(cmdArgs) == 0 {
		return fmt.Errorf("--out and a command are required")
	}

	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()

	cols, rows := 220, 48
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		cols, rows = w, h
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return err
	}
	defer ptmx.Close()

	// Forward SIGWINCH to the PTY.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
			}
		}
	}()

	// Put real stdin into raw mode so the child sees keystrokes directly.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// stdin -> ptmx (no need to record what we type; only child output).
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	// ptmx -> stdout + capture file.
	_, _ = io.Copy(io.MultiWriter(os.Stdout, f), ptmx)
	return nil
}

// replay feeds the captured bytes through a virtual terminal and prints the
// rendered screen to stdout with ANSI styling preserved.
func replay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	in := fs.String("in", "", "capture file (required)")
	cols := fs.Int("cols", 0, "columns (default: current terminal width)")
	rows := fs.Int("rows", 0, "rows (default: current terminal height)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("--in is required")
	}
	if *cols == 0 || *rows == 0 {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			if *cols == 0 {
				*cols = w
			}
			if *rows == 0 {
				*rows = h
			}
		} else {
			if *cols == 0 {
				*cols = 220
			}
			if *rows == 0 {
				*rows = 48
			}
		}
	}

	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	e := vt.NewEmulator(*cols, *rows)
	// Drain any emulator replies so it doesn't block.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := e.Read(buf); err != nil {
				return
			}
		}
	}()
	_, _ = e.Write(data)

	fmt.Fprintf(os.Stderr, "replay: %d bytes, alt-screen=%v, scrollback=%d, size=%dx%d\n",
		len(data), e.IsAltScreen(), e.ScrollbackLen(), *cols, *rows)
	fmt.Print(e.Render())
	fmt.Println("\x1b[0m")
	return nil
}
