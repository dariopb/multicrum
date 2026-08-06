package agentdetect

import (
	"strings"
)

func crushPresent(rootPID int, processes []Process) bool {
	return processTreeContains(rootPID, processes, isCrushProcess)
}

func isCrushProcess(process Process) bool {
	return processHasName(process, "crush", "crush.exe")
}

// DetectCrushScreen recognizes stable controls captured from the Crush TUI.
func DetectCrushScreen(lines []string) (State, bool) {
	screen := strings.ToLower(strings.Join(lines, "\n"))
	if strings.Contains(screen, "permission required") &&
		strings.Contains(screen, "allow for session") &&
		strings.Contains(screen, "deny") &&
		strings.Contains(screen, "enter confirm") {
		return StateBlocked, true
	}
	for _, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "> Ready for instructions") {
			return StateIdle, true
		}
	}
	return StateUnknown, false
}
