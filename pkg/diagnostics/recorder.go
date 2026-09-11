// Package diagnostics records bounded, metadata-only owner events for postmortems.
package diagnostics

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const (
	maxEvents          = 256
	maxEventBytes      = 768
	checkpointInterval = 5 * time.Second
)

type event struct {
	sequence uint64
	at       time.Time
	message  string
}

type Recorder struct {
	mu       sync.Mutex
	events   [maxEvents]event
	sequence uint64
	count    int
	path     string
	logger   *log.Logger
	build    string

	fileMu    sync.Mutex
	persisted uint64
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// New preserves the previous run's checkpoint at path+".previous". Call this
// only after acquiring the owner listener so competing owners cannot rotate it.
func New(path string, logger *log.Logger) (*Recorder, error) {
	if logger == nil {
		logger = log.Default()
	}
	if err := os.Rename(path, path+".previous"); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("preserve previous diagnostic trace: %w", err)
	}
	r := &Recorder{
		path: path, logger: logger,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	r.Record("trace.start pid=%d capacity=%d checkpoint_interval=%s", os.Getpid(), maxEvents, checkpointInterval)
	var build strings.Builder
	fmt.Fprintf(&build, "go=%s os=%s arch=%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision", "vcs.time", "vcs.modified":
				fmt.Fprintf(&build, " %s=%s", setting.Key, setting.Value)
			}
		}
		for _, dep := range info.Deps {
			switch dep.Path {
			case "charm.land/bubbletea/v2", "github.com/charmbracelet/ultraviolet", "github.com/charmbracelet/x/vt":
				fmt.Fprintf(&build, " %s=%s", dep.Path, dep.Version)
			}
		}
	}
	r.build = build.String()
	if err := r.checkpoint(); err != nil {
		return nil, err
	}
	go r.checkpointLoop()
	return r, nil
}

// Record must receive metadata only: never terminal bytes, input, command
// arguments, environment variables, tokens, or user-assigned session titles.
// A nil Recorder is disabled.
func (r *Recorder) Record(format string, args ...any) {
	if r == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	message = strings.ReplaceAll(strings.ReplaceAll(message, "\r", `\r`), "\n", `\n`)
	if len(message) > maxEventBytes {
		message = message[:maxEventBytes-3] + "..."
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	r.events[(r.sequence-1)%maxEvents] = event{r.sequence, time.Now(), message}
	r.count = min(r.count+1, maxEvents)
}

func (r *Recorder) snapshot() (uint64, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out strings.Builder
	fmt.Fprintf(&out, "multicrum diagnostic trace pid=%d events=%d dropped=%d last_sequence=%d build=%q\n",
		os.Getpid(), r.count, r.sequence-uint64(r.count), r.sequence, r.build)
	first := r.sequence - uint64(r.count)
	for i := 0; i < r.count; i++ {
		e := r.events[(first+uint64(i))%maxEvents]
		fmt.Fprintf(&out, "%s #%d %s\n", e.at.Format(time.RFC3339Nano), e.sequence, e.message)
	}
	return r.sequence, out.String()
}

// Dump also saves a checkpoint synchronously. SIGKILL cannot execute this;
// periodic checkpoints preserve all but the last few seconds in that case.
func (r *Recorder) Dump(reason string) {
	if r == nil {
		return
	}
	_, data := r.snapshot()
	r.logger.Printf("diagnostic trace dump reason=%q\n%s", reason, data)
	r.saveCheckpoint()
}

// Repanic is used as a defer at background-goroutine boundaries. It records
// context but deliberately rethrows the panic; it does not hide a failed session.
func (r *Recorder) Repanic(scope string) {
	if value := recover(); value != nil {
		r.Record("panic scope=%s type=%T", scope, value)
		r.Dump("panic")
		panic(value)
	}
}

func (r *Recorder) checkpointLoop() {
	defer close(r.done)
	ticker := time.NewTicker(checkpointInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.saveCheckpoint()
		case <-r.stop:
			return
		}
	}
}

func (r *Recorder) saveCheckpoint() {
	if err := r.checkpoint(); err != nil {
		r.logger.Printf("diagnostic checkpoint failed: path=%q error=%v", r.path, err)
	}
}

func (r *Recorder) checkpoint() error {
	r.fileMu.Lock()
	defer r.fileMu.Unlock()
	sequence, data := r.snapshot()
	if sequence == r.persisted {
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(r.path), ".multicrum-trace-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	_, writeErr := file.WriteString(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, r.path); err != nil {
		return err
	}
	r.persisted = sequence
	return nil
}

func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		close(r.stop)
		<-r.done
		r.saveCheckpoint()
	})
}
