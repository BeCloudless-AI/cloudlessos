package state

import "testing"

func TestCustomEngineValidationPreservesImmutableProfile(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	def := CustomEngine{
		ID: "custom-sm121", Name: "SM121 vLLM", Image: "cloudless/vllm-sm121:dev",
		ResolvedImage: "sha256:abc", ImageDigest: "sha256:abc", Architecture: "arm64",
		Base: "vllm", ProfileVersion: 1, ContractVersion: "cloudless-openai-v1",
		ValidationStatus: "registered",
	}
	if err := store.UpsertCustomEngine(def); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCustomEngineValidation(def.ID, "compatible", ""); err != nil {
		t.Fatal(err)
	}
	got := store.CustomEngineList()[0]
	if got.ResolvedImage != def.ResolvedImage || got.ImageDigest != def.ImageDigest ||
		got.ProfileVersion != 1 || got.ContractVersion != def.ContractVersion ||
		got.ValidationStatus != "compatible" || got.LastValidated == "" || got.LastError != "" {
		t.Fatalf("unexpected custom engine profile: %#v", got)
	}
}
