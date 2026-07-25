package catalog

import (
	"slices"
	"testing"

	"github.com/cloudless/orchestrator/internal/platform"
)

func TestDefaultModelIsPlatformSpecific(t *testing.T) {
	t.Setenv("CLOUDLESS_DEFAULT_MODEL", "")
	t.Setenv("CLOUDLESS_PLATFORM", platform.Generic)
	if got := DefaultModel(); got != defaultModelSentinel {
		t.Fatalf("generic default = %q, want %q", got, defaultModelSentinel)
	}

	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	if got := DefaultModel(); got != dgxSparkDefaultModel {
		t.Fatalf("Spark default = %q, want %q", got, dgxSparkDefaultModel)
	}
}

func TestDefaultModelOverrideRemainsAuthoritative(t *testing.T) {
	const custom = "example/custom-model"
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	t.Setenv("CLOUDLESS_DEFAULT_MODEL", custom)
	if got := DefaultModel(); got != custom {
		t.Fatalf("override = %q, want %q", got, custom)
	}
}

func TestSparkEngineSpecUsesSparkDefaultAndMemoryBudget(t *testing.T) {
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	t.Setenv("CLOUDLESS_DEFAULT_MODEL", "")
	vllm, ok := Get("vllm")
	if !ok {
		t.Fatal("vLLM unavailable on Spark")
	}
	spec := EngineSpec(vllm, "")
	if !slices.Contains(spec.Args, dgxSparkDefaultModel) {
		t.Fatalf("Spark engine args do not contain default model: %#v", spec.Args)
	}
	if !slices.Contains(spec.Args, "0.75") {
		t.Fatalf("Spark engine args do not contain Spark memory budget: %#v", spec.Args)
	}
	for _, want := range []string{"qwen3", "qwen3_coder"} {
		if !slices.Contains(spec.Args, want) {
			t.Fatalf("Spark engine args do not contain %q parser: %#v", want, spec.Args)
		}
	}
	if slices.Contains(spec.Args, defaultModelSentinel) {
		t.Fatalf("generic model sentinel leaked into Spark engine args: %#v", spec.Args)
	}

	sglang, ok := Get("sglang")
	if !ok {
		t.Fatal("SGLang unavailable on Spark")
	}
	sglangSpec := EngineSpec(sglang, "")
	for _, want := range []string{dgxSparkDefaultModel, "0.75", "qwen3", "qwen3_coder"} {
		if !slices.Contains(sglangSpec.Args, want) {
			t.Fatalf("Spark SGLang args do not contain %q: %#v", want, sglangSpec.Args)
		}
	}
}
