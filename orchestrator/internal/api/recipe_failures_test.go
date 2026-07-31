package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestRecipeFailurePlanFailsExactOccurrence(t *testing.T) {
	plan := newRecipeFailurePlan(map[recipeBoundary]int{recipeBoundaryPeerTransfer: 2})
	server := &Server{recipeFailures: plan}
	if err := server.recipeBoundary("rop-test", recipeBoundaryPeerTransfer); err != nil {
		t.Fatalf("first transfer failed: %v", err)
	}
	if err := server.recipeBoundary("rop-test", recipeBoundaryModelDownload); err != nil {
		t.Fatalf("unplanned boundary failed: %v", err)
	}
	err := server.recipeBoundary("rop-test", recipeBoundaryPeerTransfer)
	if err == nil || !strings.Contains(err.Error(), "peer-transfer") {
		t.Fatalf("second transfer failure = %v", err)
	}
	want := []recipeBoundary{recipeBoundaryPeerTransfer, recipeBoundaryModelDownload, recipeBoundaryPeerTransfer}
	got := plan.snapshot()
	if len(got) != len(want) {
		t.Fatalf("visits = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("visits = %#v, want %#v", got, want)
		}
	}
}

func TestProductionRecipeBoundaryIsNoop(t *testing.T) {
	if err := (&Server{}).recipeBoundary("rop-test", recipeBoundaryRuntimeStart); err != nil {
		t.Fatal(err)
	}
}

func TestEveryRecipeFailureBoundaryHasAnInjectedOutcome(t *testing.T) {
	expected := map[recipeBoundary]recipeFailureDisposition{
		recipeBoundarySourceCheckout: recipeFailurePreservesCurrent,
		recipeBoundaryImagePrepare:   recipeFailurePreservesCurrent,
		recipeBoundaryPeerTransfer:   recipeFailurePreservesCurrent,
		recipeBoundaryModelDownload:  recipeFailurePreservesCurrent,
		recipeBoundaryModelTransfer:  recipeFailurePreservesCurrent,
		recipeBoundaryEngineStop:     recipeFailurePreservesCurrent,
		recipeBoundaryRuntimeStart:   recipeFailureRequiresRollback,
		recipeBoundaryPrivateHealth:  recipeFailureRequiresRollback,
		recipeBoundaryProxyPull:      recipeFailureRequiresRollback,
		recipeBoundaryProxyCreate:    recipeFailureRequiresRollback,
		recipeBoundaryPromotion:      recipeFailureRequiresRollback,
		recipeBoundaryStateCommit:    recipeFailureRequiresRollback,
	}
	if len(recipeBoundaryDispositions) != len(expected) {
		t.Fatalf("fault registry has %d boundaries, want %d", len(recipeBoundaryDispositions), len(expected))
	}
	failures := make(map[recipeBoundary]int, len(expected))
	for boundary := range expected {
		failures[boundary] = 1
	}
	server := &Server{recipeFailures: newRecipeFailurePlan(failures)}
	for boundary, disposition := range expected {
		if got := recipeBoundaryDispositions[boundary]; got != disposition {
			t.Fatalf("%s disposition = %s, want %s", boundary, got, disposition)
		}
		if err := server.recipeBoundary("rop-matrix", boundary); err == nil || !strings.Contains(err.Error(), string(boundary)) {
			t.Fatalf("%s injection = %v", boundary, err)
		}
	}
	if err := (&Server{}).recipeBoundary("rop-test", recipeBoundary("new-unclassified-step")); err == nil {
		t.Fatal("unclassified production boundary was accepted")
	}
}

func TestSourceBoundaryFailureIsPersistedBeforeCheckout(t *testing.T) {
	dir := t.TempDir()
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "Failure test", Platform: "dgx-spark",
		Engine:      localrecipes.Engine{Image: "example.invalid/runtime:latest", ContainerPort: 8890},
		Model:       localrecipes.Model{ID: "example/model", Revision: "abc123"},
		Distributed: localrecipes.Distributed{Nodes: 1},
		Runtime:     localrecipes.Runtime{TimeoutMinutes: 1, Lifecycle: localrecipes.Lifecycle{Build: localrecipes.Command{Program: "true"}}},
	}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	plan := newRecipeFailurePlan(map[recipeBoundary]int{recipeBoundarySourceCheckout: 1})
	server := &Server{recipeOps: operations, recipeFailures: plan}
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server.runLocalRecipe(job, recipe, operation.ID)
	if snapshot := job.Snapshot(); !snapshot.Done || snapshot.Phase != "error" || !strings.Contains(snapshot.Error, "source-checkout") {
		t.Fatalf("job after injected failure = %#v", snapshot)
	}
	persisted, ok := operations.Get(operation.ID)
	if !ok || persisted.Phase != recipeops.PhaseFailed || !strings.Contains(persisted.Error, "source-checkout") {
		t.Fatalf("operation after injected failure = %#v, %v", persisted, ok)
	}
}

func TestImagePreparationBoundaryFailurePreservesPreviousRuntimeAndCleansClaims(t *testing.T) {
	previousRoot := recipeRuntimeRoot
	recipeRuntimeRoot = t.TempDir()
	t.Cleanup(func() { recipeRuntimeRoot = previousRoot })

	dir := t.TempDir()
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	previous := state.InferenceRuntime{Engine: "vllm", Model: "stable/model", ExecutionMode: "local"}
	if err := stateStore.CommitInferenceRuntime(previous); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "Image failure", Platform: "dgx-spark",
		Engine:      localrecipes.Engine{Image: "example/runtime:local", ContainerPort: 65431, ServedModelName: localrecipes.CloudlessModelAlias, APIPath: "/v1"},
		Health:      localrecipes.Health{Port: 65431, Path: "/health"},
		Model:       localrecipes.Model{ID: "example/model", Revision: "abc123"},
		Distributed: localrecipes.Distributed{Nodes: 1, MasterPort: 65432},
		Runtime: localrecipes.Runtime{
			WorkingDir: ".", TimeoutMinutes: 1,
			Lifecycle: localrecipes.Lifecycle{
				Build: localrecipes.Command{Program: "true"},
				Start: localrecipes.Command{Program: "true"},
			},
		},
	}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		eng: &recipeAbortEngine{}, state: stateStore, recipeOps: operations,
		recipeFailures: newRecipeFailurePlan(map[recipeBoundary]int{recipeBoundaryImagePrepare: 1}),
	}
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server.runLocalRecipe(job, recipe, operation.ID)
	if snapshot := job.Snapshot(); !snapshot.Done || snapshot.Phase != "error" || !strings.Contains(snapshot.Error, "image-prepare") {
		t.Fatalf("job after image failure = %#v", snapshot)
	}
	persisted, _ := operations.Get(operation.ID)
	if persisted.Phase != recipeops.PhaseFailed || !strings.Contains(persisted.Error, "image-prepare") {
		t.Fatalf("operation after image failure = %#v", persisted)
	}
	if got := stateStore.Get().InferenceRuntime(); got != previous {
		t.Fatalf("pre-switch failure changed active runtime: %#v", got)
	}
	for _, resource := range persisted.Resources {
		if resource.RequiresCleanup() {
			t.Fatalf("pre-switch failure left runtime claim %#v", resource)
		}
	}
	if _, err := os.Stat(recipeCheckout(recipe)); err != nil {
		t.Fatalf("reusable checked-out source was discarded: %v", err)
	}
}

func TestModelDownloadBoundaryFailureRetainsPreparedImageAndCurrentRuntime(t *testing.T) {
	allowUnreviewedRecipeCommandsForTest(t)
	previousRoot, previousCompatibilityDocker := recipeRuntimeRoot, recipeDockerCompatibilityExecutable
	recipeRuntimeRoot = t.TempDir()
	fakeDocker := filepath.Join(t.TempDir(), "docker")
	recipeDockerCompatibilityExecutable = fakeDocker
	t.Cleanup(func() {
		recipeRuntimeRoot, recipeDockerCompatibilityExecutable = previousRoot, previousCompatibilityDocker
	})
	imageID := "sha256:" + strings.Repeat("a", 64)
	imageJSON := `{"Id":"` + imageID + `","RepoDigests":[],"Os":"linux","Architecture":"` + runtime.GOARCH + `","Size":4096,"Config":{"Entrypoint":[],"Cmd":[]}}`
	script := "#!/bin/sh\nif [ \"$1 $2\" = \"image inspect\" ]; then\n  printf '%s\\n' '" + imageJSON + "'\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(fakeDocker, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	previous := state.InferenceRuntime{Engine: "vllm", Model: "stable/model", ExecutionMode: "local"}
	if err := stateStore.CommitInferenceRuntime(previous); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "Download failure", Platform: "dgx-spark",
		Engine:      localrecipes.Engine{Image: "example/runtime:local", ContainerPort: 65421, ServedModelName: localrecipes.CloudlessModelAlias, APIPath: "/v1"},
		Health:      localrecipes.Health{Port: 65421, Path: "/health"},
		Model:       localrecipes.Model{ID: "example/model", Revision: "abc123"},
		Distributed: localrecipes.Distributed{Nodes: 1, MasterPort: 65422},
		Runtime: localrecipes.Runtime{
			WorkingDir: ".", TimeoutMinutes: 1,
			Lifecycle: localrecipes.Lifecycle{
				Build: localrecipes.Command{Program: "true"},
				Start: localrecipes.Command{Program: "true"},
			},
		},
	}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		eng: &recipeAbortEngine{}, state: stateStore, recipeOps: operations,
		recipeFailures: newRecipeFailurePlan(map[recipeBoundary]int{recipeBoundaryModelDownload: 1}),
	}
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server.runLocalRecipe(job, recipe, operation.ID)
	if snapshot := job.Snapshot(); !snapshot.Done || snapshot.Phase != "error" || !strings.Contains(snapshot.Error, "model-download") {
		t.Fatalf("job after model download failure = %#v", snapshot)
	}
	persisted, _ := operations.Get(operation.ID)
	if persisted.Phase != recipeops.PhaseFailed || persisted.PreparedImageDigest != imageID || persisted.PreparedImageReference != imageID {
		t.Fatalf("prepared image was not retained: %#v", persisted)
	}
	if got := stateStore.Get().InferenceRuntime(); got != previous {
		t.Fatalf("download failure changed active runtime: %#v", got)
	}
	for _, resource := range persisted.Resources {
		if resource.RequiresCleanup() {
			t.Fatalf("download failure left runtime claim %#v", resource)
		}
	}
}
