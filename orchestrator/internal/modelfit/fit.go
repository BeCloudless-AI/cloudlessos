// Package modelfit estimates the actual runtime envelope of a model instead of
// comparing one hand-entered VRAM number with total device memory.
package modelfit

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudless/orchestrator/internal/models"
)

type Envelope struct {
	MemoryGB   float64 `json:"memoryGB"`
	MemoryType string  `json:"memoryType"` // discrete | unified
	Nodes      int     `json:"nodes"`
	Sharded    bool    `json:"sharded"`
}

type Estimate struct {
	Status              string  `json:"status"` // fits | tight | over | unknown
	MemoryType          string  `json:"memoryType"`
	Nodes               int     `json:"nodes"`
	Sharded             bool    `json:"sharded"`
	QuantizationBits    float64 `json:"quantizationBits,omitempty"`
	ParameterBillions   float64 `json:"parameterBillions,omitempty"`
	ContextK            int     `json:"contextK,omitempty"`
	RecommendedContextK int     `json:"recommendedContextK,omitempty"`
	WeightsGB           float64 `json:"weightsGB,omitempty"`
	KVCacheGB           float64 `json:"kvCacheGB,omitempty"`
	RuntimeGB           float64 `json:"runtimeGB,omitempty"`
	RequiredGB          float64 `json:"requiredGB,omitempty"`
	RequiredPerNodeGB   float64 `json:"requiredPerNodeGB,omitempty"`
	UsablePerNodeGB     float64 `json:"usablePerNodeGB,omitempty"`
	HeadroomPerNodeGB   float64 `json:"headroomPerNodeGB,omitempty"`
	Confidence          string  `json:"confidence"`
	Reason              string  `json:"reason"`
}

var paramsPattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*B`)

func parameterBillions(label string) float64 {
	m := paramsPattern.FindStringSubmatch(label)
	if len(m) != 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

func quantizationBits(quant string) float64 {
	q := strings.ToLower(strings.ReplaceAll(quant, "-", ""))
	switch {
	case strings.Contains(q, "fp4") && strings.Contains(q, "fp8"):
		return 5
	case strings.Contains(q, "awq"), strings.Contains(q, "gptq"), strings.Contains(q, "4bit"), strings.Contains(q, "q4"), strings.Contains(q, "fp4"):
		return 4
	case strings.Contains(q, "q5"):
		return 5
	case strings.Contains(q, "q6"):
		return 6
	case strings.Contains(q, "fp8"), strings.Contains(q, "int8"), strings.Contains(q, "8bit"):
		return 8
	default:
		return 16
	}
}

func usableMemory(total float64, kind string) float64 {
	if total <= 0 {
		return 0
	}
	if strings.EqualFold(kind, "unified") {
		// Leave room for Ubuntu, the compositor, Docker, filesystem cache and
		// Cloudless services. On 128 GB Spark this exposes roughly 105 GB.
		return math.Max(2, math.Min(total*0.82, total-16))
	}
	return total * 0.90
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// EstimateModel evaluates weights, KV cache, runtime overhead and topology at
// Cloudless's normal 32K serving context. The full model context is retained as
// an upper bound and a safe context recommendation is returned when necessary.
func EstimateModel(model models.Model, env Envelope) Estimate {
	nodes := env.Nodes
	if nodes < 1 {
		nodes = 1
	}
	params := parameterBillions(model.Params)
	if params == 0 || env.MemoryGB <= 0 {
		return Estimate{Status: "unknown", MemoryType: env.MemoryType, Nodes: nodes,
			Sharded: env.Sharded, Confidence: "low", Reason: "Model parameters or accelerator memory are unknown."}
	}
	bits := quantizationBits(model.Quant)
	contextK := model.ContextK
	if contextK <= 0 || contextK > 32 {
		contextK = 32
	}
	weights := params * 1e9 * bits / 8 / float64(uint64(1)<<30)
	// This conservative GQA-neutral approximation is intentionally catalog
	// independent. Runtime profiles can replace it once exact layer metadata is
	// available from config.json.
	kv := math.Max(0.8, params*(float64(contextK)/32)*0.055)
	runtime := math.Max(2, weights*0.08+1.5)
	shards := 1
	if env.Sharded && nodes > 1 {
		shards = nodes
	}
	requiredPerNode := (weights+kv)/float64(shards) + runtime
	usable := usableMemory(env.MemoryGB, env.MemoryType)
	headroom := usable - requiredPerNode
	status := "over"
	switch {
	case requiredPerNode <= usable*0.85:
		status = "fits"
	case requiredPerNode <= usable:
		status = "tight"
	}
	maxContext := model.ContextK
	if maxContext <= 0 {
		maxContext = 32
	}
	availableKV := (usable-runtime)*float64(shards) - weights
	recommended := 0
	if availableKV > 0 {
		perK := params * 0.055 / 32
		recommended = int(math.Floor(availableKV / perK))
		for _, step := range []int{8, 16, 32, 64, 128, 256, 512, 1024} {
			if recommended >= step {
				recommended = step
			}
		}
		if recommended > maxContext {
			recommended = maxContext
		}
	}
	reason := "Estimated from parameter count, quantization, a 32K KV cache, runtime overhead, and reserved system memory."
	if env.Sharded && nodes > 1 {
		reason = "Weights and KV cache are estimated as tensor-sharded; runtime overhead remains resident on every node."
	}
	return Estimate{
		Status: status, MemoryType: env.MemoryType, Nodes: nodes, Sharded: env.Sharded,
		QuantizationBits: bits, ParameterBillions: params, ContextK: contextK,
		RecommendedContextK: recommended, WeightsGB: round1(weights), KVCacheGB: round1(kv),
		RuntimeGB: round1(runtime), RequiredGB: round1(weights + kv + runtime*float64(nodes)),
		RequiredPerNodeGB: round1(requiredPerNode), UsablePerNodeGB: round1(usable),
		HeadroomPerNodeGB: round1(headroom), Confidence: "estimated", Reason: reason,
	}
}
