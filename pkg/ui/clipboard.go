package ui

import (
	"os"
	"os/exec"
)

// copyToClipboard places text into the system clipboard using the best
// available method, in order:
//
//  1. A native clipboard helper if one is installed (wl-copy, xclip, xsel,
//     pbcopy on macOS, clip.exe on Windows / WSL).
//  2. OSC 52 written directly to /dev/tty so it bypasses Bubble Tea's
//     alt-screen renderer and reaches the host terminal emulator. The
//     sequence is wrapped in tmux's DCS passthrough when $TMUX is set.
//
// Both paths are attempted: native helper for the local clipboard, OSC 52
// for remote SSH/tmux sessions. Either succeeding is fine.
func copyToClipboard(text string) {
	tryHelper(text)
	tryOSC52(text)
}

func tryHelper(text string) bool {
	candidates := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"},
		{"clip.exe"},
	}
	// Inside tmux, also push to the tmux paste buffer so the user can paste
	// with `prefix + ]`. This is the only reliable transport when neither a
	// local clipboard helper nor host-terminal OSC 52 support is available
	// (e.g., SSH from Windows Terminal into Linux inside tmux).
	if os.Getenv("TMUX") != "" {
		candidates = append(candidates, []string{"tmux", "load-buffer", "-"})
	}
	for _, c := range candidates {
		path, err := exec.LookPath(c[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, c[1:]...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			continue
		}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			continue
		}
		_, _ = stdin.Write([]byte(text))
		_ = stdin.Close()
		_ = cmd.Wait()
		return true
	}
	return false
}

func tryOSC52(text string) bool {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		// Fall back to stderr; some environments don't expose /dev/tty.
		_, werr := os.Stderr.WriteString(buildOSC52(text))
		return werr == nil
	}
	defer tty.Close()
	_, err = tty.WriteString(buildOSC52(text))
	return err == nil
}
