package modelfit

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/models"
)

func reviewedModel(required float64) models.Model {
	return models.Model{ID: "example/model", FitProfiles: []models.FitProfile{{
		ID: "single-vllm", Evidence: "reviewed-estimate", Source: "qualification sheet",
		Engine: "vllm", Architectures: []string{"amd64"}, MemoryTypes: []string{"dedicated"},
		MinNodes: 1, MaxNodes: 1, ContextK: 32, RequiredPerNodeGB: required,
	}}}
}

func TestReviewedRequirementDeterminesFitWithoutParameterHeuristic(t *testing.T) {
	model := reviewedModel(20)
	model.Params = "999B / 1B active"
	got := EstimateModel(model, Envelope{
		MemoryGB: 24, MemoryType: "dedicated", Nodes: 1,
		Engine: "vllm", Architecture: "amd64", Platform: "generic",
	})
	if got.Status != "tight" || got.RequiredPerNodeGB != 20 || got.Confidence != "reviewed" {
		t.Fatalf("fit = %#v", got)
	}
	if got.ProfileID != "single-vllm" || got.EvidenceSource != "qualification sheet" {
		t.Fatalf("evidence missing: %#v", got)
	}
}

func TestWrongRuntimeArchitectureOrTopologyIsUnknown(t *testing.T) {
	model := reviewedModel(12)
	cases := []Envelope{
		{MemoryGB: 24, MemoryType: "dedicated", Nodes: 1, Engine: "sglang", Architecture: "amd64"},
		{MemoryGB: 24, MemoryType: "dedicated", Nodes: 1, Engine: "vllm", Architecture: "arm64"},
		{MemoryGB: 24, MemoryType: "dedicated", Nodes: 2, Sharded: true, Engine: "vllm", Architecture: "amd64"},
	}
	for _, env := range cases {
		got := EstimateModel(model, env)
		if got.Status != "unknown" || got.Evidence != "missing" || got.RequiredPerNodeGB != 0 {
			t.Fatalf("environment %#v produced false claim %#v", env, got)
		}
	}
}

func TestMeasuredSparkClusterProfileDoesNotTreatMemoryAsOnePool(t *testing.T) {
	model := models.Model{FitProfiles: []models.FitProfile{{
		ID: "spark-pair", Evidence: "measured", Source: "physical qualification",
		Engine: "vllm", Architectures: []string{"arm64"}, Platforms: []string{"dgx-spark"},
		MemoryTypes: []string{"unified"}, MinNodes: 2, MaxNodes: 2, Sharded: true,
		ContextK: 32, RequiredPerNodeGB: 100,
	}}}
	got := EstimateModel(model, Envelope{
		MemoryGB: 128, MemoryType: "unified", Nodes: 2, Sharded: true,
		Engine: "vllm", Architecture: "arm64", Platform: "dgx-spark",
	})
	if got.Status != "tight" || got.RequiredPerNodeGB != 100 || got.RequiredGB != 200 {
		t.Fatalf("cluster fit = %#v", got)
	}
	if got.UsablePerNodeGB >= 128 || got.Confidence != "measured" {
		t.Fatalf("cluster evidence = %#v", got)
	}
}

func TestUnifiedFitSeparatesPhysicalCapacityFromCurrentHeadroom(t *testing.T) {
	model := models.Model{ID: "example", FitProfiles: []models.FitProfile{{
		ID: "spark", Evidence: "measured", Engine: "vllm",
		Architectures: []string{"arm64"}, Platforms: []string{"dgx-spark"},
		MemoryTypes: []string{"unified"}, MinNodes: 1, MaxNodes: 1,
		RequiredPerNodeGB: 100,
	}}}
	got := EstimateModel(model, Envelope{
		MemoryGB: 128, AvailableGB: 110, ReservedGB: 12,
		MemoryType: "unified", Nodes: 1, Engine: "vllm",
		Architecture: "arm64", Platform: "dgx-spark",
	})
	if got.TotalPerNodeGB != 128 || got.ReservedPerNodeGB != 12 ||
		got.UsablePerNodeGB != 116 || got.AvailablePerNodeGB != 110 {
		t.Fatalf("physical/capacity/current accounting was conflated: %#v", got)
	}
	if got.HeadroomPerNodeGB != 16 || got.LaunchHeadroomPerNodeGB != -2 {
		t.Fatalf("capacity and current launch margins were not distinguished: %#v", got)
	}
}

func TestParameterOnlyCustomModelFailsHonestly(t *testing.T) {
	got := EstimateModel(models.Model{Params: "284B", Quant: "FP4"}, Envelope{
		MemoryGB: 128, MemoryType: "unified", Nodes: 2, Sharded: true,
		Engine: "vllm", Architecture: "arm64", Platform: "dgx-spark",
	})
	if got.Status != "unknown" || got.Confidence != "unknown" || got.RequiredPerNodeGB != 0 {
		t.Fatalf("parameter-only model = %#v", got)
	}
}
