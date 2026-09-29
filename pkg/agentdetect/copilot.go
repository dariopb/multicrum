package agentdetect

import (
	"strings"
	"unicode/utf8"
)

func copilotPresent(rootPID int, processes []Process) bool {
	return processTreeContains(rootPID, processes, isCopilotProcess)
}

func isCopilotProcess(process Process) bool {
	return processHasName(process, "copilot", "copilot.exe")
}

// DetectCopilotScreen recognizes only high-confidence footer states.
func DetectCopilotScreen(lines []string) (State, bool) {
	footerLines := make([]string, 0, 6)
	for i := len(lines) - 1; i >= 0 && len(footerLines) < 6; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			footerLines = append([]string{lines[i]}, footerLines...)
		}
	}
	footer := strings.ToLower(strings.Join(strings.Fields(strings.Join(footerLines, " ")), " "))
	for _, line := range footerLines {
		line = strings.ToLower(strings.TrimSpace(line))
		// Copilot replaces the activity label with the current task text.
		if hasCopilotWorkingMarker(line) &&
			strings.Contains(footer, "esc interrupt") {
			return StateWorking, true
		}
	}
	for i, line := range footerLines {
		line = strings.ToLower(line)
		if !strings.Contains(line, "enter to select") || !strings.Contains(line, "esc to cancel") {
			continue
		}
		for adjacent := max(0, i-1); adjacent <= min(len(footerLines)-1, i+1); adjacent++ {
			marker := strings.ToLower(footerLines[adjacent])
			if strings.Contains(marker, "permission") || strings.Contains(marker, "approve") ||
				strings.Contains(marker, "allow") || strings.Contains(marker, "────") {
				return StateBlocked, true
			}
		}
	}
	if strings.Contains(footer, "← open sidebar") &&
		strings.Contains(footer, "/ commands") &&
		strings.Contains(footer, "? help") &&
		strings.Contains(footer, "tab next") {
		return StateIdle, true
	}
	return StateUnknown, false
}

func hasCopilotWorkingMarker(line string) bool {
	if line == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(line)
	return strings.ContainsRune("●◉◎○", first)
}
