package agentdetect

import "errors"

var ErrProcessInventoryUnavailable = errors.New("process inventory unavailable")

type Process struct {
	PID         int
	ParentPID   int
	Executable  string
	CommandLine string
	StartTime   uint64
}

type ProcessInventory interface {
	Snapshot() ([]Process, error)
}
