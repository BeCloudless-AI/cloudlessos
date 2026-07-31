package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/state"
)

type recipeRollbackEngine struct {
	engine.Engine
	removed []string
}

func (e *recipeRollbackEngine) Find(_ context.Context, name string) (*engine.Container, error) {
	if name == "cloudless-cluster-engine-proxy" {
		return &engine.Container{Name: name, State: "running"}, nil
	}
	return nil, nil
}

func (e *recipeRollbackEngine) Remove(_ context.Context, name string) error {
	e.removed = append(e.removed, name)
	return nil
}

func TestRestorePreviousOrMarkUnloadedPersistsSafeFallback(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := state.InferenceRuntime{
		Engine: "vllm", Model: "owner/model", ExecutionMode: "cluster",
		LocalRecipeID: "local-0123456789abcdef", EngineUnloaded: false,
	}
	server := &Server{state: store}
	job := jobs.NewManager().Create("recipe:rollback")
	err = server.restorePreviousOrMarkUnloaded(job, previous)
	if err == nil || !strings.Contains(err.Error(), "previous runtime was another local recipe") {
		t.Fatalf("rollback error = %v", err)
	}
	got := store.Get().InferenceRuntime()
	want := previous
	want.EngineUnloaded = true
	if got != want {
		t.Fatalf("safe rollback state = %#v, want %#v", got, want)
	}
}

func TestRestorePreviousOrMarkUnloadedKeepsAlreadyUnloadedState(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := state.InferenceRuntime{
		Engine: "vllm", Model: "owner/model", ExecutionMode: "local", EngineUnloaded: true,
	}
	server := &Server{state: store}
	job := jobs.NewManager().Create("recipe:rollback")
	if err := server.restorePreviousOrMarkUnloaded(job, previous); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().InferenceRuntime(); got != previous {
		t.Fatalf("restored state = %#v, want %#v", got, previous)
	}
}

func TestRollbackFailedRecipeCleansCandidateAndPersistsTruthfulFallback(t *testing.T) {
	allowUnreviewedRecipeCommandsForTest(t)
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := recipeops.PreviousRuntime{
		Engine: "vllm", Model: "stable/model", ExecutionMode: "cluster",
		LocalRecipeID: "local-aaaaaaaaaaaaaaaa", EngineUnloaded: false,
	}
	operation := recipeops.Operation{ID: "rop-test", PreviousRuntime: &previous}
	workdir := t.TempDir()
	marker := filepath.Join(workdir, "stopped")
	recipe := localrecipes.Recipe{Runtime: localrecipes.Runtime{Lifecycle: localrecipes.Lifecycle{
		Stop: localrecipes.Command{Program: "bash", Args: []string{"-c", "touch stopped"}},
	}}}
	containerEngine := &recipeRollbackEngine{}
	server := &Server{state: store, eng: containerEngine}
	job := jobs.NewManager().Create("recipe:rollback")
	cause := errors.New("injected private-health failure")
	result := server.rollbackFailedRecipe(job, operation, recipe, workdir, nil, cause)
	if result == nil || !strings.Contains(result.Error(), cause.Error()) || !strings.Contains(result.Error(), "previous runtime was another local recipe") {
		t.Fatalf("rollback result = %v", result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("candidate stop lifecycle did not run: %v", err)
	}
	if len(containerEngine.removed) != 1 || containerEngine.removed[0] != "cloudless-cluster-engine-proxy" {
		t.Fatalf("stable candidate proxy was not removed: %#v", containerEngine.removed)
	}
	want := state.InferenceRuntime{
		Engine: previous.Engine, Model: previous.Model, ExecutionMode: previous.ExecutionMode,
		LocalRecipeID: previous.LocalRecipeID, EngineUnloaded: true,
	}
	if got := store.Get().InferenceRuntime(); got != want {
		t.Fatalf("rollback state = %#v, want %#v", got, want)
	}
}
