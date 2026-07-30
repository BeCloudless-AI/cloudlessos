package models

import "testing"

func TestMergePreservesHostedEntriesAndAddsNewBuiltins(t *testing.T) {
	hosted := []Model{{ID: "Qwen/Qwen2.5-1.5B-Instruct", Name: "Hosted override"}, {ID: "example/custom", Name: "Custom"}}
	got := Merge(hosted)
	if len(got) <= len(hosted) || got[0].Name != "Hosted override" || got[1].ID != "example/custom" {
		t.Fatalf("hosted catalog was not preserved: %#v", got)
	}
	want := map[string]bool{"Qwen/Qwen3.6-27B-FP8": false, "Qwen/Qwen3.6-35B-A3B-FP8": false, "deepseek-ai/DeepSeek-V4-Flash": false}
	for _, m := range got {
		if _, ok := want[m.ID]; ok {
			want[m.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("merged catalog missing %s", id)
		}
	}
}

func TestReviewedNVIDIAModelRuntimesArePinned(t *testing.T) {
	for _, id := range []string{"nvidia/Cosmos3-Edge", "nvidia/LocateAnything-3B"} {
		model, ok := Get(id)
		if !ok {
			t.Fatalf("missing reviewed model %s", id)
		}
		if model.Revision == "" || model.RuntimeImage == "" || model.PreferredEngine != "vllm" || len(model.RuntimeCommand) == 0 {
			t.Fatalf("%s has an incomplete runtime contract: %#v", id, model)
		}
		if !model.SingleNodeOnly {
			t.Fatalf("%s must remain single-node until its distributed runtime is reviewed", id)
		}
		if id == "nvidia/LocateAnything-3B" && (model.RuntimeBuild != "locateanything" || model.RuntimeEntry != "python3") {
			t.Fatalf("LocateAnything is not wired to its Transformers adapter: %#v", model)
		}
	}
}

func TestMergeCannotEraseReviewedRuntimeContract(t *testing.T) {
	hosted := []Model{{ID: "nvidia/Cosmos3-Edge", Name: "Hosted display name"}}
	got := Merge(hosted)
	if len(got) == 0 || got[0].Name != "Hosted display name" {
		t.Fatalf("hosted display metadata was not preserved: %#v", got)
	}
	builtin, _ := Get("nvidia/Cosmos3-Edge")
	if got[0].RuntimeImage != builtin.RuntimeImage || got[0].Revision != builtin.Revision || !got[0].SingleNodeOnly {
		t.Fatalf("reviewed runtime contract was erased: %#v", got[0])
	}
}
