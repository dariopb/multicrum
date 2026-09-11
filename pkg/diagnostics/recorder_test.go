package diagnostics

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestRecorderBoundsAndCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.trace.log")
	r, err := New(path, log.New(&bytes.Buffer{}, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxEvents+10; i++ {
		r.Record("event=%d %s", i, strings.Repeat("x", maxEventBytes*2))
	}
	r.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != maxEvents+1 {
		t.Fatalf("checkpoint has %d lines, want %d", lines, maxEvents+1)
	}
	if strings.Contains(string(data), "event=0 ") || !strings.Contains(string(data), "event=265 ") {
		t.Fatal("ring did not preserve the most recent events")
	}
	if len(data) > (maxEventBytes+100)*(maxEvents+1) {
		t.Fatalf("checkpoint exceeds bound: %d bytes", len(data))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("checkpoint permissions = %v", info.Mode())
	}
	next, err := New(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	previous, err := os.ReadFile(path + ".previous")
	if err != nil || !bytes.Equal(previous, data) {
		t.Fatalf("previous run was not preserved: %v", err)
	}
}

func TestRecorderDumpAndRepanic(t *testing.T) {
	var output bytes.Buffer
	path := filepath.Join(t.TempDir(), "owner.trace.log")
	r, err := New(path, log.New(&output, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	value := &struct{}{}
	func() {
		defer func() {
			if got := recover(); got != value {
				t.Fatalf("panic was swallowed or replaced: %v", got)
			}
		}()
		defer r.Repanic("session.readLoop")
		r.Record("replay.begin session=ses_test old=160x60 new=136x44")
		panic(value)
	}()
	for _, want := range []string{"replay.begin", "scope=session.readLoop", `reason="panic"`} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("failure dump missing %q: %s", want, output.String())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "scope=session.readLoop") {
		t.Fatalf("failure checkpoint missing panic context: %v", err)
	}
}

func TestRecorderConcurrentEvents(t *testing.T) {
	r, err := New(filepath.Join(t.TempDir(), "trace.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	initial, _ := r.snapshot()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Record("resize=%d", j)
				_, _ = r.snapshot()
			}
		}()
	}
	wg.Wait()
	sequence, _ := r.snapshot()
	if sequence != initial+800 {
		t.Fatalf("sequence = %d, want %d", sequence, initial+800)
	}
}
