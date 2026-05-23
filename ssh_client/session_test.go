package ssh_client

import (
	"io"
	"testing"
)

func TestFilterEscapesDisconnect(t *testing.T) {
	s := &RemoteSession{atLineStart: true}
	out, disconnect := s.filterEscapes([]byte("~."))
	if !disconnect {
		t.Fatalf("disconnect = false, want true")
	}
	if len(out) != 0 {
		t.Fatalf("out = %q, want empty", string(out))
	}
}

func TestFilterEscapesEscapedTilde(t *testing.T) {
	s := &RemoteSession{atLineStart: true}
	out, disconnect := s.filterEscapes([]byte("~~"))
	if disconnect {
		t.Fatalf("disconnect = true, want false")
	}
	if string(out) != "~" {
		t.Fatalf("out = %q, want %q", string(out), "~")
	}
}

func TestFilterEscapesOnlyAtLineStart(t *testing.T) {
	s := &RemoteSession{atLineStart: true}
	out, disconnect := s.filterEscapes([]byte("echo ~.\r~."))
	if !disconnect {
		t.Fatalf("disconnect = false, want true")
	}
	if string(out) != "echo ~.\r" {
		t.Fatalf("out = %q, want %q", string(out), "echo ~.\r")
	}
}

func TestFilterEscapesPendingFlush(t *testing.T) {
	s := &RemoteSession{atLineStart: true}
	out, disconnect := s.filterEscapes([]byte("~"))
	if disconnect || len(out) != 0 {
		t.Fatalf("first out=%q disconnect=%v, want empty/false", string(out), disconnect)
	}
	out, disconnect = s.filterEscapes([]byte("x"))
	if disconnect || string(out) != "~x" {
		t.Fatalf("second out=%q disconnect=%v, want ~x/false", string(out), disconnect)
	}
}

func TestRemoteSessionImplementsReadWriteCloser(t *testing.T) {
	var _ io.ReadWriteCloser = (*RemoteSession)(nil)
}
