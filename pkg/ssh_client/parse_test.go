package ssh_client

import "testing"

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		user   string
		host   string
		port   string
		wantOK bool
	}{
		{name: "host", input: "example.com", host: "example.com", wantOK: true},
		{name: "user host", input: "alice@example.com", user: "alice", host: "example.com", wantOK: true},
		{name: "host port", input: "example.com:2222", host: "example.com", port: "2222", wantOK: true},
		{name: "user host port", input: "alice@example.com:2222", user: "alice", host: "example.com", port: "2222", wantOK: true},
		{name: "ipv6 host", input: "2001:db8::1", host: "2001:db8::1", wantOK: true},
		{name: "bracket ipv6", input: "[2001:db8::1]", host: "2001:db8::1", wantOK: true},
		{name: "bracket ipv6 port", input: "alice@[2001:db8::1]:2222", user: "alice", host: "2001:db8::1", port: "2222", wantOK: true},
		{name: "empty user", input: "@example.com", wantOK: false},
		{name: "empty host", input: "alice@", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTarget(tt.input)
			if tt.wantOK && err != nil {
				t.Fatalf("ParseTarget() error = %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatalf("ParseTarget() expected error, got nil")
			}
			if !tt.wantOK {
				return
			}
			if got.User != tt.user || got.Host != tt.host || got.Port != tt.port {
				t.Fatalf("ParseTarget() = %+v, want user=%q host=%q port=%q", got, tt.user, tt.host, tt.port)
			}
		})
	}
}

func TestExpandPath(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	got := expandPath("~/.ssh/id_ed25519")
	want := "/home/tester/.ssh/id_ed25519"
	if got != want {
		t.Fatalf("expandPath() = %q, want %q", got, want)
	}
}
