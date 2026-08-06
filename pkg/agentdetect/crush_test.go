package agentdetect

import "testing"

func TestCrushPresentIncludesRootAndDescendants(t *testing.T) {
	processes := []Process{
		{PID: 10, ParentPID: 1, Executable: "bash"},
		{PID: 11, ParentPID: 10, Executable: "node"},
		{PID: 12, ParentPID: 11, Executable: "/usr/local/bin/crush"},
		{PID: 20, ParentPID: 1, Executable: "crush.exe"},
		{PID: 30, ParentPID: 1, Executable: "charm"},
	}
	if !crushPresent(10, processes) {
		t.Fatal("descendant Crush was not detected")
	}
	if !crushPresent(20, processes) {
		t.Fatal("root Crush was not detected")
	}
	if crushPresent(30, processes) {
		t.Fatal("unrelated Charm process was detected as Crush")
	}
}

func TestCrushProcessUsesExecutableOrArgvZero(t *testing.T) {
	for _, process := range []Process{
		{Executable: "/usr/local/bin/crush (deleted)"},
		{Executable: "/memfd:runtime (deleted)", Command: "crush"},
		{Executable: "/memfd:runtime (deleted)", CommandLine: "crush --continue"},
	} {
		if !isCrushProcess(process) {
			t.Fatalf("Crush process was not detected: %#v", process)
		}
	}
}

func TestDetectCrushScreen(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		state State
		ok    bool
	}{
		{
			name:  "ready editor",
			lines: []string{"New Session", "> Ready for instructions", "::: "},
			state: StateIdle, ok: true,
		},
		{
			name: "permission dialog",
			lines: []string{
				"Permission Required",
				"Tool write",
				"Allow      Allow for Session      Deny",
				"←/→ choose • enter confirm • esc exit",
			},
			state: StateBlocked, ok: true,
		},
		{
			name:  "version specific working phrase is not trusted",
			lines: []string{"> Brrrrr..."},
			state: StateUnknown, ok: false,
		},
		{
			name:  "ordinary permission text",
			lines: []string{"Permission Required", "Allow for Session", "Deny"},
			state: StateUnknown, ok: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, ok := DetectCrushScreen(test.lines)
			if state != test.state || ok != test.ok {
				t.Fatalf("DetectCrushScreen() = %q, %v; want %q, %v", state, ok, test.state, test.ok)
			}
		})
	}
}
