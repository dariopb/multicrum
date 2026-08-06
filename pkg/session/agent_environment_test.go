//go:build !windows

package session

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"multicrum/pkg/agentdetect"
)

func TestSessionPassesNativeAgentEnvironment(t *testing.T) {
	if os.Getenv("MULTICRUM_AGENT_ENV_HELPER") == "1" {
		runAgentEnvironmentHelper()
		return
	}

	updates := make(chan agentdetect.Update, 1)
	server, err := agentdetect.ListenNativeUpdates(func(update agentdetect.Update) {
		updates <- update
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer server.Close()

	manager := NewManager(80, 24, nil, nil)
	defer manager.CloseAll()
	manager.SetAgentStateEndpoint(server.Endpoint())
	sess, err := manager.New([]string{
		"env", "MULTICRUM_AGENT_ENV_HELPER=1",
		os.Args[0], "-test.run=TestSessionPassesNativeAgentEnvironment",
	})
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}

	select {
	case update := <-updates:
		id, generation, _, _ := sess.RuntimeSnapshot()
		if update.ID != id || update.Generation != generation ||
			update.Status == nil || update.Status.Provider != agentdetect.ProviderCrush ||
			update.Status.State != agentdetect.StateWorking {
			t.Fatalf("update = %#v, session = %q/%d", update, id, generation)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for inherited native agent report")
	}
}

func runAgentEnvironmentHelper() {
	endpoint := os.Getenv("HERDR_SOCKET_PATH")
	target := os.Getenv("HERDR_PANE_ID")
	if os.Getenv("HERDR_ENV") != "1" || endpoint == "" || target == "" {
		os.Exit(2)
	}
	conn, err := net.Dial("unix", endpoint)
	if err != nil {
		os.Exit(3)
	}
	defer conn.Close()
	request := map[string]any{
		"id": "helper", "method": "pane.report_agent",
		"params": map[string]any{
			"pane_id": target, "source": "crush", "agent": "crush",
			"state": "working", "seq": uint64(1),
		},
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		os.Exit(4)
	}
	var response map[string]any
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&response); err != nil {
		os.Exit(5)
	}
}
