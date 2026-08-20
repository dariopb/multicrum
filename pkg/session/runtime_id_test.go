package session

import "testing"

func TestRuntimeIDSurvivesIndexChangesAndGenerationAdvances(t *testing.T) {
	sess, err := newSession(0, []string{"sh"}, 80, 24, nil)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	id, generation, _, local := sess.RuntimeSnapshot()
	if id == "" {
		t.Fatal("runtime ID is empty")
	}
	if !local {
		t.Fatal("local session reported as remote")
	}
	sess.setIndex(4)
	nextID, nextGeneration, _, _ := sess.RuntimeSnapshot()
	if nextID != id {
		t.Fatalf("runtime ID changed after reindex: %q -> %q", id, nextID)
	}
	if nextGeneration != generation {
		t.Fatalf("generation changed after reindex: %d -> %d", generation, nextGeneration)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, closedGeneration, processID, _ := sess.RuntimeSnapshot()
	if closedGeneration != generation {
		t.Fatalf("generation after close = %d, want %d", closedGeneration, generation)
	}
	if processID != 0 {
		t.Fatalf("process ID after close = %d, want 0", processID)
	}
}
