package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestSparkClusterDeniedOnGenericSystem(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	t.Setenv("CLOUDLESS_ARCH", "amd64")
	rec := httptest.NewRecorder()
	(&Server{}).sparkClusterStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/spark-cluster", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "spark-cluster") {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestClusterDisconnectBlocksInFlightRecipeButAllowsSafeStopOfActiveRecipe(t *testing.T) {
	recipe := localrecipes.Recipe{ID: "local-0123456789abcdef", Name: "cluster recipe", Engine: localrecipes.Engine{ContainerPort: 8890}, Model: localrecipes.Model{ID: "owner/model", Revision: "rev"}, Runtime: localrecipes.Runtime{TimeoutMinutes: 1}}
	inFlight, err := recipeops.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inFlight.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing); err != nil {
		t.Fatal(err)
	}
	if operation, blocked := recipeOperationBlockingClusterDisconnect(inFlight); !blocked || operation.RecipeID != recipe.ID {
		t.Fatalf("in-flight recipe did not block disconnect: %#v %v", operation, blocked)
	}

	active, err := recipeops.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := active.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []recipeops.Phase{recipeops.PhasePrepared, recipeops.PhaseSwitching, recipeops.PhaseStarting, recipeops.PhaseVerifying, recipeops.PhaseActive} {
		if _, err := active.Transition(operation.ID, phase, nil); err != nil {
			t.Fatal(err)
		}
	}
	if operation, blocked := recipeOperationBlockingClusterDisconnect(active); blocked {
		t.Fatalf("active recipe should enter the synchronous safe-stop path: %#v", operation)
	}
}

func TestSparkClusterCreateRequiresExplicitConfirmation(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	rec := httptest.NewRecorder()
	(&Server{}).sparkClusterCreate(rec, httptest.NewRequest(http.MethodPost, "/api/system/spark-cluster/create", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSparkClusterDisconnectRequiresExplicitConfirmation(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	rec := httptest.NewRecorder()
	(&Server{}).sparkClusterDisconnect(rec, httptest.NewRequest(http.MethodPost, "/api/system/spark-cluster/disconnect", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSparkClusterRebindRequiresExplicitConfirmation(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	rec := httptest.NewRecorder()
	(&Server{}).sparkClusterRebind(rec, httptest.NewRequest(http.MethodPost, "/api/system/spark-cluster/rebind", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestInferenceOperationBlocksClusterMutationUntilTerminalError(t *testing.T) {
	for _, phase := range []string{"pending", "preparing", "downloading", "starting-workers", "loading", "optimizing", "verifying", "stopping", "rollback"} {
		t.Run(phase, func(t *testing.T) {
			if !inferenceOperationBlocksClusterMutation(state.InferenceOperation{ID: "job-1", Phase: phase}) {
				t.Fatal("active inference operation did not block cluster mutation")
			}
		})
	}
	if inferenceOperationBlocksClusterMutation(state.InferenceOperation{ID: "job-1", Phase: "error"}) {
		t.Fatal("terminal failed inference operation still blocks cluster mutation")
	}
	if inferenceOperationBlocksClusterMutation(state.InferenceOperation{}) {
		t.Fatal("empty inference operation blocks cluster mutation")
	}
}

func TestClusterDisconnectKeepsSingleSparkCompatibleModel(t *testing.T) {
	model, changed := localModelAfterClusterDisconnect(state.State{Model: "Qwen/Qwen3.6-35B-A3B"}, 120)
	if changed || model != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("fallback = %q, changed = %v", model, changed)
	}
}

func TestClusterDisconnectFallsBackFromClusterOnlyModel(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	model, changed := localModelAfterClusterDisconnect(state.State{Model: "deepseek-ai/DeepSeek-V4-Flash"}, 120)
	if !changed || model != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("fallback = %q, changed = %v", model, changed)
	}
}
