package agentdetect

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrProcessInventoryUnavailable = errors.New("process inventory unavailable")

type Process struct {
	PID         int
	ParentPID   int
	Executable  string
	Command     string
	CommandLine string
	StartTime   uint64
}

type ProcessInventory interface {
	Snapshot() ([]Process, error)
}

func processTreeContains(rootPID int, processes []Process, match func(Process) bool) bool {
	children := make(map[int][]Process, len(processes))
	byPID := make(map[int]Process, len(processes))
	for _, process := range processes {
		byPID[process.PID] = process
		children[process.ParentPID] = append(children[process.ParentPID], process)
	}
	stack := []int{rootPID}
	seen := make(map[int]bool)
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if process, ok := byPID[pid]; ok && match(process) {
			return true
		}
		for _, child := range children[pid] {
			stack = append(stack, child.PID)
		}
	}
	return false
}

func processHasName(process Process, names ...string) bool {
	candidates := []string{
		processExecutableName(process.Executable),
		processExecutableName(process.Command),
	}
	if candidates[1] == "" {
		fields := strings.Fields(process.CommandLine)
		if len(fields) > 0 {
			candidates[1] = processExecutableName(fields[0])
		}
	}
	for _, candidate := range candidates {
		for _, name := range names {
			if candidate == name {
				return true
			}
		}
	}
	return false
}

func processExecutableName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimSuffix(value, " (deleted)")
	return strings.ToLower(filepath.Base(value))
}
