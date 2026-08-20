// Package models is the built-in FALLBACK catalog of LLMs the Model Manager offers
// (used when the hosted "Cloudless highlights" manifest is unreachable). Like the app
// catalog, curation is the reliability bet: every entry is vLLM-servable and
// (deliberately) NON-GATED on Hugging Face, so the engine can auto-download it without
// a license wall or token. Compatibility is expressed through explicit runtime
// profiles. MinVRAMGB remains as display and migration metadata; it is never
// sufficient by itself to claim that an arbitrary runtime or topology will fit.
package models

// FitProfile is one reviewed runtime envelope for one model artifact. Profiles
// are exact about engine, architecture, memory type and node topology so the
// Model Manager never infers loaded memory from a parameter-count label.
type FitProfile struct {
	ID                string   `json:"id"`
	Evidence          string   `json:"evidence"` // reviewed-estimate | measured
	Source            string   `json:"source"`
	Engine            string   `json:"engine"`
	Architectures     []string `json:"architectures,omitempty"`
	Platforms         []string `json:"platforms,omitempty"`
	MemoryTypes       []string `json:"memoryTypes,omitempty"`
	MinNodes          int      `json:"minNodes"`
	MaxNodes          int      `json:"maxNodes"`
	Sharded           bool     `json:"sharded,omitempty"`
	ContextK          int      `json:"contextK"`
	RequiredPerNodeGB float64  `json:"requiredPerNodeGB"`
	// EstimatedTokensPerSecond is optional reviewed performance evidence for
	// this exact runtime/topology profile. A missing value is never inferred
	// from model size or quantization.
	EstimatedTokensPerSecond float64 `json:"estimatedTokensPerSecond,omitempty"`
	PerformanceEvidence      string  `json:"performanceEvidence,omitempty"`
}

// Model is a curated, servable LLM with display + capability metadata.
type Model struct {
	ID              string       `json:"id"`              // Hugging Face model id (what vLLM serves)
	Name            string       `json:"name"`            // display name
	Family          string       `json:"family"`          // "Qwen", "Phi", …
	Params          string       `json:"params"`          // "7B"
	Quant           string       `json:"quant,omitempty"` // "", "AWQ" (4-bit), …
	ContextK        int          `json:"contextK"`        // context window in K tokens
	MinVRAMGB       int          `json:"minVramGB"`       // rough total GPU VRAM to run it
	Use             string       `json:"use"`             // primary: "general" | "coding" | "vision"
	Tags            []string     `json:"tags"`            // use-case tags for display
	ToolCalling     bool         `json:"toolCalling"`     // supports function/tool calling
	Vision          bool         `json:"vision"`          // accepts image input
	License         string       `json:"license"`
	Description     string       `json:"description"`
	Default         bool         `json:"default,omitempty"`
	Region          string       `json:"region,omitempty"` // ISO-3166 alpha-2 this model is recommended in (e.g. "FR"); "" = global
	Gated           bool         `json:"gated,omitempty"`  // HF repo requires accepting terms / a token before download
	Source          string       `json:"source,omitempty"` // "huggingface" for user-imported Hub models
	SourceURL       string       `json:"sourceUrl,omitempty"`
	RuntimeStatus   string       `json:"runtimeStatus,omitempty"` // likely | unverified (curated models leave this empty)
	RuntimeNote     string       `json:"runtimeNote,omitempty"`
	Revision        string       `json:"revision,omitempty"`        // reviewed Hugging Face commit
	PreferredEngine string       `json:"preferredEngine,omitempty"` // managed engine selected for this model
	RuntimeImage    string       `json:"runtimeImage,omitempty"`    // model-specific multi-arch inference image
	RuntimeBuild    string       `json:"runtimeBuild,omitempty"`    // embedded build context for a local adapter image
	RuntimeEntry    string       `json:"runtimeEntry,omitempty"`    // optional container entrypoint override
	RuntimeCommand  []string     `json:"runtimeCommand,omitempty"`  // reviewed command replacing the engine default
	SingleNodeOnly  bool         `json:"singleNodeOnly,omitempty"`  // distributed Spark launch is not reviewed
	FitProfiles     []FitProfile `json:"fitProfiles,omitempty"`     // reviewed artifact/runtime/topology envelopes
	// QualityScore and FidelityScore are optional reviewed catalog evidence on
	// a 0-100 scale. Recommendation code treats zero as missing evidence rather
	// than manufacturing a score from parameter count or marketing labels.
	QualityScore    float64 `json:"qualityScore,omitempty"`
	QualityEvidence string  `json:"qualityEvidence,omitempty"`
	FidelityScore   float64 `json:"fidelityScore,omitempty"`
}

func normalized(m Model) Model {
	if len(m.FitProfiles) == 0 && m.MinVRAMGB > 0 {
		engine := m.PreferredEngine
		if engine == "" {
			engine = "vllm"
		}
		contextK := m.ContextK
		if contextK <= 0 || contextK > 32 {
			contextK = 32
		}
		m.FitProfiles = []FitProfile{{
			ID: "cloudless-single-v1", Evidence: "reviewed-estimate",
			Source: "Cloudless curated single-node runtime requirement",
			Engine: engine, Architectures: []string{"amd64", "arm64"},
			MemoryTypes: []string{"dedicated", "unified"},
			MinNodes:    1, MaxNodes: 1, ContextK: contextK,
			RequiredPerNodeGB: float64(m.MinVRAMGB),
		}}
	}
	// CloudlessOS 0.2.2 physically qualified its Spark default on one Spark
	// pair through the
	// managed vLLM/Ray path. vLLM reserves most of each Spark's unified memory,
	// so this profile records the observed per-node envelope instead of dividing
	// the parameter count or pretending aggregate memory is one pool. Larger
	// topologies remain unavailable until they are independently measured.
	if m.ID == "Qwen/Qwen3.6-35B-A3B" {
		hasCluster := false
		for _, profile := range m.FitProfiles {
			hasCluster = hasCluster || profile.Sharded
		}
		if !hasCluster {
			m.FitProfiles = append(m.FitProfiles, FitProfile{
				ID: "cloudless-spark-pair-0.2.2", Evidence: "measured",
				Source: "CloudlessOS 0.2.2 two-DGX-Spark qualification",
				Engine: "vllm", Architectures: []string{"arm64"},
				Platforms: []string{"dgx-spark"}, MemoryTypes: []string{"unified"},
				MinNodes: 2, MaxNodes: 2, Sharded: true, ContextK: 32,
				RequiredPerNodeGB: 100,
			})
		}
	}
	return m
}

// RegionModels returns the curated models recommended for a given ISO country code.
func RegionModels(country string) []Model {
	out := []Model{}
	if country == "" {
		return out
	}
	for _, m := range curated {
		m = normalized(m)
		if m.Region == country {
			out = append(out, m)
		}
	}
	return out
}

var curated = []Model{
	{ID: "Qwen/Qwen2.5-1.5B-Instruct", Name: "Qwen2.5 1.5B", Family: "Qwen", Params: "1.5B", ContextK: 32, MinVRAMGB: 5,
		Use: "general", Tags: []string{"chat", "fast"}, ToolCalling: true, License: "Apache-2.0", Default: true,
		Description: "Tiny and instant — a capable everyday assistant for low-VRAM machines."},
	{ID: "Qwen/Qwen2.5-3B-Instruct", Name: "Qwen2.5 3B", Family: "Qwen", Params: "3B", ContextK: 32, MinVRAMGB: 9,
		Use: "general", Tags: []string{"chat", "fast"}, ToolCalling: true, License: "Qwen",
		Description: "Small and fast, noticeably smarter than 1.5B."},
	{ID: "microsoft/Phi-3.5-mini-instruct", Name: "Phi-3.5 mini", Family: "Phi", Params: "3.8B", ContextK: 128, MinVRAMGB: 11,
		Use: "general", Tags: []string{"chat", "reasoning", "long-context"}, ToolCalling: true, License: "MIT",
		Description: "Strong small model with a long 128K context — great for documents."},
	{ID: "Qwen/Qwen2.5-7B-Instruct", Name: "Qwen2.5 7B", Family: "Qwen", Params: "7B", ContextK: 32, MinVRAMGB: 18,
		Use: "general", Tags: []string{"chat", "reasoning", "multilingual"}, ToolCalling: true, License: "Apache-2.0",
		Description: "Excellent all-rounder — the sweet spot for a 24GB+ GPU."},
	{ID: "Qwen/Qwen2.5-14B-Instruct-AWQ", Name: "Qwen2.5 14B (4-bit)", Family: "Qwen", Params: "14B", Quant: "AWQ", ContextK: 32, MinVRAMGB: 13,
		Use: "general", Tags: []string{"chat", "reasoning"}, ToolCalling: true, License: "Apache-2.0",
		Description: "14B quality at roughly half the VRAM, via 4-bit AWQ."},
	{ID: "Qwen/Qwen2.5-32B-Instruct-AWQ", Name: "Qwen2.5 32B (4-bit)", Family: "Qwen", Params: "32B", Quant: "AWQ", ContextK: 32, MinVRAMGB: 22,
		Use: "general", Tags: []string{"chat", "reasoning", "multilingual"}, ToolCalling: true, License: "Apache-2.0",
		Description: "Big-model quality in 4-bit — fits a single 24–32GB GPU."},
	{ID: "Qwen/Qwen2.5-Coder-7B-Instruct", Name: "Qwen2.5 Coder 7B", Family: "Qwen", Params: "7B", ContextK: 32, MinVRAMGB: 18,
		Use: "coding", Tags: []string{"coding", "tools"}, ToolCalling: true, License: "Apache-2.0",
		Description: "Coding-tuned 7B; pairs well with OpenClaw."},
	{ID: "Qwen/Qwen2.5-Coder-32B-Instruct-AWQ", Name: "Qwen2.5 Coder 32B (4-bit)", Family: "Qwen", Params: "32B", Quant: "AWQ", ContextK: 32, MinVRAMGB: 22,
		Use: "coding", Tags: []string{"coding", "agentic"}, ToolCalling: true, License: "Apache-2.0",
		Description: "Top open coding model, 4-bit — fits 24–32GB."},
	{ID: "Qwen/Qwen2.5-VL-7B-Instruct", Name: "Qwen2.5-VL 7B", Family: "Qwen", Params: "7B", ContextK: 32, MinVRAMGB: 20,
		Use: "vision", Tags: []string{"vision", "chat"}, Vision: true, License: "Apache-2.0",
		Description: "Sees images: ask about photos, screenshots, charts and documents."},
	{ID: "Qwen/Qwen2.5-72B-Instruct-AWQ", Name: "Qwen2.5 72B (4-bit)", Family: "Qwen", Params: "72B", Quant: "AWQ", ContextK: 32, MinVRAMGB: 44,
		Use: "general", Tags: []string{"chat", "reasoning", "flagship"}, ToolCalling: true, License: "Apache-2.0",
		Description: "Flagship quality. Needs ~48GB — a big card or multiple GPUs."},

	// Mistral — French-built open models, surfaced as "recommended" on machines in France.
	// Both are Apache-2.0 but their Hugging Face repos are gated (a free token / accepting
	// terms is required before download), hence Gated: true.
	// Current official Qwen3.6 releases. These are multimodal checkpoints; the
	// FP8 variants reduce weight memory but still need useful runtime headroom.
	{ID: "Qwen/Qwen3.6-27B-FP8", Name: "Qwen3.6 27B (FP8)", Family: "Qwen", Params: "27B", Quant: "FP8", ContextK: 262, MinVRAMGB: 34,
		Use: "general", Tags: []string{"chat", "reasoning", "vision", "agentic"}, ToolCalling: true, Vision: true, License: "Apache-2.0",
		Description: "Qwen3.6's dense multimodal model in official FP8; strong general reasoning and coding, but tight on a 32GB GPU."},
	{ID: "Qwen/Qwen3.6-35B-A3B-FP8", Name: "Qwen3.6 35B-A3B (FP8)", Family: "Qwen", Params: "35B / 3B active", Quant: "FP8", ContextK: 262, MinVRAMGB: 43,
		Use: "coding", Tags: []string{"coding", "reasoning", "vision", "agentic", "moe"}, ToolCalling: true, Vision: true, License: "Apache-2.0",
		Description: "Official FP8 Qwen3.6 MoE: 35B total, 3B active per token, tuned for agentic coding and multimodal work."},
	{ID: "Qwen/Qwen3.6-27B", Name: "Qwen3.6 27B", Family: "Qwen", Params: "27B", ContextK: 262, MinVRAMGB: 64,
		Use: "general", Tags: []string{"chat", "reasoning", "vision", "agentic"}, ToolCalling: true, Vision: true, License: "Apache-2.0",
		Description: "Full-precision Qwen3.6 27B multimodal checkpoint for larger multi-GPU systems."},
	{ID: "Qwen/Qwen3.6-35B-A3B", Name: "Qwen3.6 35B-A3B", Family: "Qwen", Params: "35B / 3B active", ContextK: 262, MinVRAMGB: 81,
		Use: "coding", Tags: []string{"coding", "reasoning", "vision", "agentic", "moe"}, ToolCalling: true, Vision: true, License: "Apache-2.0",
		Revision:    "995ad96eacd98c81ed38be0c5b274b04031597b0",
		Description: "Full-precision Qwen3.6 MoE checkpoint; all 35B weights must be resident even though only 3B activate per token."},
	{ID: "deepseek-ai/DeepSeek-V4-Flash", Name: "DeepSeek V4 Flash", Family: "DeepSeek", Params: "284B / 13B active", Quant: "FP4 + FP8", ContextK: 1000, MinVRAMGB: 180,
		Use: "coding", Tags: []string{"coding", "reasoning", "agentic", "moe", "long-context"}, ToolCalling: true, License: "MIT",
		Description: "Official DeepSeek V4 Flash: 284B total, 13B active, mixed FP4/FP8 weights and up to a 1M-token context. Built for large multi-GPU or unified-memory systems."},
	{ID: "nvidia/Cosmos3-Edge", Name: "NVIDIA Cosmos3-Edge", Family: "Cosmos", Params: "4B", ContextK: 131, MinVRAMGB: 16,
		Use: "vision", Tags: []string{"vision", "video", "physical-ai", "robotics", "reasoning"}, Vision: true, License: "OpenMDW-1.1",
		Description: "Compact NVIDIA omnimodal world model for visual understanding and physical-AI reasoning on edge hardware.",
		Revision:    "ff48d22144de52de296a7b4d3a78914831007212", PreferredEngine: "vllm",
		RuntimeImage:   "vllm/vllm-openai:cosmos3@sha256:db0bb920b0b54e82ea96a98659bbd21921f87d0dcfc86feffdafa2db3f08be55",
		RuntimeCommand: []string{"nvidia/Cosmos3-Edge", "--revision", "ff48d22144de52de296a7b4d3a78914831007212", "--served-model-name", "cloudless", "--host", "0.0.0.0", "--port", "8000", "--gpu-memory-utilization", "0.75", "--max-model-len", "131072", "--allowed-local-media-path", "/", "--mm-processor-kwargs", `{"do_resize":true,"min_pixels":4096,"max_pixels":16777216}`, "--media-io-kwargs", `{"video":{"num_frames":256}}`},
		RuntimeNote:    "Cloudless serves the Cosmos3 reasoner with NVIDIA's reviewed vLLM image and launch settings. Generative video and action endpoints require the separate vLLM-Omni runtime. Robotics and safety-critical uses still require application-specific validation and guardrails.", SingleNodeOnly: true},
	{ID: "nvidia/LocateAnything-3B", Name: "NVIDIA LocateAnything 3B", Family: "LocateAnything", Params: "3B", ContextK: 25, MinVRAMGB: 16,
		Use: "vision", Tags: []string{"vision", "grounding", "object-detection", "gui", "localization"}, Vision: true, License: "NVIDIA License",
		Description: "Vision-language grounding model that locates objects, text, and interface elements and returns precise coordinates.",
		Revision:    "c32291ca5e996f5a7a485845b4f57a233936bba0", PreferredEngine: "vllm",
		RuntimeImage: "cloudless/locateanything-runtime:0.1.0", RuntimeBuild: "locateanything", RuntimeEntry: "python3",
		RuntimeCommand: []string{"/opt/cloudless/server.py", "--model", "nvidia/LocateAnything-3B", "--revision", "c32291ca5e996f5a7a485845b4f57a233936bba0", "--served-model-name", "cloudless", "--host", "0.0.0.0", "--port", "8000"},
		RuntimeNote:    "Cloudless wraps NVIDIA's supported Transformers worker in a pinned local OpenAI-compatible adapter. Hybrid visual grounding is enabled by default; this NVIDIA-licensed checkpoint is intended for research and development use.", SingleNodeOnly: true},

	{ID: "mistralai/Mistral-7B-Instruct-v0.3", Name: "Mistral 7B Instruct", Family: "Mistral", Params: "7B", ContextK: 32, MinVRAMGB: 18,
		Use: "general", Tags: []string{"chat", "multilingual", "french"}, ToolCalling: true, License: "Apache-2.0", Region: "FR", Gated: true,
		Description: "Mistral AI's open 7B — a strong, French-built assistant with excellent French. Made in France 🇫🇷."},
	{ID: "mistralai/Mistral-Nemo-Instruct-2407", Name: "Mistral Nemo 12B", Family: "Mistral", Params: "12B", ContextK: 128, MinVRAMGB: 28,
		Use: "general", Tags: []string{"chat", "multilingual", "french", "long-context"}, ToolCalling: true, License: "Apache-2.0", Region: "FR", Gated: true,
		Description: "Mistral's 12B with a 128K context and strong multilingual (incl. French) skills. Made in France 🇫🇷."},
}

// All returns the built-in fallback model catalog.
func All() []Model {
	out := make([]Model, 0, len(curated))
	for _, model := range curated {
		out = append(out, normalized(model))
	}
	return out
}

// Merge keeps hosted metadata authoritative for matching ids while ensuring a
// temporarily stale hosted manifest cannot hide newly verified built-ins.
func Merge(hosted []Model) []Model {
	out := append([]Model(nil), hosted...)
	seen := make(map[string]int, len(out))
	for i, m := range out {
		seen[m.ID] = i
	}
	for _, m := range curated {
		m = normalized(m)
		if index, ok := seen[m.ID]; ok {
			// Hosted display metadata may evolve independently, but a stale hosted
			// manifest must never erase the reviewed runtime contract compiled into
			// this Cloudless release.
			if m.RuntimeImage != "" {
				out[index].Revision = m.Revision
				out[index].PreferredEngine = m.PreferredEngine
				out[index].RuntimeImage = m.RuntimeImage
				out[index].RuntimeBuild = m.RuntimeBuild
				out[index].RuntimeEntry = m.RuntimeEntry
				out[index].RuntimeCommand = append([]string(nil), m.RuntimeCommand...)
				out[index].SingleNodeOnly = m.SingleNodeOnly
				if out[index].RuntimeNote == "" {
					out[index].RuntimeNote = m.RuntimeNote
				}
			}
			// A hosted display manifest may lag the package that knows how to run
			// the model. Never let it erase a runtime-fit contract qualified by
			// this exact CloudlessOS build.
			out[index].FitProfiles = append([]FitProfile(nil), m.FitProfiles...)
		} else {
			out = append(out, m)
		}
	}
	return out
}

// Get returns the model with the given id.
func Get(id string) (Model, bool) {
	for _, m := range curated {
		if m.ID == id {
			return normalized(m), true
		}
	}
	return Model{}, false
}

// Default returns the default model id.
func Default() string {
	for _, m := range curated {
		if m.Default {
			return m.ID
		}
	}
	return ""
}
