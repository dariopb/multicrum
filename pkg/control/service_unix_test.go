//go:build !windows

package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testBackend struct{}

func (testBackend) HandleControl(_ context.Context, method string, params json.RawMessage, _ string) (any, *Error) {
	switch method {
	case "server.get":
		return map[string]any{"name": "test"}, nil
	case "session.sendBytes":
		var request struct {
			Data []byte `json:"data"`
		}
		_ = json.Unmarshal(params, &request)
		return map[string]any{"accepted": len(request.Data)}, nil
	default:
		return map[string]any{}, nil
	}
}

func TestServiceHandshakeRequestInputAndOutput(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "test.control.sock")
	service := NewService("test", endpoint, "secret", testBackend{})
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	client, err := Dial(Options{Server: "test", Endpoint: endpoint, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Welcome().Protocol != Protocol || client.Welcome().ServerName != "test" {
		t.Fatalf("unexpected welcome: %#v", client.Welcome())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var server map[string]any
	if err := client.Call(ctx, "server.get", map[string]any{}, &server); err != nil {
		t.Fatal(err)
	}
	if server["name"] != "test" {
		t.Fatalf("server.get = %#v", server)
	}
	if accepted, err := client.SendBytes(ctx, "ses_test", 1, []byte("abc")); err != nil || accepted != 3 {
		t.Fatalf("SendBytes = %d, %v", accepted, err)
	}

	var subscribed struct {
		SubscriptionID string `json:"subscriptionId"`
	}
	if err := client.Call(ctx, "subscribe", map[string]any{
		"streams": []string{"session.output"}, "sessionIds": []string{"ses_test"},
	}, &subscribed); err != nil {
		t.Fatal(err)
	}
	service.PublishOutput("ses_test", 1, 7, []byte("output"))
	select {
	case output := <-client.Outputs():
		if output.Header.SubscriptionID != subscribed.SubscriptionID ||
			output.Header.Sequence != 7 || string(output.Data) != "output" {
			t.Fatalf("unexpected output: %#v %q", output.Header, output.Data)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for output")
	}
}

func TestServiceRejectsInvalidToken(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "test.control.sock")
	service := NewService("test", endpoint, "secret", testBackend{})
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := Dial(Options{Server: "test", Endpoint: endpoint, Token: "wrong"}); err == nil {
		t.Fatal("Dial succeeded with an invalid token")
	}
}

func TestWithinRootResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(child, link); err != nil {
		t.Fatal(err)
	}
	if !withinRoot(link, root) {
		t.Fatal("symlink resolving inside root was rejected")
	}
	if withinRoot(t.TempDir(), root) {
		t.Fatal("path outside root was accepted")
	}
}

func TestNormalizeSessionCloseAlias(t *testing.T) {
	for method, wantMode := range map[string]string{
		"session.remove":    "remove",
		"session.terminate": "terminate",
	} {
		request := normalizeSessionCloseAlias(Request{
			ID: method, Method: method,
			Params: mustJSON(map[string]any{"sessionId": "ses_test", "generation": 2}),
		})
		if request.Method != "session.close" {
			t.Errorf("%s normalized method = %q", method, request.Method)
		}
		var params struct {
			SessionID  string `json:"sessionId"`
			Generation uint64 `json:"generation"`
			Mode       string `json:"mode"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Fatal(err)
		}
		if params.SessionID != "ses_test" || params.Generation != 2 || params.Mode != wantMode {
			t.Errorf("%s normalized params = %#v", method, params)
		}
	}
}
