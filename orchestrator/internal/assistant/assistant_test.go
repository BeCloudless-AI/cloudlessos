package assistant

import (
	"strings"
	"testing"
)

func modelTestContext() Context {
	return Context{
		GPU:       "RTX 5090 (31 GB)",
		GPUVRAMGB: 31,
		Models: []ModelOption{
			{ID: "Qwen/Qwen2.5-14B-Instruct-AWQ", Name: "Qwen2.5 14B (4-bit)", Params: "14B", Quant: "AWQ", MinVRAMGB: 13, Fit: "fits"},
			{ID: "Qwen/Qwen2.5-72B-Instruct-AWQ", Name: "Qwen2.5 72B (4-bit)", Params: "72B", Quant: "AWQ", MinVRAMGB: 44, Fit: "over"},
		},
	}
}

func TestModelGuidanceRejectsUnknownModelWorkflow(t *testing.T) {
	advice, handled := ModelGuidance("Would Qwen 3.6 work here?", modelTestContext())
	if !handled {
		t.Fatal("model compatibility question was not handled")
	}
	for _, want := range []string{"don’t have enough detail", "exact Hugging Face repository", "Model Manager"} {
		if !strings.Contains(advice.Reply, want) {
			t.Fatalf("reply %q does not contain %q", advice.Reply, want)
		}
	}
}

func TestModelGuidanceCatchesNaturalHardwareQuestions(t *testing.T) {
	c := modelTestContext()
	advice, handled := ModelGuidance("Does Qwen 3.6 run on this system?", c)
	if !handled || !strings.Contains(advice.Reply, "don’t have enough detail") {
		t.Fatalf("natural run question escaped deterministic guidance: handled=%v advice=%#v", handled, advice)
	}

	advice, handled = ModelGuidance("Qwen 3.6 35B needs 9 GB of VRAM? What?", c)
	if !handled {
		t.Fatal("VRAM follow-up escaped deterministic guidance")
	}
	for _, want := range []string{"35.0B", "16-bit", "about 81 GB", "unlikely to fit"} {
		if !strings.Contains(advice.Reply, want) {
			t.Fatalf("hardware reply %q does not contain %q", advice.Reply, want)
		}
	}
}

func TestModelGuidanceRecognizesFP8(t *testing.T) {
	advice, handled := ModelGuidance("Will Qwen/Qwen3.6-35B-A3B-FP8 run locally?", modelTestContext())
	if !handled || advice.ModelID != "Qwen/Qwen3.6-35B-A3B-FP8" {
		t.Fatalf("FP8 model destination lost: handled=%v advice=%#v", handled, advice)
	}
	for _, want := range []string{"35.0B", "8-bit", "about 43 GB", "unlikely to fit"} {
		if !strings.Contains(advice.Reply, want) {
			t.Fatalf("FP8 reply %q does not contain %q", advice.Reply, want)
		}
	}
}

func TestModelGuidanceUsesModelManagerFit(t *testing.T) {
	advice, handled := ModelGuidance("Would Qwen2.5 14B (4-bit) fit?", modelTestContext())
	if !handled || !strings.Contains(advice.Reply, "should fit this machine") || !strings.Contains(advice.Reply, "about 13 GB") || advice.ModelID != "Qwen/Qwen2.5-14B-Instruct-AWQ" {
		t.Fatalf("unexpected fit advice: handled=%v advice=%#v", handled, advice)
	}

	advice, handled = ModelGuidance("Can I install Qwen2.5 72B (4-bit)?", modelTestContext())
	if !handled || !strings.Contains(advice.Reply, "more than this machine") || !strings.Contains(advice.Label, "installation") {
		t.Fatalf("unexpected oversized advice: handled=%v advice=%#v", handled, advice)
	}
}

func TestModelGuidanceEstimatesCustomModelAndPreservesRepo(t *testing.T) {
	advice, handled := ModelGuidance("Can I install acme/Qwen3-32B-AWQ? It is a 32B AWQ model.", modelTestContext())
	if !handled || advice.ModelID != "acme/Qwen3-32B-AWQ" {
		t.Fatalf("custom model destination lost: handled=%v advice=%#v", handled, advice)
	}
	for _, want := range []string{"32.0B", "4-bit", "about 22 GB", "should fit"} {
		if !strings.Contains(advice.Reply, want) {
			t.Fatalf("estimate %q does not contain %q", advice.Reply, want)
		}
	}
}

func TestModelGuidanceExplainsMissingGPUForKnownCustomSize(t *testing.T) {
	c := modelTestContext()
	c.GPU = "no NVIDIA GPU detected"
	c.GPUVRAMGB = 0
	advice, handled := ModelGuidance("Can I install acme/Qwen3-32B-AWQ? It is a 32B AWQ model.", c)
	if !handled || advice.ModelID != "acme/Qwen3-32B-AWQ" {
		t.Fatalf("custom model destination lost: handled=%v advice=%#v", handled, advice)
	}
	for _, want := range []string{"about 22 GB", "no NVIDIA GPU detected", "cannot run this model"} {
		if !strings.Contains(advice.Reply, want) {
			t.Fatalf("missing-GPU reply %q does not contain %q", advice.Reply, want)
		}
	}
}

func TestModelGuidanceExplainsMissingGPUForCatalogModel(t *testing.T) {
	c := modelTestContext()
	c.GPU = "no NVIDIA GPU detected"
	c.GPUVRAMGB = 0
	advice, handled := ModelGuidance("Can I install Qwen2.5 14B (4-bit)?", c)
	if !handled || advice.ModelID != "Qwen/Qwen2.5-14B-Instruct-AWQ" {
		t.Fatalf("catalog destination lost: handled=%v advice=%#v", handled, advice)
	}
	for _, want := range []string{"about 13 GB", "no NVIDIA GPU detected", "cannot run this model"} {
		if !strings.Contains(advice.Reply, want) {
			t.Fatalf("missing-GPU reply %q does not contain %q", advice.Reply, want)
		}
	}
}

func TestModelGuidanceLeavesOrdinaryChatToHermes(t *testing.T) {
	if advice, handled := ModelGuidance("Tell me why the sky is blue", modelTestContext()); handled || advice.Reply != "" {
		t.Fatalf("ordinary chat was intercepted: handled=%v advice=%#v", handled, advice)
	}
}

func TestModelGuidanceDescribesDGXUnifiedMemory(t *testing.T) {
	c := modelTestContext()
	c.GPU = "GB10 (121 GB unified memory)"
	c.GPUVRAMGB = 121
	c.GPUMemoryType = "unified"
	advice, handled := ModelGuidance("Would Qwen2.5 72B (4-bit) fit?", c)
	if !handled || !strings.Contains(advice.Reply, "unified memory") || strings.Contains(advice.Reply, "of VRAM") {
		t.Fatalf("DGX memory guidance = %q", advice.Reply)
	}
}

func TestSystemPromptMakesModelManagerAuthoritative(t *testing.T) {
	prompt := SystemPrompt(modelTestContext())
	for _, want := range []string{"Model Manager is the only authority", "Never create or invoke a Hermes skill", "[[do:models]]"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	actions := Suggest("Use the supported flow.\n[[do:models]]", "switch model", modelTestContext())
	if len(actions) != 1 || actions[0].Kind != "models" || actions[0].Label != "Open Model Manager" {
		t.Fatalf("model manager action = %#v", actions)
	}
}

func TestSuggestRejectsIrrelevantModelAndAppTags(t *testing.T) {
	actions := Suggest("Ready.\n[[do:models]]\n[[do:open:open-webui]]", "Reply with Ready and a two-item bullet list.", modelTestContext())
	if len(actions) != 0 {
		t.Fatalf("irrelevant actions leaked into ordinary chat: %#v", actions)
	}
}

func TestSuggestKeepsRelevantAppRecommendation(t *testing.T) {
	actions := Suggest("ComfyUI is a good fit.\n[[do:install:comfyui]]", "Which app should I use to build images?", modelTestContext())
	if len(actions) != 1 || actions[0].ID != "comfyui" {
		t.Fatalf("relevant app action was discarded: %#v", actions)
	}
}
