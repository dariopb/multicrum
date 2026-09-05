package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

// Preserve Bubble Tea's SIGINT/SIGTERM behavior, but record the signal before
// delivering it. A bare "program was killed" cannot diagnose daemon death.
func runOwnerProgram(p *tea.Program, server string) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer signal.Stop(signals)
		select {
		case <-done:
			return
		case sig := <-signals:
			log.Printf("owner signal: server=%q pid=%d signal=%s", server, os.Getpid(), sig)
			if sig == syscall.SIGINT {
				p.Send(tea.Interrupt())
			} else {
				p.Send(tea.Quit())
			}
		}
	}()
	log.Printf("owner started: server=%q pid=%d", server, os.Getpid())
	_, err := p.Run()
	log.Printf("owner stopped: server=%q pid=%d error=%v", server, os.Getpid(), err)
	return err
}
