package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/modelfit"
	"github.com/cloudless/orchestrator/internal/models"
)

func recommendationEstimate(status string, required, usable, headroom float64) modelfit.Estimate {
	return modelfit.Estimate{
		Status: status, Evidence: "measured", Confidence: "measured",
		RequiredPerNodeGB: required, UsablePerNodeGB: usable, HeadroomPerNodeGB: headroom,
		ContextK: 32,
	}
}

func TestHardwareRecommendationAnnotatesTheBalancedWinner(t *testing.T) {
	yours := []modelView{{
		Model: models.Model{ID: "installed", ContextK: 32},
		Fit:   "fits", FitEstimate: recommendationEstimate("fits", 20, 100, 80),
	}}
	picks := []modelView{{
		Model: models.Model{ID: "long-context", ContextK: 256},
		Fit:   "fits", FitEstimate: recommendationEstimate("fits", 40, 100, 60),
	}}
	picks[0].FitEstimate.ContextK = 256
	recommendations := applyHardwareModelRecommendations(yours, picks)
	if len(recommendations) == 0 || !picks[0].HardwareRecommended || yours[0].HardwareRecommended {
		t.Fatalf("balanced annotation missing: yours=%#v picks=%#v recommendations=%#v", yours, picks, recommendations)
	}
	if picks[0].HardwareRecommendationReason == "" || picks[0].HardwareRecommendationExecution != "local" {
		t.Fatalf("recommendation explanation missing: %#v", picks[0])
	}
}

func TestHardwareRecommendationUsesReviewedClusterFallback(t *testing.T) {
	cluster := recommendationEstimate("fits", 90, 116, 26)
	picks := []modelView{{
		Model: models.Model{ID: "cluster-only", ContextK: 128},
		Fit:   "over", FitEstimate: recommendationEstimate("over", 140, 116, -24),
		ClusterFit: "fits", ClusterEstimate: &cluster,
	}, {
		Model: models.Model{ID: "unknown", ContextK: 256},
		Fit:   "unknown", FitEstimate: modelfit.Estimate{Status: "unknown", Evidence: "missing"},
	}}
	applyHardwareModelRecommendations(nil, picks)
	if !picks[0].HardwareRecommended || picks[0].HardwareRecommendationExecution != "cluster" {
		t.Fatalf("reviewed cluster fallback was not selected: %#v", picks)
	}
	if picks[1].HardwareRecommended {
		t.Fatalf("unknown model was recommended: %#v", picks[1])
	}
}
