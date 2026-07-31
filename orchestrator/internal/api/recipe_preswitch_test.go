package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestCheckedRecipeModelBytesUsesHashBoundCapacityEvidence(t *testing.T) {
	artifact := recipeops.PreflightArtifact{Checks: []recipeops.CheckResult{
		{ID: "capacity", Values: map[string]string{"local.modelBytes": "123456789"}},
	}}
	got, err := checkedRecipeModelBytes(artifact)
	if err != nil || got != 123456789 {
		t.Fatalf("model bytes = %d, %v", got, err)
	}
}

func TestCheckedRecipeModelBytesRejectsMissingOrInvalidEvidence(t *testing.T) {
	for _, artifact := range []recipeops.PreflightArtifact{
		{},
		{Checks: []recipeops.CheckResult{{ID: "capacity", Values: map[string]string{"local.modelBytes": "unknown"}}}},
		{Checks: []recipeops.CheckResult{{ID: "capacity", Values: map[string]string{"local.modelBytes": "-1"}}}},
	} {
		if _, err := checkedRecipeModelBytes(artifact); err == nil {
			t.Fatalf("invalid evidence was accepted: %#v", artifact)
		}
	}
}
