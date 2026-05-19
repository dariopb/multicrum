package ui

import (
	"fmt"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	dbgOnce sync.Once
	dbgF    *os.File
)

func debugLog(format string, args ...any) {
	if os.Getenv("MULTIAGENT_DEBUG") == "" {
		return
	}
	dbgOnce.Do(func() {
		dbgF, _ = os.OpenFile("/tmp/multiagent-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	})
	if dbgF == nil {
		return
	}
	fmt.Fprintf(dbgF, "%s ", time.Now().Format("15:04:05.000"))
	fmt.Fprintf(dbgF, format, args...)
	fmt.Fprintln(dbgF)
	_ = dbgF.Sync()
}

func debugMouse(ev tea.MouseEvent) {
	if os.Getenv("MULTIAGENT_DEBUG") == "" {
		return
	}
	dbgOnce.Do(func() {
		dbgF, _ = os.OpenFile("/tmp/multiagent-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	})
	if dbgF == nil {
		return
	}
	fmt.Fprintf(dbgF, "%s action=%d button=%d x=%d y=%d shift=%v alt=%v ctrl=%v\n",
		time.Now().Format("15:04:05.000"), ev.Action, ev.Button, ev.X, ev.Y, ev.Shift, ev.Alt, ev.Ctrl)
	_ = dbgF.Sync()
}
