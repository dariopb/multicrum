//go:build windows && testconpty

package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"multiagent/console"
)

func main() {
	fmt.Println("Creating WinConsole for cmd.exe …")
	wc, err := console.NewWinConsole("cmd.exe", 120, 30)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("OK – reading output …")

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := wc.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "read err: %v\n", err)
				}
				return
			}
		}
	}()

	time.Sleep(2 * time.Second)
	fmt.Fprintln(os.Stderr, "\n--- sending dir ---")
	_, _ = wc.Write([]byte("dir\r\n"))
	time.Sleep(2 * time.Second)
	_, _ = wc.Write([]byte("exit\r\n"))
	time.Sleep(time.Second)
	wc.Close()
}
