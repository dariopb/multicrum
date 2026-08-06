//go:build !windows

package agentdetect

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestNativeServerMapsCrushLifecycleReports(t *testing.T) {
	updates := make(chan Update, 3)
	server, err := ListenNativeUpdates(func(update Update) { updates <- update })
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer server.Close()

	target := NativeTarget("session-1", 4)
	sendNativeRequest(t, server.Endpoint(), nativeRequest{
		ID: "idle", Method: "pane.report_agent",
		Params: nativeParams{
			PaneID: target, Source: "crush", Agent: "crush",
			State: "idle", Seq: 10,
		},
	})
	sendNativeRequest(t, server.Endpoint(), nativeRequest{
		ID: "blocked", Method: "pane.report_agent",
		Params: nativeParams{
			PaneID: target, Source: "crush", Agent: "crush",
			State: "blocked", Seq: 11,
		},
	})
	sendNativeRequest(t, server.Endpoint(), nativeRequest{
		ID: "release", Method: "pane.release_agent",
		Params: nativeParams{
			PaneID: target, Source: "crush", Agent: "crush", Seq: 12,
		},
	})

	idle := <-updates
	if idle.ID != "session-1" || idle.Generation != 4 || idle.Status == nil ||
		idle.Status.Provider != ProviderCrush || idle.Status.State != StateIdle ||
		idle.Status.Source != SourceNative {
		t.Fatalf("idle update = %#v", idle)
	}
	blocked := <-updates
	if blocked.Status == nil || blocked.Status.State != StateBlocked {
		t.Fatalf("blocked update = %#v", blocked)
	}
	if release := <-updates; release.Status != nil {
		t.Fatalf("release update = %#v, want removal", release)
	}
}

func TestNativeServerRejectsStaleOrForeignReports(t *testing.T) {
	updates := make(chan Update, 1)
	server, err := ListenNativeUpdates(func(update Update) { updates <- update })
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer server.Close()

	target := NativeTarget("session-1", 2)
	sendNativeRequest(t, server.Endpoint(), nativeRequest{
		ID: "valid", Method: "pane.report_agent",
		Params: nativeParams{
			PaneID: target, Source: "crush", Agent: "crush",
			State: "working", Seq: 20,
		},
	})
	<-updates
	sendNativeRequest(t, server.Endpoint(), nativeRequest{
		ID: "stale", Method: "pane.report_agent",
		Params: nativeParams{
			PaneID: target, Source: "crush", Agent: "crush",
			State: "blocked", Seq: 19,
		},
	})
	sendNativeRequest(t, server.Endpoint(), nativeRequest{
		ID: "foreign", Method: "pane.report_agent",
		Params: nativeParams{
			PaneID: NativeTarget("session-2", 1), Source: "other", Agent: "other",
			State: "working", Seq: 21,
		},
	})
	select {
	case update := <-updates:
		t.Fatalf("unexpected update: %#v", update)
	case <-time.After(50 * time.Millisecond):
	}
}

func sendNativeRequest(t *testing.T, endpoint string, request nativeRequest) {
	t.Helper()
	conn, err := net.Dial("unix", endpoint)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var response map[string]any
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
