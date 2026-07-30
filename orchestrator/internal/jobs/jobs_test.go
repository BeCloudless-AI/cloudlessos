package jobs

import (
	"testing"
	"time"
)

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

func TestCreateUniqueReconnectsToActiveOperation(t *testing.T) {
	manager := NewManager()
	first, created := manager.CreateUnique("app:n8n:install", "app:n8n:")
	if !created {
		t.Fatal("first operation was not created")
	}
	again, created := manager.CreateUnique("app:n8n:uninstall", "app:n8n:")
	if created || again != first {
		t.Fatal("conflicting operation did not reconnect to active job")
	}
	first.Succeed("")
	third, created := manager.CreateUnique("app:n8n:uninstall", "app:n8n:")
	if !created || third == first {
		t.Fatal("terminal operation prevented a new job")
	}
}

func TestOperationProgressIncludesTimingAndETA(t *testing.T) {
	job := NewManager().Create("app:n8n:install")
	job.start = time.Now().Add(-20 * time.Second)
	job.ProgressOperation("pulling", "Downloading n8n", "n8n", 25, 0, 1)
	got := job.Snapshot()
	if got.Percent != 25 || got.CurrentItem != "n8n" || got.ItemsTotal != 1 {
		t.Fatalf("operation detail = %#v", got)
	}
	if got.ElapsedSecs < 19 || got.ETASecs < 55 {
		t.Fatalf("timing detail = elapsed %d eta %d", got.ElapsedSecs, got.ETASecs)
	}
}
