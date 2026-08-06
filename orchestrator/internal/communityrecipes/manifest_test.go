package communityrecipes

import (
	"strings"
	"testing"
)

func safeManifest() map[string]any {
	empty := func() map[string]any { return map[string]any{"program": "", "args": []any{}} }
	return map[string]any{
		"schema":   ManifestSchema,
		"metadata": map[string]any{"name": "Safe recipe", "description": "A safe managed recipe.", "version": "1.0.0", "license": "Apache-2.0", "minimumAcceleratorMemoryGB": 48},
		"recipe": map[string]any{
			"source":      map[string]any{"url": "", "revision": "", "files": map[string]any{}},
			"engine":      map[string]any{"type": "vllm", "image": "ghcr.io/cloudless/vllm@sha256:" + string(make([]byte, 0)), "servedModelName": "cloudless", "containerPort": 8890, "apiPath": "/v1", "proxyHost": "host.docker.internal", "restartPolicy": "no"},
			"model":       map[string]any{"revision": "1111111111111111111111111111111111111111", "tensorParallel": 1, "pipelineParallel": 1},
			"distributed": map[string]any{"nodes": 1},
			"runtime":     map[string]any{"adapter": "managed-container-v1", "workingDir": "", "lifecycle": map[string]any{"build": empty(), "download": empty(), "start": empty(), "stop": empty()}},
			"health":      map[string]any{"scheme": "http", "host": "127.0.0.1", "port": 8890},
		},
	}
}

func TestValidateManagedManifest(t *testing.T) {
	manifest := safeManifest()
	manifest["recipe"].(map[string]any)["engine"].(map[string]any)["image"] = "ghcr.io/cloudless/vllm@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	if err := ValidateManagedManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest["recipe"].(map[string]any)["engine"].(map[string]any)["containerPort"] = 8000
	if err := ValidateManagedManifest(manifest); err == nil {
		t.Fatal("unsafe port was accepted")
	}
}

func TestValidateManagedSGLangManifest(t *testing.T) {
	manifest := safeManifest()
	recipe := manifest["recipe"].(map[string]any)
	engine := recipe["engine"].(map[string]any)
	engine["type"] = "sglang"
	engine["image"] = "docker.io/lmsysorg/sglang@sha256:" + strings.Repeat("a", 64)
	engine["arguments"] = []any{"--allow-auto-truncate", "--enable-fp32-lm-head"}
	recipe["runtime"].(map[string]any)["environment"] = map[string]any{
		"SGLANG_ALLOW_OVERWRITE_LONGER_CONTEXT_LEN": "1",
		"SGLANG_ENABLE_SPEC_V2":                     "1",
	}
	if err := ValidateManagedManifest(manifest); err != nil {
		t.Fatal(err)
	}
	engine["arguments"] = []any{"--port", "9999"}
	if err := ValidateManagedManifest(manifest); err == nil {
		t.Fatal("managed SGLang contract override was accepted")
	}
}

func TestValidateAdvancedContainerManifest(t *testing.T) {
	manifest := safeManifest()
	manifest["schema"] = AdvancedManifestSchema
	recipe := manifest["recipe"].(map[string]any)
	recipe["platform"] = "dgx-spark"
	engine := recipe["engine"].(map[string]any)
	model := recipe["model"].(map[string]any)
	runtime := recipe["runtime"].(map[string]any)
	engine["image"] = "ghcr.io/cloudless/vllm@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	engine["entryPoint"] = "/bin/bash"
	engine["command"] = []any{"-lc", "exec vllm serve model --speculative-config '{}'"}
	model["dependencies"] = []any{map[string]any{"id": "example/dflash", "revision": "2222222222222222222222222222222222222222", "role": "speculative-draft"}}
	runtime["adapter"] = "advanced-container-v1"
	runtime["environment"] = map[string]any{"CUTE_DSL_ARCH": "sm_121a"}
	runtime["container"] = map[string]any{"user": "0", "ipc": "host", "capAdd": []any{"IPC_LOCK"}}
	if err := ValidateManagedManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest["schema"] = ManifestSchema
	if err := ValidateManagedManifest(manifest); err == nil {
		t.Fatal("advanced container was accepted under the frozen v1 manifest schema")
	}
}

func TestValidateDistributedAdvancedContainerManifest(t *testing.T) {
	manifest := safeManifest()
	manifest["schema"] = AdvancedManifestSchema
	recipe := manifest["recipe"].(map[string]any)
	recipe["platform"] = "dgx-spark"
	recipe["engine"].(map[string]any)["image"] = "ghcr.io/cloudless/vllm@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	recipe["model"].(map[string]any)["tensorParallel"] = 2
	recipe["distributed"] = map[string]any{"nodes": 2, "backend": "nccl"}
	runtime := recipe["runtime"].(map[string]any)
	runtime["adapter"] = "advanced-container-v1"
	runtime["buildOnce"] = true
	runtime["downloadOnce"] = true
	runtime["container"] = map[string]any{"ipc": "host", "infiniband": true}
	if err := ValidateManagedManifest(manifest); err != nil {
		t.Fatal(err)
	}
	runtime["container"].(map[string]any)["infiniband"] = false
	if err := ValidateManagedManifest(manifest); err == nil {
		t.Fatal("distributed container without bounded InfiniBand permission was accepted")
	}
}
