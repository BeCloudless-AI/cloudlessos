package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestParseRecipePortInspectionReportsOwner(t *testing.T) {
	result := parseRecipePortInspection(8890, "spark-a", `LISTEN 0 4096 0.0.0.0:8890 0.0.0.0:* users:(("python",pid=4321,fd=9))`)
	if !result.Listening || result.Owner != "python (pid 4321)" || result.Node != "spark-a" {
		t.Fatalf("port inspection = %#v", result)
	}
	free := parseRecipePortInspection(8890, "spark-a", "")
	if free.Listening || free.Owner != "" {
		t.Fatalf("free port inspection = %#v", free)
	}
}

func TestRecipeOperationOwnsExactPortAndNode(t *testing.T) {
	operation := recipeops.Operation{Resources: []recipeops.Resource{{Kind: "private-port", ID: "8890", Node: "spark-a"}}}
	if !recipeOperationOwnsPort(operation, "private-port", 8890, "spark-a") {
		t.Fatal("owned port was not recognized")
	}
	if recipeOperationOwnsPort(operation, "private-port", 8890, "spark-b") || recipeOperationOwnsPort(operation, "stable-port", 8890, "spark-a") {
		t.Fatal("different port ownership was accepted")
	}
}
