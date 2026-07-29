package jobs

import "testing"

func TestListIncludesIdentityAndByteProgress(t *testing.T) {
	manager := NewManager()
	model := manager.Create("model-dl:Qwen/Test")
	manager.Create("other")
	model.ProgressBytes("downloading", "Downloading", 25, 100)

	got := manager.List("model-dl:")
	if len(got) != 1 {
		t.Fatalf("List returned %d jobs, want 1", len(got))
	}
	if got[0].ID != model.ID || got[0].AppID != "model-dl:Qwen/Test" {
		t.Fatalf("identity = %#v", got[0])
	}
	if got[0].BytesDone != 25 || got[0].BytesTotal != 100 || got[0].Phase != "downloading" {
		t.Fatalf("progress = %#v", got[0].Update)
	}
}

func TestCancelIsTerminalWithoutError(t *testing.T) {
	manager := NewManager()
	job := manager.Create("model-dl:org/model")
	job.ProgressBytes("downloading", "Downloading", 25, 100)
	job.Cancel()

	got := job.Snapshot()
	if !got.Done || got.Phase != "canceled" || got.Message != "Canceled" || got.Error != "" {
		t.Fatalf("canceled snapshot = %#v", got)
	}
}

func TestChangingPhaseClearsStaleByteProgress(t *testing.T) {
	job := NewManager().Create("recipe:test")
	job.ProgressBytes("building", "Downloading runtime", 50, 100)
	job.Progress("syncing-image", "Preparing transfer", 2, 8)
	got := job.Snapshot()
	if got.BytesDone != 0 || got.BytesTotal != 0 || got.Phase != "syncing-image" {
		t.Fatalf("stale byte progress survived phase change: %#v", got)
	}
}
