//go:build linux

package agentdetect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type systemProcessInventory struct{}

func NewProcessInventory() ProcessInventory { return systemProcessInventory{} }

func (systemProcessInventory) Snapshot() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	processes := make([]Process, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		closeParen := strings.LastIndexByte(string(stat), ')')
		if closeParen < 0 || closeParen+2 >= len(stat) {
			continue
		}
		fields := strings.Fields(string(stat[closeParen+2:]))
		if len(fields) < 20 {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		startTime, _ := strconv.ParseUint(fields[19], 10, 64)
		executable := ""
		if path, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe")); err == nil {
			executable = filepath.Base(path)
		}
		if executable == "" {
			executable = strings.Trim(string(stat[strings.IndexByte(string(stat), '(')+1:closeParen]), " ")
		}
		commandLine := ""
		if data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline")); err == nil {
			commandLine = strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " "))
		}
		processes = append(processes, Process{
			PID: pid, ParentPID: ppid, Executable: executable,
			CommandLine: commandLine, StartTime: startTime,
		})
	}
	return processes, nil
}
