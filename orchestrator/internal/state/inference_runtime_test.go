package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommitInferenceRuntimePersistsAllFieldsTogether(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := InferenceRuntime{Engine: "vllm", Model: "owner/model", ExecutionMode: "cluster", LocalRecipeID: "local-0123456789abcdef"}
	if err := store.CommitInferenceRuntime(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get().InferenceRuntime(); got != want {
		t.Fatalf("runtime = %#v, want %#v", got, want)
	}
}

func TestCommitInferenceRuntimeRevertsMemoryWhenAtomicSaveFails(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	previous := InferenceRuntime{Engine: "vllm", Model: "stable/model", ExecutionMode: "local"}
	if err := store.CommitInferenceRuntime(previous); err != nil {
		t.Fatal(err)
	}
	// save writes state.json.tmp before rename. Replacing that temporary path
	// with a directory deterministically injects a persistence failure even
	// when tests run as root.
	if err := os.Mkdir(filepath.Join(dir, "state.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	candidate := InferenceRuntime{Engine: "sglang", Model: "candidate/model", ExecutionMode: "cluster"}
	if err := store.CommitInferenceRuntime(candidate); err == nil {
		t.Fatal("state commit unexpectedly succeeded")
	}
	if got := store.Get().InferenceRuntime(); got != previous {
		t.Fatalf("in-memory runtime changed after failed save: %#v", got)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get().InferenceRuntime(); got != previous {
		t.Fatalf("on-disk runtime changed after failed save: %#v", got)
	}
}
