package assistant

import (
	"strings"
	"testing"
)

func modelTestContext() Context {
	return Context{
		GPU: "RTX 5090 (31 GB)",
		Models: []ModelOption{
			{ID: "Qwen/Qwen2.5-14B-Instruct-AWQ", Name: "Qwen2.5 14B (4-bit)", Params: "14B", Quant: "AWQ", MinVRAMGB: 13, Fit: "fits"},
			{ID: "Qwen/Qwen2.5-72B-Instruct-AWQ", Name: "Qwen2.5 72B (4-bit)", Params: "72B", Quant: "AWQ", MinVRAMGB: 44, Fit: "over"},
		},
	}
}

func TestModelGuidanceRejectsUnknownModelWorkflow(t *testing.T) {
	reply, handled := ModelGuidance("Would Qwen 3.6 work here?", modelTestContext())
	if !handled {
		t.Fatal("model compatibility question was not handled")
	}
	for _, want := range []string{"can’t verify", "Model Manager", "will not install models through Hermes skills"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply %q does not contain %q", reply, want)
		}
	}
}

func TestModelGuidanceUsesModelManagerFit(t *testing.T) {
	reply, handled := ModelGuidance("Would Qwen2.5 14B (4-bit) fit?", modelTestContext())
	if !handled || !strings.Contains(reply, "should fit this machine") || !strings.Contains(reply, "about 13 GB") {
		t.Fatalf("unexpected fit reply: handled=%v reply=%q", handled, reply)
	}

	reply, handled = ModelGuidance("Can I install Qwen2.5 72B (4-bit)?", modelTestContext())
	if !handled || !strings.Contains(reply, "more than this machine") {
		t.Fatalf("unexpected oversized reply: handled=%v reply=%q", handled, reply)
	}
}

func TestModelGuidanceLeavesOrdinaryChatToHermes(t *testing.T) {
	if reply, handled := ModelGuidance("Tell me why the sky is blue", modelTestContext()); handled || reply != "" {
		t.Fatalf("ordinary chat was intercepted: handled=%v reply=%q", handled, reply)
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
