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
