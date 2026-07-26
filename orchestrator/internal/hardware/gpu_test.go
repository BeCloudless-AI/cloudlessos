package hardware

import "testing"

func TestApplyUnifiedMemory(t *testing.T) {
	gpus := []GPU{{Name: "NVIDIA GB10", MemUsedMB: -1, MemTotalMB: -1}}
	ApplyUnifiedMemory(gpus, 124609, 118867)
	if gpus[0].MemoryType != "unified" {
		t.Fatalf("memory type = %q", gpus[0].MemoryType)
	}
	if gpus[0].MemTotalMB != 124609 || gpus[0].MemUsedMB != 5742 {
		t.Fatalf("unified memory = %d/%d MB", gpus[0].MemUsedMB, gpus[0].MemTotalMB)
	}
}

func TestParseGPUsCSVSupportsPeerTelemetry(t *testing.T) {
	gpus := ParseGPUsCSV("0, NVIDIA GB10, 1024, 4096, 37, 48, 22.5, 80.0, 580.173.02\n")
	if len(gpus) != 1 || gpus[0].Name != "NVIDIA GB10" || gpus[0].UtilPct != 37 || gpus[0].PowerW != 22.5 {
		t.Fatalf("parsed GPUs = %#v", gpus)
	}
}
