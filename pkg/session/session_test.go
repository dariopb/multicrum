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
