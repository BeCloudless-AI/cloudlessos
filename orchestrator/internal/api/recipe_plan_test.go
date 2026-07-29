package api

import (
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestDeepSeekLaunchPlanExplainsCustomRuntime(t *testing.T) {
	recipe := localrecipes.Recipe{ID: localrecipes.DeepSeekDSparkID}
	recipe.Source.URL = localrecipes.DeepSeekDSparkSource
	recipe.Source.Revision = localrecipes.DeepSeekDSparkRevision
	recipe.Runtime.Lifecycle.Build.Program = "bash"
	explanation := recipeRuntimeExplanation(recipe)
	for _, want := range []string{"standard Cloudless vLLM", "DeepSeek V4", "sparse MLA", "speculative decoding", "SM120/SM121"} {
		if !strings.Contains(explanation, want) {
			t.Fatalf("custom runtime explanation is missing %q: %s", want, explanation)
		}
	}
	if got := recipeRuntimeEstimate(recipe); got != deepSeekRuntimeEstimateBytes {
		t.Fatalf("runtime estimate = %d", got)
	}
}

func TestGenericRecipeLaunchPlanDoesNotClaimDeepSeekPatches(t *testing.T) {
	recipe := localrecipes.Recipe{}
	recipe.Runtime.Lifecycle.Build.Program = "make"
	explanation := recipeRuntimeExplanation(recipe)
	if !strings.Contains(explanation, "own pinned inference runtime") || strings.Contains(explanation, "DeepSeek") {
		t.Fatalf("generic explanation = %q", explanation)
	}
	if recipeRuntimeEstimate(recipe) != 0 {
		t.Fatal("generic recipe received a fabricated runtime size")
	}
}
