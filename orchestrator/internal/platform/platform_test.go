package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAtDGXSpark(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proc", "device-tree", "model")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("NVIDIA DGX Spark\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectAt(root); got != DGXSpark {
		t.Fatalf("DetectAt() = %q, want %q", got, DGXSpark)
	}
}

func TestDetectAtDGXSparkDMIName(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sys", "devices", "virtual", "dmi", "id", "product_name")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("NVIDIA_DGX_Spark\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectAt(root); got != DGXSpark {
		t.Fatalf("DetectAt() = %q, want %q", got, DGXSpark)
	}
}

func TestDetectAtGeneric(t *testing.T) {
	if got := DetectAt(t.TempDir()); got != Generic {
		t.Fatalf("DetectAt() = %q, want %q", got, Generic)
	}
}

func TestDetectOverride(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", DGXSpark)
	if got := Detect(); got != DGXSpark {
		t.Fatalf("Detect() = %q, want %q", got, DGXSpark)
	}
}

func TestDGXSparkUsesCDI(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", DGXSpark)
	if got := GPUContainerMode(); got != GPUCDI {
		t.Fatalf("GPUContainerMode() = %q, want %q", got, GPUCDI)
	}
}

func TestGPUContainerModeOverride(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", DGXSpark)
	t.Setenv("CLOUDLESS_GPU_MODE", GPUDocker)
	if got := GPUContainerMode(); got != GPUDocker {
		t.Fatalf("GPUContainerMode() = %q, want %q", got, GPUDocker)
	}
}
