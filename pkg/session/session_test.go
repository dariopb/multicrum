package session

import "testing"

func TestSessionTitlePrefersCmdLineAndBasename(t *testing.T) {
	s := &Session{cmd: []string{"/usr/bin/python3", "-m", "http.server"}, cmdLine: "python -m http.server"}
	if got := s.Title(); got != "python -m http.server" {
		t.Fatalf("Title() = %q, want preserved cmdLine", got)
	}

	s2 := &Session{cmd: []string{"/usr/local/bin/ssh"}}
	if got := s2.Title(); got != "ssh" {
		t.Fatalf("Title() = %q, want basename of command", got)
	}
}

func TestSessionIdentifiesInteractiveShells(t *testing.T) {
	for _, tc := range []struct {
		cmd  []string
		want bool
	}{
		{cmd: []string{"bash"}, want: true},
		{cmd: []string{"/bin/zsh", "-l"}, want: true},
		{cmd: []string{"fish", "--login"}, want: true},
		{cmd: []string{"bash", "-c", "echo hi"}, want: false},
		{cmd: []string{"bash", "-lc", "echo hi"}, want: false},
		{cmd: []string{"pwsh", "-Command", "Get-Location"}, want: false},
		{cmd: []string{"python"}, want: false},
	} {
		s := &Session{cmd: tc.cmd}
		if got := s.IsInteractiveShell(); got != tc.want {
			t.Errorf("IsInteractiveShell(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}
