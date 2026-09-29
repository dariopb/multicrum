package agentdetect

import "testing"

func TestCopilotPresentIncludesRootAndDescendants(t *testing.T) {
	processes := []Process{
		{PID: 10, ParentPID: 1, Executable: "bash"},
		{PID: 11, ParentPID: 10, Executable: "node"},
		{PID: 12, ParentPID: 11, Executable: "/usr/local/bin/copilot"},
		{PID: 20, ParentPID: 1, Executable: "copilot"},
	}
	if !copilotPresent(10, processes) {
		t.Fatal("descendant Copilot was not detected")
	}
	if !copilotPresent(20, processes) {
		t.Fatal("root Copilot was not detected")
	}
	if copilotPresent(30, processes) {
		t.Fatal("unrelated Copilot process was detected")
	}
}

func TestCopilotProcessSurvivesExecutableReplacement(t *testing.T) {
	tests := []Process{
		{Executable: "/home/user/.local/bin/copilot (deleted)"},
		{Executable: "/memfd:runtime (deleted)", Command: "copilot"},
		{Executable: "/memfd:runtime (deleted)", CommandLine: "copilot --resume"},
	}
	for _, process := range tests {
		if !isCopilotProcess(process) {
			t.Fatalf("Copilot process was not detected: %#v", process)
		}
	}
}

func TestCopilotProcessDoesNotMatchArgumentText(t *testing.T) {
	process := Process{
		Executable:  "bash",
		Command:     "bash",
		CommandLine: "bash -c echo copilot",
	}
	if isCopilotProcess(process) {
		t.Fatalf("ordinary command was detected as Copilot: %#v", process)
	}
}

func TestDetectCopilotScreen(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		state State
		ok    bool
	}{
		{
			name:  "working",
			lines: []string{"prompt", "◉ Working · 26.1 KiB esc interrupt   GPT"},
			state: StateWorking, ok: true,
		},
		{
			name:  "working before token count",
			lines: []string{"────────────────", "❯", "────────────────", " ◎ Working esc interrupt   Claude Haiku 4.5"},
			state: StateWorking, ok: true,
		},
		{
			name:  "working wrapped",
			lines: []string{"○ Working · 26.1 KiB", "esc interrupt   GPT"},
			state: StateWorking, ok: true,
		},
		{
			name:  "working controls on separate lines",
			lines: []string{"○ Working · 26.1 KiB", "esc", "interrupt   GPT"},
			state: StateWorking, ok: true,
		},
		{
			name:  "task-specific activity",
			lines: []string{" ◉ Validating standalone atectl · 177.5 KiB esc interrupt     "},
			state: StateWorking, ok: true,
		},
		{
			name:  "task-specific activity with wrapped controls",
			lines: []string{"◎ Running targeted tests · 177.5 KiB", "esc", "interrupt"},
			state: StateWorking, ok: true,
		},
		{
			name:  "solid activity marker with arbitrary label",
			lines: []string{"● Reviewing the results esc interrupt"},
			state: StateWorking, ok: true,
		},
		{
			name:  "outline activity marker with arbitrary label",
			lines: []string{"○ Preparing the next step esc interrupt"},
			state: StateWorking, ok: true,
		},
		{
			name:  "task text without activity marker",
			lines: []string{"Validating standalone atectl · 177.5 KiB esc interrupt"},
			state: StateUnknown, ok: false,
		},
		{
			name:  "activity marker without interrupt control",
			lines: []string{"◉ Validating standalone atectl · 177.5 KiB"},
			state: StateUnknown, ok: false,
		},
		{
			name:  "hard line break inside interrupt control",
			lines: []string{"◉ Validating standalone atectl · 177.5 KiB esc inter", "rupt"},
			state: StateUnknown, ok: false,
		},
		{
			name:  "idle controls on separate lines",
			lines: []string{"← open", "sidebar · /", "commands · ?", "help · tab", "next tab GPT"},
			state: StateIdle, ok: true,
		},
		{
			name:  "blocked",
			lines: []string{"Permission required", "────────────────", "enter to select  esc to cancel"},
			state: StateBlocked, ok: true,
		},
		{
			name:  "ordinary shell text",
			lines: []string{"echo 'Working · esc interrupt'", "enter to select esc to cancel"},
			state: StateUnknown, ok: false,
		},
		{
			name:  "unrelated selector with distant divider",
			lines: []string{"────────────────", "other", "other", "enter to select  esc to cancel"},
			state: StateUnknown, ok: false,
		},
		{
			name: "idle wide",
			lines: []string{
				"────────────────",
				"❯",
				"────────────────",
				" ← open sidebar · / commands · ? help · tab next tab   Claude Haiku 4.5",
			},
			state: StateIdle, ok: true,
		},
		{
			name: "idle narrow",
			lines: []string{
				"────────────────",
				" ← open sidebar· / commands · ? help · tab next",
				"                 tab",
				" Claude Haiku 4.5",
			},
			state: StateIdle, ok: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, ok := DetectCopilotScreen(test.lines)
			if state != test.state || ok != test.ok {
				t.Fatalf("DetectCopilotScreen() = %q, %v; want %q, %v", state, ok, test.state, test.ok)
			}
		})
	}
}
