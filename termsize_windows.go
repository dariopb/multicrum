//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func termSize() (cols, rows int, err error) {
	var csbi windows.ConsoleScreenBufferInfo
	h := windows.Handle(os.Stdout.Fd())
	if err := windows.GetConsoleScreenBufferInfo(h, &csbi); err != nil {
		return 0, 0, fmt.Errorf("GetConsoleScreenBufferInfo: %w", err)
	}
	_ = unsafe.Sizeof(csbi) // keep import used
	cols = int(csbi.Window.Right-csbi.Window.Left) + 1
	rows = int(csbi.Window.Bottom-csbi.Window.Top) + 1
	return cols, rows, nil
}
