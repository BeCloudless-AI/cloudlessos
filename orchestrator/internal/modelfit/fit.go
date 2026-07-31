// Package modelfit matches models to reviewed runtime envelopes. It never
// derives a compatibility verdict from a marketing parameter-count label.
package modelfit

import (
	"math"
	"strings"

	"github.com/cloudless/orchestrator/internal/models"
)

type Envelope struct {
	MemoryGB     float64 `json:"memoryGB"`
	AvailableGB  float64 `json:"availableGB,omitempty"`
	ReservedGB   float64 `json:"reservedGB,omitempty"`
	MemoryType   string  `json:"memoryType"` // dedicated | unified
	Nodes        int     `json:"nodes"`
	Sharded      bool    `json:"sharded"`
	Engine       string  `json:"engine"`
	Architecture string  `json:"architecture"`
	Platform     string  `json:"platform"`
}

type Estimate struct {
	Status                  string  `json:"status"` // fits | tight | over | unknown
	MemoryType              string  `json:"memoryType"`
	Nodes                   int     `json:"nodes"`
	Sharded                 bool    `json:"sharded"`
	Engine                  string  `json:"engine,omitempty"`
	Architecture            string  `json:"architecture,omitempty"`
	Platform                string  `json:"platform,omitempty"`
	ProfileID               string  `json:"profileId,omitempty"`
	Evidence                string  `json:"evidence"` // measured | reviewed-estimate | missing
	EvidenceSource          string  `json:"evidenceSource,omitempty"`
	ContextK                int     `json:"contextK,omitempty"`
	RequiredGB              float64 `json:"requiredGB,omitempty"`
	RequiredPerNodeGB       float64 `json:"requiredPerNodeGB,omitempty"`
	TotalPerNodeGB          float64 `json:"totalPerNodeGB,omitempty"`
	ReservedPerNodeGB       float64 `json:"reservedPerNodeGB,omitempty"`
	UsablePerNodeGB         float64 `json:"usablePerNodeGB,omitempty"`
	HeadroomPerNodeGB       float64 `json:"headroomPerNodeGB,omitempty"`
	AvailablePerNodeGB      float64 `json:"availablePerNodeGB,omitempty"`
	LaunchHeadroomPerNodeGB float64 `json:"launchHeadroomPerNodeGB,omitempty"`
	Confidence              string  `json:"confidence"` // measured | reviewed | unknown
	Reason                  string  `json:"reason"`
}

func defaultReserve(total float64, kind string) float64 {
	if total <= 0 {
		return 0
	}
	if strings.EqualFold(kind, "unified") {
		return math.Min(total/2, math.Max(12, total*0.08))
	}
	return total * 0.10
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func includes(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

func profileMatches(profile models.FitProfile, env Envelope, nodes int) bool {
	minNodes, maxNodes := profile.MinNodes, profile.MaxNodes
	if minNodes < 1 {
		minNodes = 1
	}
	if maxNodes < minNodes {
		maxNodes = minNodes
	}
	return nodes >= minNodes && nodes <= maxNodes &&
		profile.Sharded == env.Sharded &&
		(profile.Engine == "" || strings.EqualFold(profile.Engine, env.Engine)) &&
		includes(profile.Architectures, env.Architecture) &&
		includes(profile.Platforms, env.Platform) &&
		includes(profile.MemoryTypes, env.MemoryType)
}

func selectProfile(model models.Model, env Envelope, nodes int) (models.FitProfile, bool) {
	var selected models.FitProfile
	found := false
	for _, profile := range model.FitProfiles {
		if !profileMatches(profile, env, nodes) || profile.RequiredPerNodeGB <= 0 {
			continue
		}
		// Prefer measured evidence. Within the same evidence class, choose the
		// lower reviewed requirement for the exact environment.
		if !found ||
			(profile.Evidence == "measured" && selected.Evidence != "measured") ||
			(profile.Evidence == selected.Evidence && profile.RequiredPerNodeGB < selected.RequiredPerNodeGB) {
			selected, found = profile, true
		}
	}
	return selected, found
}

// EstimateModel classifies an exact model artifact/runtime/topology profile
// against the current machine. If no reviewed profile matches, the answer is
// deliberately unknown: parameters, quantization labels and repository size do
// not prove loaded memory or engine compatibility.
func EstimateModel(model models.Model, env Envelope) Estimate {
	nodes := env.Nodes
	if nodes < 1 {
		nodes = 1
	}
	base := Estimate{
		Status: "unknown", MemoryType: env.MemoryType, Nodes: nodes,
		Sharded: env.Sharded, Engine: env.Engine, Architecture: env.Architecture,
		Platform: env.Platform, Evidence: "missing", Confidence: "unknown",
	}
	profile, ok := selectProfile(model, env, nodes)
	if !ok {
		base.Reason = "No reviewed fit profile matches this model artifact, inference engine, architecture, memory type, and node topology."
		return base
	}
	base.ProfileID = profile.ID
	base.Evidence = profile.Evidence
	base.EvidenceSource = profile.Source
	base.ContextK = profile.ContextK
	base.RequiredPerNodeGB = round1(profile.RequiredPerNodeGB)
	base.RequiredGB = round1(profile.RequiredPerNodeGB * float64(nodes))
	if profile.Evidence == "measured" {
		base.Confidence = "measured"
	} else {
		base.Confidence = "reviewed"
	}
	if env.MemoryGB <= 0 {
		base.Reason = "A reviewed fit profile exists, but this machine did not report usable accelerator memory."
		return base
	}
	reserved := env.ReservedGB
	if reserved <= 0 {
		reserved = defaultReserve(env.MemoryGB, env.MemoryType)
	}
	reserved = math.Min(env.MemoryGB, math.Max(0, reserved))
	available := env.AvailableGB
	if available <= 0 {
		available = env.MemoryGB
	}
	available = math.Min(env.MemoryGB, math.Max(0, available))
	base.TotalPerNodeGB = round1(env.MemoryGB)
	base.ReservedPerNodeGB = round1(reserved)
	base.AvailablePerNodeGB = round1(available)
	base.UsablePerNodeGB = round1(env.MemoryGB - reserved)
	base.HeadroomPerNodeGB = round1(base.UsablePerNodeGB - base.RequiredPerNodeGB)
	base.LaunchHeadroomPerNodeGB = round1(math.Max(0, available-reserved) - base.RequiredPerNodeGB)
	switch {
	case base.RequiredPerNodeGB <= base.UsablePerNodeGB*0.85:
		base.Status = "fits"
	case base.RequiredPerNodeGB <= base.UsablePerNodeGB:
		base.Status = "tight"
	default:
		base.Status = "over"
	}
	if profile.Evidence == "measured" {
		base.Reason = "Matched a measured Cloudless runtime profile for this exact engine, architecture, memory type, and topology."
	} else {
		base.Reason = "Matched a reviewed Cloudless runtime requirement for this exact engine, architecture, memory type, and topology; this is not a measured benchmark."
	}
	return base
}
