package localserver

import (
	"path/filepath"
	"testing"
)

func TestLBStatusPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner")
	owner, err := ListenWithSettings(path, "startup", nil, ServerSettings{ReverseLB: &LBStatus{}})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	before, err := ServerStatus(path, "startup")
	if err != nil {
		t.Fatal(err)
	}
	ready := LBStatus{Ready: true, FrontendPort: 8123, FrontendAddress: "lb.example:8123"}
	owner.SetLBStatus(ready)
	after, err := ServerStatus(path, "startup")
	if err != nil || after.Settings.ReverseLB == nil || *after.Settings.ReverseLB != ready {
		t.Fatalf("LB readiness not published: %+v, %v", after, err)
	}
	if before.Settings.ReverseLB.Ready {
		t.Fatal("publishing readiness mutated an earlier snapshot")
	}
}
