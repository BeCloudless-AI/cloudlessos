package modelrecommend

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/modelfit"
	"github.com/cloudless/orchestrator/internal/models"
)

func candidate(id, status string, required, usable, headroom float64, contextK int) Candidate {
	return Candidate{ID: id, ContextK: contextK, Execution: "local", Estimate: modelfit.Estimate{
		Status: status, Evidence: "measured", Confidence: "measured",
		RequiredPerNodeGB: required, UsablePerNodeGB: usable, HeadroomPerNodeGB: headroom,
	}}
}

func find(result []Recommendation, intent string) Recommendation {
	for _, item := range result {
		if item.Intent == intent {
			return item
		}
	}
	return Recommendation{}
}

func TestRankRejectsUnknownAndOversizedCandidates(t *testing.T) {
	unknown := candidate("unknown", "unknown", 0, 128, 128, 256)
	over := candidate("over", "over", 140, 116, -24, 256)
	fit := candidate("fit", "fits", 80, 116, 36, 64)
	result := Rank([]Candidate{unknown, over, fit})
	if got := find(result, "balanced").ModelID; got != "fit" {
		t.Fatalf("balanced = %q, want admitted fit", got)
	}
}

func TestBalancedCombinesReviewedHeadroomAndContext(t *testing.T) {
	roomy := candidate("roomy", "fits", 30, 100, 70, 32)
	long := candidate("long", "fits", 55, 100, 45, 256)
	if got := find(Rank([]Candidate{roomy, long}), "balanced").ModelID; got != "long" {
		t.Fatalf("balanced = %q, want long", got)
	}
	if got := find(Rank([]Candidate{roomy, long}), "lowest-memory").ModelID; got != "roomy" {
		t.Fatalf("lowest-memory = %q, want roomy", got)
	}
}

func TestEvidenceSpecificIntentsOnlyAppearWhenSupported(t *testing.T) {
	plain := candidate("plain", "fits", 30, 100, 70, 32)
	if got := find(Rank([]Candidate{plain}), "fastest"); got.ModelID != "" {
		t.Fatalf("speed recommendation fabricated without evidence: %#v", got)
	}
	measured := candidate("measured", "fits", 45, 100, 55, 32)
	measured.QualityScore, measured.QualityEvidence = 88, "benchmark-v1"
	measured.Estimate.EstimatedTokensPerSecond, measured.Estimate.PerformanceEvidence = 72, "lab-v1"
	result := Rank([]Candidate{plain, measured})
	if got := find(result, "highest-quality").ModelID; got != "measured" {
		t.Fatalf("highest-quality = %q", got)
	}
	if got := find(result, "fastest").ModelID; got != "measured" {
		t.Fatalf("fastest = %q", got)
	}
}

func TestMissingEvidenceCannotWinEvidenceSpecificIntent(t *testing.T) {
	unknownSpeed := candidate("unknown-speed", "fits", 1, 100, 99, 32)
	measured := candidate("measured-speed", "fits", 80, 100, 20, 32)
	measured.Estimate.EstimatedTokensPerSecond = 15
	measured.Estimate.PerformanceEvidence = "measured-run"
	if got := find(Rank([]Candidate{unknownSpeed, measured}), "fastest").ModelID; got != "measured-speed" {
		t.Fatalf("model without throughput evidence won fastest: %q", got)
	}
}

func TestRankingIsDeterministic(t *testing.T) {
	a := candidate("a", "fits", 50, 100, 50, 32)
	b := candidate("b", "fits", 50, 100, 50, 32)
	if got := find(Rank([]Candidate{b, a}), "balanced").ModelID; got != "a" {
		t.Fatalf("tie break = %q, want a", got)
	}
}

func TestFreshSparkCatalogProducesAnEvidenceBackedRecommendation(t *testing.T) {
	candidates := make([]Candidate, 0)
	for _, model := range models.All() {
		estimate := modelfit.EstimateModel(model, modelfit.Envelope{
			MemoryGB: 128, AvailableGB: 116, ReservedGB: 12,
			MemoryType: "unified", Nodes: 1, Engine: "vllm",
			Architecture: "arm64", Platform: "dgx-spark",
		})
		candidates = append(candidates, Candidate{
			ID: model.ID, Estimate: estimate, ContextK: estimate.ContextK,
			QualityScore: model.QualityScore, QualityEvidence: model.QualityEvidence,
			FidelityScore: model.FidelityScore, Execution: "local",
		})
	}
	balanced := find(Rank(candidates), "balanced")
	if balanced.ModelID == "" || balanced.Confidence == "unknown" || len(balanced.Reasons) < 2 {
		t.Fatalf("fresh Spark recommendation is incomplete: %#v", balanced)
	}
}
