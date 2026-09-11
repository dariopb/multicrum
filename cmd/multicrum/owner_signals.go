package main

import (
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/diagnostics"
)

// Preserve Bubble Tea's SIGINT/SIGTERM behavior, but record the signal before
// delivering it. A bare "program was killed" cannot diagnose daemon death.
func runOwnerProgram(p *tea.Program, server string, trace *diagnostics.Recorder) error {
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
			trace.Record("owner.signal signal=%s", sig)
			log.Printf("owner signal: server=%q pid=%d signal=%s", server, os.Getpid(), sig)
			if sig == syscall.SIGINT {
				p.Send(tea.Interrupt())
			} else {
				p.Send(tea.Quit())
			}
		}
	}()
	log.Printf("owner started: server=%q pid=%d", server, os.Getpid())
	trace.Record("owner.run.begin")
	_, err := p.Run()
	trace.Record("owner.run.end ok=%t panic=%t error_type=%T", err == nil, errors.Is(err, tea.ErrProgramPanic), err)
	if err != nil {
		trace.Dump("owner-run-failed")
	}
	log.Printf("owner stopped: server=%q pid=%d error=%v", server, os.Getpid(), err)
	return err
}
