package catalog

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/platform"
)

func TestARM64CatalogSelectsDGXComfyImageAndHidesUnsupportedApps(t *testing.T) {
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)

	comfy, ok := Get("comfyui")
	if !ok {
		t.Fatal("ComfyUI should be available on ARM64")
	}
	const want = "mmartial/comfyui-nvidia-docker:ubuntu24_cuda13.2-dgx-latest"
	if comfy.Image != want {
		t.Fatalf("ComfyUI ARM64 image = %q, want %q", comfy.Image, want)
	}
	if comfy.Preinstall {
		t.Fatal("ComfyUI must remain an optional install on ARM64")
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
	t.Setenv("CLOUDLESS_PLATFORM", platform.Generic)
	for _, id := range []string{"comfyui", "ai-toolkit", "unsloth", "openclaw"} {
		if _, ok := Get(id); !ok {
			t.Fatalf("%s should remain available on AMD64", id)
		}
	}
	comfy, _ := Get("comfyui")
	if comfy.Image != "mmartial/comfyui-nvidia-docker:latest" {
		t.Fatalf("unexpected AMD64 ComfyUI image: %q", comfy.Image)
	}
	if comfy.Preinstall {
		t.Fatal("ComfyUI must remain an optional install on AMD64")
	}
}

func TestBundledExcludesOptionalComfyUI(t *testing.T) {
	t.Setenv("CLOUDLESS_ARCH", "amd64")
	t.Setenv("CLOUDLESS_PLATFORM", platform.Generic)
	for _, app := range Bundled() {
		if app.ID == "comfyui" {
			t.Fatal("ComfyUI must not be pulled or started during boot provisioning")
		}
	}
}

func TestGenericARM64DoesNotReceiveDGXContainerRecipes(t *testing.T) {
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	t.Setenv("CLOUDLESS_PLATFORM", platform.Generic)
	for _, id := range []string{"vllm", "sglang", "comfyui"} {
		if _, ok := Get(id); ok {
			t.Fatalf("%s exposed a DGX-qualified ARM64 recipe on generic ARM64", id)
		}
	}
}

func TestPlatformAndVersionLockedRecipeUsesCentralCapabilities(t *testing.T) {
	app := App{
		ID: "spark-only", Platforms: []string{platform.DGXSpark},
		Architectures: []string{"arm64"}, MinCloudlessVersion: "0.3.0",
		MinDGXOSVersion: "7.6.0", RequiredFeatures: []string{capabilities.NVIDIACDI},
	}
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	t.Setenv("CLOUDLESS_VERSION", "0.3.0")
	t.Setenv("CLOUDLESS_DGX_OS_VERSION", "7.5.0")
	if app.SupportsHost() {
		t.Fatal("old DGX OS version unexpectedly satisfied recipe")
	}

	t.Setenv("CLOUDLESS_DGX_OS_VERSION", "7.6.0")
	if !app.SupportsHost() {
		t.Fatalf("compatible Spark recipe rejected: %#v", app.Availability())
	}

	t.Setenv("CLOUDLESS_PLATFORM", platform.Generic)
	if app.SupportsHost() {
		t.Fatal("generic host accepted DGX-only recipe")
	}
}

func TestCatalogGetCannotBypassPlatformLock(t *testing.T) {
	original := apps
	t.Cleanup(func() { apps = original })
	apps = append(apps, App{
		ID: "test-dgx-only", Image: "example/test:latest",
		Platforms: []string{platform.DGXSpark},
	})

	t.Setenv("CLOUDLESS_PLATFORM", platform.Generic)
	if _, ok := Get("test-dgx-only"); ok {
		t.Fatal("Catalog.Get exposed a DGX-only recipe on a generic host")
	}
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	if _, ok := Get("test-dgx-only"); !ok {
		t.Fatal("Catalog.Get rejected the DGX-only recipe on a Spark")
	}
}
