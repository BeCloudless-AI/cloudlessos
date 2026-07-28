package localrecipes

import (
	"strings"
	"testing"
)

const minimalSparkRun = `
model: Qwen/Qwen3-1.7B
model_revision: abc123
runtime: vllm
container: ghcr.io/example/vllm:1
min_nodes: 1
max_nodes: 2
metadata:
  description: Small Qwen test recipe
  model_dtype: fp8
defaults:
  port: 8123
  host: 0.0.0.0
  tensor_parallel: 2
  gpu_memory_utilization: 0.75
  max_model_len: 65536
  served_model_name: qwen-test
env:
  VLLM_ALLOW_LONG_MAX_MODEL_LEN: "1"
command: |
  vllm serve {model} --host {host} --port {port} --tensor-parallel-size {tensor_parallel}
`

func TestParseSparkRunMapsPortableSchema(t *testing.T) {
	preview, err := ParseSparkRun([]byte(minimalSparkRun), "https://example.com/qwen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Compatible || preview.Draft.Runtime.Adapter != SparkRunAdapter {
		t.Fatalf("preview = %#v", preview)
	}
	d := preview.Draft
	if d.Engine.Type != "vllm" || d.Engine.Image != "ghcr.io/example/vllm:1" || d.Engine.ContainerPort != 8123 {
		t.Fatalf("engine = %#v", d.Engine)
	}
	if d.Model.ID != "Qwen/Qwen3-1.7B" || d.Model.TensorParallel != 2 || d.Distributed.Nodes != 2 {
		t.Fatalf("model/topology = %#v %#v", d.Model, d.Distributed)
	}
	if _, err := validateDraft(d); err != nil {
		t.Fatalf("mapped draft does not validate: %v", err)
	}
}

func TestParseSparkRunPreservesRuntimeExtensionsForProvider(t *testing.T) {
	preview, err := ParseSparkRun([]byte(minimalSparkRun+"\nmods: [qwen-tools]\n"), "https://example.com/qwen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Compatible || len(preview.Risks) != 1 || !strings.Contains(preview.Risks[0], "modifications") {
		t.Fatalf("provider extension risk not reported: %#v", preview)
	}
	if preview.Draft.Runtime.SparkRun == nil || !strings.Contains(preview.Draft.Runtime.SparkRun.Document, "mods: [qwen-tools]") {
		t.Fatalf("original document was not preserved: %#v", preview.Draft.Runtime.SparkRun)
	}
}

func TestCreateImportedSparkRunPersistsOriginAndTrust(t *testing.T) {
	preview, err := ParseSparkRun([]byte(minimalSparkRun), "https://example.com/qwen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	store := New(t.TempDir())
	recipe, err := store.CreateImported(preview.Draft, "sparkrun")
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Origin != "sparkrun" || recipe.Trust != "external-unreviewed" || recipe.Runtime.SparkRun == nil {
		t.Fatalf("recipe = %#v", recipe)
	}
}

func TestParseSparkRunDelegatesMultiNodeRayUnchanged(t *testing.T) {
	yaml := strings.Replace(minimalSparkRun, "tensor_parallel: 2", "tensor_parallel: 2\n  distributed_executor_backend: ray", 1)
	preview, err := ParseSparkRun([]byte(yaml), "https://example.com/qwen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Compatible || preview.Draft.Runtime.SparkRun.OriginalRuntime != "vllm-ray" || !strings.Contains(preview.Draft.Runtime.SparkRun.Document, "distributed_executor_backend: ray") {
		t.Fatalf("multi-node Ray was not delegated unchanged: %#v", preview)
	}
}

func TestParseSparkRunPreservesAdvancedTripleSparkRecipe(t *testing.T) {
	yaml := `recipe_version: "2"
name: GLM test
model: example/glm
model_revision: immutable-revision
runtime: vllm-ray
container: ghcr.io/example/glm@sha256:abcdef
min_nodes: 3
max_nodes: 3
executor_config:
  privileged: true
  network: host
distribution_config:
  models:
    enabled: true
pre_exec:
  - echo prepare
defaults:
  tensor_parallel: 3
  port: 8000
command: vllm serve {model} --tensor-parallel-size {tensor_parallel}
`
	preview, err := ParseSparkRun([]byte(yaml), "https://example.com/glm.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Compatible || preview.Draft.Distributed.Nodes != 3 || preview.Draft.Runtime.SparkRun == nil {
		t.Fatalf("preview = %#v", preview)
	}
	for _, field := range []string{"executor_config:", "distribution_config:", "pre_exec:"} {
		if !strings.Contains(preview.Draft.Runtime.SparkRun.Document, field) {
			t.Fatalf("original recipe lost %s", field)
		}
	}
	if len(preview.Risks) < 3 {
		t.Fatalf("advanced execution risks were not disclosed: %#v", preview.Risks)
	}
}

func TestParseSparkRunAcceptsRegistryStyleDeepSeekV4Recipe(t *testing.T) {
	yaml := `recipe_version: "2"
model: deepseek-ai/DeepSeek-V4-Flash
runtime: vllm
container: scitrera/dgx-spark-vllm-jasl-ds4:20260609
min_nodes: 2
defaults:
  port: 8000
  host: 0.0.0.0
  tensor_parallel: 2
  gpu_memory_utilization: 0.825
  max_model_len: 256000
  max_num_batched_tokens: 6144
  compilation_config: '{"cudagraph_mode":"FULL_AND_PIECEWISE"}'
command: |
  vllm serve {model} --host {host} --port {port} --max-model-len {max_model_len} --max-num-batched-tokens {max_num_batched_tokens} --compilation-config '{compilation_config}' -tp {tensor_parallel}
`
	preview, err := ParseSparkRun([]byte(yaml), "https://raw.githubusercontent.com/spark-arena/recipe-registry/main/experimental-recipes/deepseek4/deepseek4-flash-fp8-vllm.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Compatible || preview.Draft.Distributed.Nodes != 2 || preview.Draft.Model.MaxContext != 256000 {
		t.Fatalf("preview = %#v", preview)
	}
}
