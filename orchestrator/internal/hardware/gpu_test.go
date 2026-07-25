package hardware

import "testing"

func TestApplyUnifiedMemory(t *testing.T) {
	gpus := []GPU{{Name: "NVIDIA GB10", MemUsedMB: -1, MemTotalMB: -1}}
	applyUnifiedMemory(gpus, 124609, 118867)
	if gpus[0].MemoryType != "unified" {
		t.Fatalf("memory type = %q", gpus[0].MemoryType)
	}
	if gpus[0].MemTotalMB != 124609 || gpus[0].MemUsedMB != 5742 {
		t.Fatalf("unified memory = %d/%d MB", gpus[0].MemUsedMB, gpus[0].MemTotalMB)
	}
}
