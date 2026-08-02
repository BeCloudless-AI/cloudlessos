package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestOrphanedRecipeContainerNamesReturnsOnlyRunningExactMatches(t *testing.T) {
	recipe := localrecipes.Recipe{
		ID:     "deepseek-v4-flash-dual-dspark-1m",
		Engine: localrecipes.Engine{Image: "example.invalid/cloudless/deepseek:fixed"},
	}
	containers := []engine.Container{
		{Name: "/deepseek-v4-flash-dual-dspark-1m-vllm-1", Image: recipe.Engine.Image, State: "running"},
		{Name: "compose-generated-name", Image: recipe.Engine.Image, State: "running"},
		{Name: "deepseek-v4-flash-dual-dspark-1m-stopped", Image: recipe.Engine.Image, State: "exited"},
		{Name: "unrelated", Image: "example.invalid/other:latest", State: "running"},
	}

	got := orphanedRecipeContainerNames(containers, recipe)
	want := []string{"compose-generated-name", "deepseek-v4-flash-dual-dspark-1m-vllm-1"}
	if len(got) != len(want) {
		t.Fatalf("names = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %#v, want %#v", got, want)
		}
	}
}

func TestOwnedRecipeCleanupRequiresExactActiveRun(t *testing.T) {
	active := recipeops.Operation{ID: "rop-active", Kind: recipeops.KindRun, RecipeID: "recipe-a", Phase: recipeops.PhaseActive}
	if !ownedRecipeCleanupOperationAllowed(active, "recipe-a") {
		t.Fatal("exact active run was not eligible for ownership cleanup")
	}
	for name, operation := range map[string]recipeops.Operation{
		"wrong recipe":   {ID: "rop-active", Kind: recipeops.KindRun, RecipeID: "recipe-b", Phase: recipeops.PhaseActive},
		"stop operation": {ID: "rop-stop", Kind: recipeops.KindStop, RecipeID: "recipe-a", Phase: recipeops.PhaseStopping},
		"failed run":     {ID: "rop-failed", Kind: recipeops.KindRun, RecipeID: "recipe-a", Phase: recipeops.PhaseFailed},
	} {
		t.Run(name, func(t *testing.T) {
			if ownedRecipeCleanupOperationAllowed(operation, "recipe-a") {
				t.Fatalf("unsafe operation accepted: %#v", operation)
			}
		})
	}
	if ownedRecipeCleanupOperationAllowed(active, "") {
		t.Fatal("empty target recipe was accepted")
	}
}

func TestConstrainedRecipeCleanupAllowsUnloadedOrExactActiveOwner(t *testing.T) {
	tests := []struct {
		name           string
		engineUnloaded bool
		activeRecipeID string
		targetRecipeID string
		want           bool
	}{
		{name: "unloaded orphan", engineUnloaded: true, targetRecipeID: "recipe-a", want: true},
		{name: "exact active recipe", activeRecipeID: "recipe-a", targetRecipeID: "recipe-a", want: true},
		{name: "different active recipe", activeRecipeID: "recipe-b", targetRecipeID: "recipe-a", want: false},
		{name: "bundled engine active", activeRecipeID: "", targetRecipeID: "recipe-a", want: false},
		{name: "empty target never owns runtime", activeRecipeID: "", targetRecipeID: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := constrainedRecipeCleanupAllowed(test.engineUnloaded, test.activeRecipeID, test.targetRecipeID); got != test.want {
				t.Fatalf("constrainedRecipeCleanupAllowed(%v, %q, %q) = %v, want %v", test.engineUnloaded, test.activeRecipeID, test.targetRecipeID, got, test.want)
			}
		})
	}
}
