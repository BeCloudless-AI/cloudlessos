package catalog

import "testing"

func TestARM64CatalogSelectsDGXComfyImageAndHidesUnsupportedApps(t *testing.T) {
	t.Setenv("CLOUDLESS_ARCH", "arm64")

	comfy, ok := Get("comfyui")
	if !ok {
		t.Fatal("ComfyUI should be available on ARM64")
	}
	const want = "mmartial/comfyui-nvidia-docker:ubuntu24_cuda13.2-dgx-latest"
	if comfy.Image != want {
		t.Fatalf("ComfyUI ARM64 image = %q, want %q", comfy.Image, want)
	}
	for _, id := range []string{"ai-toolkit", "unsloth", "openclaw"} {
		if _, ok := Get(id); ok {
			t.Fatalf("%s must be hidden until an ARM64 recipe is published", id)
		}
	}
	for _, id := range []string{"vllm", "sglang", "llamacpp", "open-webui", "hermes"} {
		if _, ok := Get(id); !ok {
			t.Fatalf("%s should be available on ARM64", id)
		}
	}
	vllm, _ := Get("vllm")
	if vllm.Image != "nvcr.io/nvidia/vllm:26.05.post1-py3" {
		t.Fatalf("unexpected ARM64 vLLM image: %q", vllm.Image)
	}
	if len(vllm.Command) < 2 || vllm.Command[0] != "vllm" || vllm.Command[1] != "serve" {
		t.Fatalf("ARM64 vLLM command must include its NGC entrypoint: %#v", vllm.Command)
	}
	sglang, _ := Get("sglang")
	if sglang.Image != "lmsysorg/sglang:latest-cu130" {
		t.Fatalf("unexpected ARM64 SGLang image: %q", sglang.Image)
	}
}

func TestAMD64CatalogKeepsExistingRecipes(t *testing.T) {
	t.Setenv("CLOUDLESS_ARCH", "amd64")
	for _, id := range []string{"comfyui", "ai-toolkit", "unsloth", "openclaw"} {
		if _, ok := Get(id); !ok {
			t.Fatalf("%s should remain available on AMD64", id)
		}
	}
	comfy, _ := Get("comfyui")
	if comfy.Image != "mmartial/comfyui-nvidia-docker:latest" {
		t.Fatalf("unexpected AMD64 ComfyUI image: %q", comfy.Image)
	}
}
