package state

import "testing"

func TestInferenceOperationRejectsSupersededWriters(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := InferenceOperation{ID: "job-1", Action: "load", Previous: InferenceRuntime{Engine: "vllm"}, Phase: "loading"}
	second := InferenceOperation{ID: "job-2", Action: "abort", Previous: InferenceRuntime{Engine: "vllm"}, Phase: "stopping"}
	if err := store.BeginInferenceOperation(first); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginInferenceOperation(second); err != nil {
		t.Fatal(err)
	}
	first.Phase = "error"
	if updated, err := store.UpdateInferenceOperation(first); err != nil || updated {
		t.Fatalf("superseded writer updated=%v err=%v", updated, err)
	}
	if cleared, err := store.ClearInferenceOperation(first.ID); err != nil || cleared {
		t.Fatalf("superseded writer cleared=%v err=%v", cleared, err)
	}
	if got := store.Get().InferenceOperation; got.ID != second.ID || got.Action != "abort" {
		t.Fatalf("newer operation was overwritten: %#v", got)
	}
}

func TestInferenceOperationRetainsRollbackTargetAcrossProgress(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := InferenceRuntime{Engine: "vllm", Model: "stable/model", ExecutionMode: "local"}
	begin := InferenceOperation{ID: "job-1", Action: "switch", TargetEngine: "custom", Previous: previous, Phase: "pending"}
	if err := store.BeginInferenceOperation(begin); err != nil {
		t.Fatal(err)
	}
	nodes := []InferenceNodeProgress{{Node: "spark-2", Phase: "downloading", Percent: 42}}
	if updated, err := store.UpdateInferenceOperation(InferenceOperation{ID: begin.ID, Phase: "loading", Percent: 42, Nodes: nodes}); err != nil || !updated {
		t.Fatalf("update=%v err=%v", updated, err)
	}
	got := store.Get().InferenceOperation
	if got.Previous != previous || got.TargetEngine != "custom" || got.Percent != 42 ||
		len(got.Nodes) != 1 || got.Nodes[0].Node != "spark-2" {
		t.Fatalf("operation contract changed during progress: %#v", got)
	}
}
