package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/hardware"
)

func TestRecipeMinimumAcceleratorMemoryDistributesWeightsWithHeadroom(t *testing.T) {
	want := int64(60 * 1024)
	modelBytes := int64(100 * 1024 * 1024 * 1024)
	if got := recipeMinimumAcceleratorMemoryMB(modelBytes, 2); got != want {
		t.Fatalf("minimum memory = %d MiB, want %d", got, want)
	}
}

func TestHasNVIDIACDISpecAtRequiresNonEmptyNVIDIAFile(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "var", "run", "cdi")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "nvidia.yaml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if hasNVIDIACDISpecAt(root) {
		t.Fatal("empty CDI specification was accepted")
	}
	if err := os.WriteFile(path, []byte("cdiVersion: 0.6.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasNVIDIACDISpecAt(root) {
		t.Fatal("NVIDIA CDI specification was not found")
	}
}

func TestParseRecipeComputeCapabilities(t *testing.T) {
	values, err := parseRecipeComputeCapabilities("12.1\n12.1\n")
	if err != nil || len(values) != 2 || values[0] != 12.1 {
		t.Fatalf("capabilities = %#v, %v", values, err)
	}
	if _, err := parseRecipeComputeCapabilities("N/A"); err == nil {
		t.Fatal("invalid capability was accepted")
	}
}

func TestVerifyRecipeGPUNodeEnforcesDriverMemoryAndCapability(t *testing.T) {
	gpus := []hardware.GPU{{Name: "NVIDIA GB10", Driver: "580.95", MemTotalMB: 120000}}
	if _, err := verifyRecipeGPUNode("spark-a", gpus, []float64{12.1}, 100000, 12.0, "580.95"); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRecipeGPUNode("spark-a", gpus, []float64{11.0}, 100000, 12.0, "580.95"); err == nil {
		t.Fatal("insufficient compute capability was accepted")
	}
	if _, err := verifyRecipeGPUNode("spark-a", gpus, []float64{12.1}, 130000, 12.0, "580.95"); err == nil {
		t.Fatal("insufficient accelerator memory was accepted")
	}
	if _, err := verifyRecipeGPUNode("spark-a", gpus, []float64{12.1}, 100000, 12.0, "999.0"); err == nil {
		t.Fatal("mismatched driver was accepted")
	}
}
