package api

import (
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestRecipeFailureViewIsActionableAndRedacted(t *testing.T) {
	operation := recipeops.Operation{
		ID: "op-1", RecipeID: "recipe-1", Phase: recipeops.PhaseFailed, UpdatedAt: "2026-07-31T00:00:00Z",
		Error:    "download returned 429 token=hf_supersecrettoken",
		Progress: &recipeops.Progress{Stage: "downloading", Message: "Downloading"},
		Resources: []recipeops.Resource{
			{Kind: "model-cache", ID: "org/model@commit", Node: "Spark A"},
			{Kind: "process-set", ID: "op-1", Node: "Spark B"},
		},
	}
	view := buildRecipeFailureView(operation)
	if view.Category != "dependency" || !view.Retryable || !strings.Contains(view.Remedy, "preserved") {
		t.Fatalf("failure view = %#v", view)
	}
	if strings.Contains(view.Detail, "hf_supersecrettoken") || !strings.Contains(view.Detail, "<redacted>") {
		t.Fatalf("failure detail was not redacted: %q", view.Detail)
	}
	if !view.CleanupPending || len(view.AffectedNodes) != 1 || view.AffectedNodes[0] != "Spark B" || len(view.RetainedArtifacts) != 1 {
		t.Fatalf("failure ownership = %#v", view)
	}
}

func TestRecipeFailureViewsReturnsNewestFailurePerRecipe(t *testing.T) {
	views := recipeFailureViews([]recipeops.Operation{
		{ID: "old", RecipeID: "recipe", Phase: recipeops.PhaseFailed, UpdatedAt: "2026-07-30T00:00:00Z", Error: "old"},
		{ID: "new", RecipeID: "recipe", Phase: recipeops.PhaseFailed, UpdatedAt: "2026-07-31T00:00:00Z", Error: "health check failed"},
		{ID: "other", RecipeID: "recipe", Phase: recipeops.PhaseStopped, UpdatedAt: "2026-07-29T00:00:00Z"},
	})
	if len(views) != 1 || views["recipe"].OperationID != "new" || views["recipe"].Category != "runtime-start" {
		t.Fatalf("failure views = %#v", views)
	}
}

func TestNewerSuccessfulOperationClearsRecipeFailureView(t *testing.T) {
	views := recipeFailureViews([]recipeops.Operation{
		{ID: "failed", RecipeID: "recipe", Phase: recipeops.PhaseFailed, UpdatedAt: "2026-07-30T00:00:00Z", Error: "failed"},
		{ID: "active", RecipeID: "recipe", Phase: recipeops.PhaseActive, UpdatedAt: "2026-07-31T00:00:00Z"},
	})
	if len(views) != 0 {
		t.Fatalf("stale failure remained after success: %#v", views)
	}
}

func TestRecipeDiagnosticRedactionCoversURLCredentialsAndCommonTokens(t *testing.T) {
	raw := "https://alice:hunter2@example.com github_pat_123456789abcdef PASSWORD=hunter2"
	redacted := redactRecipeDiagnosticText(raw)
	for _, secret := range []string{"alice", "hunter2", "github_pat_123456789abcdef"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("%q remains in %q", secret, redacted)
		}
	}
}
