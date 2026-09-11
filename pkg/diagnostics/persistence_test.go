package diagnostics

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderPersistsWithoutShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.trace.log")
	r, err := New(path, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Record("periodic-checkpoint-marker")
	deadline := time.Now().Add(checkpointInterval + 3*time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "periodic-checkpoint-marker") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("running recorder did not persist its event before shutdown")
}

func TestRecorderReportsInitialCheckpointFailure(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "missing", "trace.log"), nil); err == nil {
		t.Fatal("creating a recorder with an unwritable checkpoint path succeeded")
	}
}
