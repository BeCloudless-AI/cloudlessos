// Package modelrecommend ranks only models with reviewed runtime-fit evidence.
// It deliberately keeps hardware admission separate from preference ranking:
// an attractive quality score can never make an unknown or oversized runtime
// appear compatible.
package modelrecommend

import (
	"math"
	"sort"
	"strconv"

	"github.com/cloudless/orchestrator/internal/modelfit"
)

type Candidate struct {
	ID              string
	Estimate        modelfit.Estimate
	ContextK        int
	QualityScore    float64
	QualityEvidence string
	FidelityScore   float64
	Execution       string
}

type Recommendation struct {
	Intent     string   `json:"intent"`
	ModelID    string   `json:"modelId"`
	Execution  string   `json:"execution"`
	Score      float64  `json:"score"`
	Reasons    []string `json:"reasons"`
	Evidence   []string `json:"evidence"`
	Confidence string   `json:"confidence"`
}

type scored struct {
	Candidate
	memory, context, quality, speed, fidelity float64
}

func clamp01(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}

func eligible(candidate Candidate) bool {
	return (candidate.Estimate.Status == "fits" || candidate.Estimate.Status == "tight") &&
		candidate.Estimate.Evidence != "missing" && candidate.Estimate.RequiredPerNodeGB > 0 &&
		candidate.Estimate.UsablePerNodeGB > 0
}

func prepare(candidates []Candidate) []scored {
	out := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		if !eligible(candidate) {
			continue
		}
		item := scored{Candidate: candidate}
		item.memory = clamp01(candidate.Estimate.HeadroomPerNodeGB / candidate.Estimate.UsablePerNodeGB)
		// Context utility intentionally saturates at 256K: beyond that point the
		// hardware fit and model quality matter more than a headline maximum.
		item.context = clamp01(float64(candidate.ContextK) / 256)
		item.quality = clamp01(candidate.QualityScore / 100)
		item.fidelity = clamp01(candidate.FidelityScore / 100)
		// Decode throughput utility saturates at 100 tok/s. Missing evidence is
		// zero and causes the speed intent to be omitted entirely.
		item.speed = clamp01(candidate.Estimate.EstimatedTokensPerSecond / 100)
		out = append(out, item)
	}
	return out
}

type weights struct{ memory, context, quality, speed, fidelity float64 }

func utility(item scored, w weights) float64 {
	weighted, total := 0.0, 0.0
	add := func(value, weight float64) {
		if weight <= 0 {
			return
		}
		weighted += value * weight
		total += weight
	}
	add(item.memory, w.memory)
	add(item.context, w.context)
	if item.QualityScore > 0 {
		add(item.quality, w.quality)
	}
	if item.Estimate.EstimatedTokensPerSecond > 0 {
		add(item.speed, w.speed)
	}
	if item.FidelityScore > 0 {
		add(item.fidelity, w.fidelity)
	}
	if total == 0 {
		return 0
	}
	return weighted / total
}

func choose(items []scored, intent string, w weights) Recommendation {
	ordered := append([]scored(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := utility(ordered[i], w), utility(ordered[j], w)
		if left != right {
			return left > right
		}
		// Deterministic safety-oriented tie breaks.
		if ordered[i].memory != ordered[j].memory {
			return ordered[i].memory > ordered[j].memory
		}
		return ordered[i].ID < ordered[j].ID
	})
	if len(ordered) == 0 {
		return Recommendation{}
	}
	winner := ordered[0]
	recommendation := Recommendation{
		Intent: intent, ModelID: winner.ID, Execution: winner.Execution,
		Score:    math.Round(utility(winner, w)*1000) / 10,
		Reasons:  []string{formatMemoryReason(winner.Estimate.HeadroomPerNodeGB)},
		Evidence: []string{winner.Estimate.Evidence}, Confidence: winner.Estimate.Confidence,
	}
	if winner.ContextK > 0 {
		recommendation.Reasons = append(recommendation.Reasons, formatContextReason(winner.ContextK))
	}
	if winner.QualityScore > 0 {
		recommendation.Evidence = append(recommendation.Evidence, "quality:"+winner.QualityEvidence)
	}
	if winner.Estimate.EstimatedTokensPerSecond > 0 {
		recommendation.Evidence = append(recommendation.Evidence, "performance:"+winner.Estimate.PerformanceEvidence)
	}
	return recommendation
}

func formatMemoryReason(headroom float64) string {
	return "reviewed runtime leaves " + formatNumber(headroom) + " GB memory headroom per node"
}

func formatContextReason(contextK int) string {
	return "supports " + formatNumber(float64(contextK)) + "K context"
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(math.Round(value*10)/10, 'f', -1, 64)
}

// Rank returns only intentions supported by real evidence. Balanced and
// lightweight always use reviewed fit/context data. Quality and speed choices
// appear only when at least one eligible candidate carries that evidence.
func Rank(candidates []Candidate) []Recommendation {
	items := prepare(candidates)
	if len(items) == 0 {
		return nil
	}
	allQuality, allSpeed, allFidelity := true, true, true
	qualityItems, speedItems := make([]scored, 0, len(items)), make([]scored, 0, len(items))
	for _, item := range items {
		allQuality = allQuality && item.QualityScore > 0
		allSpeed = allSpeed && item.Estimate.EstimatedTokensPerSecond > 0
		allFidelity = allFidelity && item.FidelityScore > 0
		if item.QualityScore > 0 {
			qualityItems = append(qualityItems, item)
		}
		if item.Estimate.EstimatedTokensPerSecond > 0 {
			speedItems = append(speedItems, item)
		}
	}
	balancedWeights := weights{memory: .55, context: .45}
	if allQuality {
		balancedWeights = weights{memory: .45, context: .30, quality: .15, speed: .05, fidelity: .05}
		if !allSpeed {
			balancedWeights.speed = 0
		}
		if !allFidelity {
			balancedWeights.fidelity = 0
		}
	}
	result := []Recommendation{
		choose(items, "balanced", balancedWeights),
		choose(items, "lowest-memory", weights{memory: 1}),
	}
	if len(qualityItems) > 0 {
		qualityWeights := weights{quality: 1}
		if allFidelity {
			qualityWeights = weights{quality: .85, fidelity: .15}
		}
		result = append(result, choose(qualityItems, "highest-quality", qualityWeights))
	}
	if len(speedItems) > 0 {
		result = append(result, choose(speedItems, "fastest", weights{speed: .90, memory: .10}))
	}
	return result
}
