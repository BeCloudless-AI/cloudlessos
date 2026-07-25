package hardware

import (
	"os"
	"testing"
)

func TestStorageBytesReportsFilesystemCapacity(t *testing.T) {
	total, available := storageBytes(t.TempDir())
	if total == 0 {
		t.Fatal("storage total should be reported")
	}
	if available == 0 || available > total {
		t.Fatalf("invalid available storage: %d of %d", available, total)
	}
}

func TestStoragePathSupportsDeploymentOverride(t *testing.T) {
	path := t.TempDir()
	t.Setenv("CLOUDLESS_STORAGE_PATH", path)
	if got := storagePath(); got != path {
		t.Fatalf("storagePath() = %q, want %q", got, path)
	}
}

func TestSystemIncludesStorageAndSparkIdentity(t *testing.T) {
	path := t.TempDir()
	t.Setenv("CLOUDLESS_STORAGE_PATH", path)
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	got := Sys()
	if got.StorageTotalBytes == 0 || got.StorageAvailableBytes == 0 {
		t.Fatalf("missing storage capacity: %#v", got)
	}
	if !got.UnifiedGPU || got.Platform != "dgx-spark" {
		t.Fatalf("missing Spark identity: %#v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
