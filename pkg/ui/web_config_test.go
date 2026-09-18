package ui

import (
	"path/filepath"
	"reflect"
	"testing"

	"multicrum/pkg/config"
)

func TestLayoutSavePreservesWebRegistration(t *testing.T) {
	model := NewModel([]string{"sh"}, 80, 24)
	defer model.CloseAgentDetection()
	path := filepath.Join(t.TempDir(), "layout.yaml")
	model.SetConfigPath(path)
	web := &config.WebConfig{
		Address: ":9999", TokenRequired: true,
		ReverseLB: &config.ReverseLBConfig{
			APIEndpoint: "lb.example:9999", FrontendPort: 8123, ServiceName: "test", InstanceName: "test.example", WrapTLS: true, WrapSSH: true,
		},
	}
	model.SetConfigConnections(&config.Config{Web: web, LogLevel: "debug"})
	model.s.saveLayout()
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Web, web) {
		t.Fatalf("normal layout save lost reverse LB settings: %+v", saved.Web)
	}
	if saved.LogLevel != "debug" {
		t.Fatal("normal layout save lost application log level")
	}
}
