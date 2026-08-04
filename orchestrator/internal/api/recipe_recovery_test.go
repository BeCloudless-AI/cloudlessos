package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/state"
)

type delayedRecipeRecoveryEngine struct {
	engine.Engine
	failures int32
	probes   atomic.Int32
}

func (e *delayedRecipeRecoveryEngine) Available(context.Context) error {
	probe := e.probes.Add(1)
	if probe <= e.failures {
		return errors.New("broker socket is not ready")
	}
	return nil
}

func TestReconcileInterruptedCheckRemovesOnlyJournaledStagingCheckout(t *testing.T) {
	dir := t.TempDir()
	stateStore, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "Recovery test", Platform: "dgx-spark",
		Engine:  localrecipes.Engine{Type: "vllm", Image: "example.invalid/vllm", ContainerPort: 8890},
		Model:   localrecipes.Model{ID: "example/model", Revision: "main"},
		Runtime: localrecipes.Runtime{Adapter: "native", TimeoutMinutes: 60},
		Health:  localrecipes.Health{Scheme: "http", Host: "127.0.0.1", Port: 8890, Path: "/health"},
	}
	operation, err := operations.Begin(recipeops.KindCheck, recipe, recipeops.PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(dir, "recipe-checks", operation.ID)
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	resource := recipeops.Resource{Kind: "staging-checkout", ID: checkout, Node: localRecipeNodeName()}
	if _, err := operations.Claim(operation.ID, resource); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: stateStore, recipes: localrecipes.New(dir), recipeOps: operations, jobs: jobs.NewManager()}
	if err := server.reconcileRecipePreparationForResume(context.Background(), operation, recipe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Fatalf("staging checkout still exists: %v", err)
	}
	recovered, ok := operations.Get(operation.ID)
	if !ok || recovered.Phase != recipeops.PhaseChecking || len(recovered.Resources) != 0 {
		t.Fatalf("recovered operation = %#v", recovered)
	}
}

func TestRecipeRecoveryActions(t *testing.T) {
	tests := []struct {
		kind  recipeops.Kind
		phase recipeops.Phase
		want  recipeRecoveryAction
	}{
		{recipeops.KindCheck, recipeops.PhaseChecking, recipeRecoveryResumeCheck},
		{recipeops.KindRun, recipeops.PhasePreparing, recipeRecoveryResumeRun},
		{recipeops.KindRun, recipeops.PhasePrepared, recipeRecoveryResumeRun},
		{recipeops.KindRun, recipeops.PhaseSwitching, recipeRecoveryRollback},
		{recipeops.KindRun, recipeops.PhaseStarting, recipeRecoveryRollback},
		{recipeops.KindRun, recipeops.PhaseVerifying, recipeRecoveryRollback},
		{recipeops.KindRun, recipeops.PhaseActive, recipeRecoveryReconcile},
		{recipeops.KindStop, recipeops.PhaseStopping, recipeRecoveryReconcile},
	}
	for _, test := range tests {
		if got := recipeRecoveryActionFor(test.kind, test.phase); got != test.want {
			t.Fatalf("action for %s/%s = %s, want %s", test.kind, test.phase, got, test.want)
		}
	}
}

func TestInterruptedPhaseSurvivesRepeatedRecoveryRestart(t *testing.T) {
	operation := recipeops.Operation{
		Kind: recipeops.KindRun, Phase: recipeops.PhaseRecovering,
		Checkpoints: []recipeops.Checkpoint{
			{Phase: recipeops.PhasePreparing, Sequence: 1},
			{Phase: recipeops.PhaseRecovering, Sequence: 2},
		},
	}
	if got := recipeInterruptedPhase(operation); got != recipeops.PhasePreparing {
		t.Fatalf("interrupted phase = %s, want preparing", got)
	}
}

func TestRecoveryRefusesStagingPathOutsideStateDirectory(t *testing.T) {
	dir := t.TempDir()
	stateStore, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: stateStore}
	outside := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := server.removeRecoveryStagingCheckout(outside); err == nil {
		t.Fatal("unsafe staging path was accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was changed: %v", err)
	}
}

func TestRecipeRecoveryWaitsForContainerBrokerReadiness(t *testing.T) {
	previous := recipeRecoveryEngineProbeInterval
	recipeRecoveryEngineProbeInterval = time.Millisecond
	t.Cleanup(func() { recipeRecoveryEngineProbeInterval = previous })

	runtime := &delayedRecipeRecoveryEngine{failures: 3}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRecipeRecoveryEngine(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	if got := runtime.probes.Load(); got != 4 {
		t.Fatalf("availability probes = %d, want 4", got)
	}
}

func TestRecipeRecoveryKeepsWaitingUntilItsContextEnds(t *testing.T) {
	previous := recipeRecoveryEngineProbeInterval
	recipeRecoveryEngineProbeInterval = time.Millisecond
	t.Cleanup(func() { recipeRecoveryEngineProbeInterval = previous })

	runtime := &delayedRecipeRecoveryEngine{failures: 1000}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	err := waitForRecipeRecoveryEngine(ctx, runtime)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want context deadline", err)
	}
	if runtime.probes.Load() < 2 {
		t.Fatalf("availability probes = %d, want retries", runtime.probes.Load())
	}
}
