package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/state"
)

type preflightOrderingEngine struct {
	engine.Engine
	pullCalls int
}

func (e *preflightOrderingEngine) PullStream(context.Context, string, func(string)) error {
	e.pullCalls++
	return nil
}

func TestManagedRecipeRejectsChangedClusterBeforeImagePull(t *testing.T) {
	recipe := managedContainerRecipeForTest()
	operations, err := recipeops.Open(filepath.Join(t.TempDir(), "operations"))
	if err != nil {
		t.Fatal(err)
	}
	prepareRecipeMatrixPreflight(t, operations, recipe, "sha256:"+strings.Repeat("a", 64))
	run, err := operations.BeginRunValidated(recipe)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &preflightOrderingEngine{}
	server := &Server{
		eng:       runtime,
		recipeOps: operations,
		recipeRunPreflight: func(context.Context, localrecipes.Recipe, recipeops.Operation) error {
			return errRecipeRunPreflightChanged
		},
	}
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server.runManagedContainerRecipe(job, recipe, run.ID)

	if runtime.pullCalls != 0 {
		t.Fatalf("stale preflight started %d image pull(s)", runtime.pullCalls)
	}
	snapshot := job.Snapshot()
	if !snapshot.Done || snapshot.Phase != "error" || !strings.Contains(snapshot.Error, "run Check again") {
		t.Fatalf("stale preflight job = %#v", snapshot)
	}
	persisted, ok := operations.Get(run.ID)
	if !ok || persisted.Phase != recipeops.PhaseFailed || !strings.Contains(persisted.Error, "run Check again") {
		t.Fatalf("stale preflight operation = %#v, %v", persisted, ok)
	}
}

func TestRecipeRunPreflightAllowsUnchangedAdmission(t *testing.T) {
	called := false
	server := &Server{
		recipeRunPreflight: func(context.Context, localrecipes.Recipe, recipeops.Operation) error {
			called = true
			return nil
		},
	}
	if err := server.validateRecipeRunPreflight(context.Background(), managedContainerRecipeForTest(), recipeops.Operation{}); err != nil {
		t.Fatalf("unchanged preflight was rejected: %v", err)
	}
	if !called {
		t.Fatal("run admission did not revalidate the current environment")
	}
}

func TestRecipeRunAPIRejectsChangedClusterBeforeCreatingJob(t *testing.T) {
	allowUnreviewedRecipeCommandsForTest(t)
	root := t.TempDir()
	modelRoot := filepath.Join(root, "models")
	t.Setenv("CLOUDLESS_MODEL_CACHE", modelRoot)

	recipes := localrecipes.New(filepath.Join(root, "recipes"))
	draftRecipe := managedContainerRecipeForTest()
	delete(draftRecipe.Runtime.Environment, "HF_CACHE")
	recipe, err := recipes.Create(localrecipes.DraftFromSnapshot(draftRecipe))
	if err != nil {
		t.Fatal(err)
	}
	writeRecipeMatrixModelCache(t, recipe, modelRoot)
	operations, err := recipeops.Open(filepath.Join(root, "operations"))
	if err != nil {
		t.Fatal(err)
	}
	prepareRecipeMatrixPreflight(t, operations, recipe, "sha256:"+strings.Repeat("a", 64))
	stateStore, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.NewManager()
	server := &Server{
		recipes: recipes, recipeOps: operations, state: stateStore, jobs: manager,
		recipeRunPreflight: func(context.Context, localrecipes.Recipe, recipeops.Operation) error {
			return errRecipeRunPreflightChanged
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/recipes/"+recipe.ID+"/run", nil)
	request.SetPathValue("id", recipe.ID)
	response := httptest.NewRecorder()
	server.localRecipeRun(response, request)

	if response.Code != http.StatusPreconditionFailed || !strings.Contains(response.Body.String(), `"action":"check"`) {
		t.Fatalf("stale run response = %d: %s", response.Code, response.Body.String())
	}
	if running := manager.List("recipe:"); len(running) != 0 {
		t.Fatalf("stale admission created jobs: %#v", running)
	}
	var failedRuns int
	for _, operation := range operations.List() {
		if operation.Kind == recipeops.KindRun && operation.Phase == recipeops.PhaseFailed {
			failedRuns++
		}
	}
	if failedRuns != 1 {
		t.Fatalf("stale admission persisted %d failed run operations, want 1", failedRuns)
	}
}
