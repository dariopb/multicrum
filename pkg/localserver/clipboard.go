package localserver

import (
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strings"
)

func writeAttachedClipboard(stdout io.Writer, text []byte) {
	if setTmuxClipboard(text) {
		return
	}
	osc := "\x1b]52;c;" + base64.StdEncoding.EncodeToString(text) + "\x07"
	_, _ = io.WriteString(stdout, osc+"\x1bPtmux;"+strings.ReplaceAll(osc, "\x1b", "\x1b\x1b")+"\x1b\\")
}

func setTmuxClipboard(text []byte) bool {
	if os.Getenv("TMUX") == "" {
		return false
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		return false
	}
	return exec.Command(path, "set-buffer", "-w", "--", string(text)).Run() == nil
}
