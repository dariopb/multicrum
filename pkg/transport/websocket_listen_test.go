package transport

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestWebTransportReportsBindFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server, err := NewWSTransport(listener.Addr().String(), "")
	if err == nil {
		server.Close()
		t.Fatal("starting a web endpoint on an occupied port succeeded")
	}
}

func TestWebTransportExposesBoundAddress(t *testing.T) {
	server, err := NewWSTransport("127.0.0.1:0", "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + server.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("web endpoint status = %d", response.StatusCode)
	}
}

func TestWebTransportImmediateCloseReleasesListener(t *testing.T) {
	for range 20 {
		server, err := NewWSTransport("127.0.0.1:0", "")
		if err != nil {
			t.Fatal(err)
		}
		address := server.Addr().String()
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			conn.Close()
			t.Fatal("closed transport still accepted connections")
		}
	}
}
