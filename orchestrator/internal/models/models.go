// Package models is the built-in FALLBACK catalog of LLMs the Model Manager offers
// (used when the hosted "Cloudless highlights" manifest is unreachable). Like the app
// catalog, curation is the reliability bet: every entry is vLLM-servable and
// (deliberately) NON-GATED on Hugging Face, so the engine can auto-download it without
// a license wall or token. VRAM figures are rough "total GPU memory to run it usefully
// on vLLM" estimates (weights + KV-cache headroom).
package models

// Model is a curated, servable LLM with display + capability metadata.
type Model struct {
	ID          string   `json:"id"`              // Hugging Face model id (what vLLM serves)
	Name        string   `json:"name"`            // display name
	Family      string   `json:"family"`          // "Qwen", "Phi", …
	Params      string   `json:"params"`          // "7B"
	Quant       string   `json:"quant,omitempty"` // "", "AWQ" (4-bit), …
	ContextK    int      `json:"contextK"`        // context window in K tokens
	MinVRAMGB   int      `json:"minVramGB"`       // rough total GPU VRAM to run it
	Use         string   `json:"use"`             // primary: "general" | "coding" | "vision"
	Tags        []string `json:"tags"`            // use-case tags for display
	ToolCalling bool     `json:"toolCalling"`     // supports function/tool calling
	Vision      bool     `json:"vision"`          // accepts image input
	License     string   `json:"license"`
	Description string   `json:"description"`
	Default     bool     `json:"default,omitempty"`
	Region      string   `json:"region,omitempty"` // ISO-3166 alpha-2 this model is recommended in (e.g. "FR"); "" = global
	Gated       bool     `json:"gated,omitempty"`  // HF repo requires accepting terms / a token before download
}

// RegionModels returns the curated models recommended for a given ISO country code.
func RegionModels(country string) []Model {
	out := []Model{}
	if country == "" {
		return out
	}
	for _, m := range curated {
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
		Description: "Full-precision Qwen3.6 MoE checkpoint; all 35B weights must be resident even though only 3B activate per token."},
	{ID: "deepseek-ai/DeepSeek-V4-Flash", Name: "DeepSeek V4 Flash", Family: "DeepSeek", Params: "284B / 13B active", Quant: "FP4 + FP8", ContextK: 1000, MinVRAMGB: 180,
		Use: "coding", Tags: []string{"coding", "reasoning", "agentic", "moe", "long-context"}, ToolCalling: true, License: "MIT",
		Description: "Official DeepSeek V4 Flash: 284B total, 13B active, mixed FP4/FP8 weights and up to a 1M-token context. Built for large multi-GPU or unified-memory systems."},

	{ID: "mistralai/Mistral-7B-Instruct-v0.3", Name: "Mistral 7B Instruct", Family: "Mistral", Params: "7B", ContextK: 32, MinVRAMGB: 18,
		Use: "general", Tags: []string{"chat", "multilingual", "french"}, ToolCalling: true, License: "Apache-2.0", Region: "FR", Gated: true,
		Description: "Mistral AI's open 7B — a strong, French-built assistant with excellent French. Made in France 🇫🇷."},
	{ID: "mistralai/Mistral-Nemo-Instruct-2407", Name: "Mistral Nemo 12B", Family: "Mistral", Params: "12B", ContextK: 128, MinVRAMGB: 28,
		Use: "general", Tags: []string{"chat", "multilingual", "french", "long-context"}, ToolCalling: true, License: "Apache-2.0", Region: "FR", Gated: true,
		Description: "Mistral's 12B with a 128K context and strong multilingual (incl. French) skills. Made in France 🇫🇷."},
}

// All returns the built-in fallback model catalog.
func All() []Model { return curated }

// Merge keeps hosted metadata authoritative for matching ids while ensuring a
// temporarily stale hosted manifest cannot hide newly verified built-ins.
func Merge(hosted []Model) []Model {
	out := append([]Model(nil), hosted...)
	seen := make(map[string]bool, len(out))
	for _, m := range out {
		seen[m.ID] = true
	}
	for _, m := range curated {
		if !seen[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// Get returns the model with the given id.
func Get(id string) (Model, bool) {
	for _, m := range curated {
		if m.ID == id {
			return m, true
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
