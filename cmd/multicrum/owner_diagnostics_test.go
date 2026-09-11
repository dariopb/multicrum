package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"multicrum/pkg/diagnostics"
)

type diagnosticPanicMsg struct{}
type diagnosticPanicModel struct{ trace *diagnostics.Recorder }

func (m diagnosticPanicModel) Init() tea.Cmd {
	return func() tea.Msg { return diagnosticPanicMsg{} }
}

func (m diagnosticPanicModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(diagnosticPanicMsg); ok {
		m.trace.Record("replay.begin session=ses_failure old=160x60 new=136x44")
		panic("diagnostic failure")
	}
	return m, nil
}

func (m diagnosticPanicModel) View() tea.View { return tea.NewView("") }

func TestOwnerPanicDumpsRecentDiagnostics(t *testing.T) {
	var output bytes.Buffer
	path := filepath.Join(t.TempDir(), "owner.trace.log")
	trace, err := diagnostics.New(path, log.New(&output, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	program := tea.NewProgram(diagnosticPanicModel{trace},
		tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithoutSignalHandler(),
	)
	if err := runOwnerProgram(program, "panic-test", trace); !errors.Is(err, tea.ErrProgramPanic) {
		t.Fatalf("owner result = %v, want panic error", err)
	}
	for _, want := range []string{"replay.begin session=ses_failure", "panic=true", `reason="owner-run-failed"`} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("owner failure dump missing %q:\n%s", want, output.String())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "replay.begin session=ses_failure") {
		t.Fatalf("postmortem file missing replay context: %v", err)
	}
}
