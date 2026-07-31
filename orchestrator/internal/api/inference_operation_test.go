package api

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/state"
)

type inferenceRollbackEngine struct {
	engine.Engine
	runs    []engine.RunSpec
	removed []string
}

func (e *inferenceRollbackEngine) Run(_ context.Context, spec engine.RunSpec) (string, error) {
	e.runs = append(e.runs, spec)
	return "restored", nil
}

func (e *inferenceRollbackEngine) Remove(_ context.Context, name string) error {
	e.removed = append(e.removed, name)
	return nil
}

func TestInferenceJobObserverPersistsFailureAndRollbackTarget(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	previous := state.InferenceRuntime{Engine: "vllm", Model: "stable/model", ExecutionMode: "local"}
	job := jobs.NewManager().Create("engine:custom")
	server.observeInferenceJob(job, "switch", "custom", previous)
	job.ProgressOperation("loading", "Loading reviewed runtime", "coordinator", 40, 1, 3)
	job.Fail(errors.New("stable API contract failed"))
	got := store.Get().InferenceOperation
	if got.ID != job.ID || got.Phase != "error" || got.Previous != previous ||
		got.TargetEngine != "custom" || got.Error == "" {
		t.Fatalf("durable inference failure = %#v", got)
	}
}

func TestAbortOperationCannotBeOverwrittenByCanceledLaunch(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	manager := jobs.NewManager()
	launch := manager.Create("engine:vllm")
	server.observeInferenceJob(launch, "load", "vllm", state.InferenceRuntime{})
	abort := manager.Create("engine:abort")
	server.observeInferenceJob(abort, "abort", "", state.InferenceRuntime{Engine: "vllm"})
	launch.Fail(errors.New("context canceled"))
	if got := store.Get().InferenceOperation; got.ID != abort.ID || got.Action != "abort" {
		t.Fatalf("canceled launch overwrote abort operation: %#v", got)
	}
	abort.Succeed("")
	if got := store.Get().InferenceOperation; got.ID != "" {
		t.Fatalf("successful abort journal remained active: %#v", got)
	}
}

func TestFailedLocalLaunchRestoresPreviousRuntime(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := state.InferenceRuntime{Engine: "vllm", Model: "stable/model", ExecutionMode: "local"}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "sglang", Model: "candidate/model", ExecutionMode: "local"}); err != nil {
		t.Fatal(err)
	}
	operation := state.InferenceOperation{ID: "job-7", Action: "switch", TargetEngine: "sglang", Previous: previous, Phase: "error"}
	if err := store.BeginInferenceOperation(operation); err != nil {
		t.Fatal(err)
	}
	runtime := &inferenceRollbackEngine{}
	server := &Server{state: store, eng: runtime}
	server.rollbackInferenceRuntime(context.Background(), operation.ID)
	if got := store.Get().InferenceRuntime(); got != previous {
		t.Fatalf("runtime after rollback = %#v, want %#v", got, previous)
	}
	if len(runtime.runs) != 1 || runtime.runs[0].Name != "cloudless-vllm" {
		t.Fatalf("previous engine was not relaunched: %#v", runtime.runs)
	}
}
