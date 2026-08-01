package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
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
