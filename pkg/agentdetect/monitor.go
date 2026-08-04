package agentdetect

import (
	"sync"
	"time"
)

type Monitor struct {
	mu         sync.Mutex
	inventory  ProcessInventory
	interval   time.Duration
	publish    func(Update)
	targets    map[string]Target
	present    map[string]Target
	rootStarts map[string]uint64
	stop       chan struct{}
	done       chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
}

func NewMonitor(inventory ProcessInventory, interval time.Duration, publish func(Update)) *Monitor {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Monitor{
		inventory:  inventory,
		interval:   interval,
		publish:    publish,
		targets:    make(map[string]Target),
		present:    make(map[string]Target),
		rootStarts: make(map[string]uint64),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

func (m *Monitor) Start() {
	m.startOnce.Do(func() { go m.run() })
}

func (m *Monitor) Close() {
	m.closeOnce.Do(func() {
		close(m.stop)
		<-m.done
	})
}

func (m *Monitor) Sync(targets []Target) {
	next := make(map[string]Target, len(targets))
	for _, target := range targets {
		if target.ID != "" && target.Local && target.ProcessID > 0 {
			next[target.ID] = target
		}
	}
	m.mu.Lock()
	for id, old := range m.targets {
		nextTarget, ok := next[id]
		if !ok || nextTarget.Generation != old.Generation {
			delete(m.present, id)
			delete(m.rootStarts, id)
		}
	}
	m.targets = next
	m.mu.Unlock()
}

func (m *Monitor) run() {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.poll()
		case <-m.stop:
			return
		}
	}
}

func (m *Monitor) poll() {
	processes, err := m.inventory.Snapshot()
	if err != nil {
		return
	}
	m.mu.Lock()
	targets := make([]Target, 0, len(m.targets))
	for _, target := range m.targets {
		targets = append(targets, target)
	}
	m.mu.Unlock()

	now := time.Now()
	byPID := make(map[int]Process, len(processes))
	for _, process := range processes {
		byPID[process.PID] = process
	}
	for _, target := range targets {
		root, rootExists := byPID[target.ProcessID]
		m.mu.Lock()
		expectedStart := m.rootStarts[target.ID]
		if rootExists && expectedStart == 0 {
			expectedStart = root.StartTime
			m.rootStarts[target.ID] = expectedStart
		}
		rootMatches := rootExists && root.StartTime == expectedStart
		m.mu.Unlock()
		found := rootMatches && copilotPresent(target.ProcessID, processes)

		m.mu.Lock()
		current, wasPresent := m.present[target.ID]
		switch {
		case found && (!wasPresent || current.Generation != target.Generation):
			m.present[target.ID] = target
			m.mu.Unlock()
			status := Status{
				Provider: ProviderCopilot, State: StateUnknown,
				Source: SourceProcess, Confidence: ConfidenceHigh, UpdatedAt: now,
			}
			m.publishUpdate(Update{ID: target.ID, Generation: target.Generation, Status: &status})
		case !found && wasPresent && current.Generation == target.Generation:
			delete(m.present, target.ID)
			m.mu.Unlock()
			m.publishUpdate(Update{ID: target.ID, Generation: target.Generation})
		default:
			m.mu.Unlock()
		}
	}
}

func (m *Monitor) publishUpdate(update Update) {
	if m.publish != nil {
		m.publish(update)
	}
}
