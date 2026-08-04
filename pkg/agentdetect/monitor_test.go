package agentdetect

import (
	"errors"
	"testing"
	"time"
)

type fakeInventory struct {
	processes []Process
	err       error
}

func (f *fakeInventory) Snapshot() ([]Process, error) {
	return append([]Process(nil), f.processes...), f.err
}

func TestMonitorPublishesCopilotPresenceAndRemoval(t *testing.T) {
	inventory := &fakeInventory{processes: []Process{
		{PID: 100, ParentPID: 1, Executable: "bash"},
		{PID: 101, ParentPID: 100, Executable: "copilot"},
	}}
	var updates []Update
	monitor := NewMonitor(inventory, time.Second, func(update Update) {
		updates = append(updates, update)
	})
	monitor.Sync([]Target{{ID: "session", Generation: 3, ProcessID: 100, Local: true}})

	monitor.poll()
	if len(updates) != 1 || updates[0].Status == nil {
		t.Fatalf("presence updates = %#v, want one present update", updates)
	}
	if got := updates[0].Status.Provider; got != ProviderCopilot {
		t.Fatalf("provider = %q, want %q", got, ProviderCopilot)
	}

	inventory.processes = inventory.processes[:1]
	monitor.poll()
	if len(updates) != 2 || updates[1].Status != nil {
		t.Fatalf("removal updates = %#v, want absent update", updates)
	}
}

func TestMonitorInventoryErrorDoesNotPublishFalseAbsence(t *testing.T) {
	inventory := &fakeInventory{processes: []Process{
		{PID: 100, ParentPID: 1, Executable: "copilot"},
	}}
	var updates []Update
	monitor := NewMonitor(inventory, time.Second, func(update Update) {
		updates = append(updates, update)
	})
	monitor.Sync([]Target{{ID: "session", Generation: 1, ProcessID: 100, Local: true}})
	monitor.poll()

	inventory.err = errors.New("denied")
	monitor.poll()
	if len(updates) != 1 {
		t.Fatalf("updates after inventory failure = %d, want 1", len(updates))
	}
}

func TestMonitorSyncClearsPreviousGenerationWithoutSynchronousPublish(t *testing.T) {
	inventory := &fakeInventory{processes: []Process{{PID: 100, ParentPID: 1, Executable: "copilot", StartTime: 10}}}
	var updates []Update
	monitor := NewMonitor(inventory, time.Second, func(update Update) {
		updates = append(updates, update)
	})
	monitor.Sync([]Target{{ID: "session", Generation: 1, ProcessID: 100, Local: true}})
	monitor.poll()
	monitor.Sync([]Target{{ID: "session", Generation: 2, ProcessID: 100, Local: true}})

	if len(updates) != 1 {
		t.Fatalf("Sync published from caller goroutine: updates = %#v", updates)
	}
	monitor.poll()
	if len(updates) != 2 || updates[1].Status == nil || updates[1].Generation != 2 {
		t.Fatalf("generation updates = %#v, want new generation presence", updates)
	}
}

func TestMonitorRejectsReusedRootPID(t *testing.T) {
	inventory := &fakeInventory{processes: []Process{
		{PID: 100, ParentPID: 1, Executable: "bash", StartTime: 10},
		{PID: 101, ParentPID: 100, Executable: "copilot", StartTime: 11},
	}}
	var updates []Update
	monitor := NewMonitor(inventory, time.Second, func(update Update) {
		updates = append(updates, update)
	})
	monitor.Sync([]Target{{ID: "session", Generation: 1, ProcessID: 100, Local: true}})
	monitor.poll()

	inventory.processes = []Process{
		{PID: 100, ParentPID: 1, Executable: "bash", StartTime: 20},
		{PID: 102, ParentPID: 100, Executable: "copilot", StartTime: 21},
	}
	monitor.poll()
	monitor.poll()

	if len(updates) != 2 || updates[1].Status != nil {
		t.Fatalf("PID reuse updates = %#v, want one removal and no re-detection", updates)
	}
}
