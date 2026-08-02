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
	job.ProgressNodes("peer-downloading", "Workers downloading", []NodeProgress{{Node: "spark-2", Phase: "downloading"}}, 50, 100)
	job.Progress("syncing-image", "Preparing transfer", 2, 8)
	got := job.Snapshot()
	if got.BytesDone != 0 || got.BytesTotal != 0 || got.Phase != "syncing-image" || len(got.Nodes) != 0 {
		t.Fatalf("stale byte progress survived phase change: %#v", got)
	}
}

func TestDistributedNodeProgressIsCopiedAndAggregated(t *testing.T) {
	job := NewManager().Create("engine:vllm")
	nodes := []NodeProgress{
		{Node: "spark-1", Phase: "loading", Percent: 60},
		{Node: "spark-2", Phase: "downloading", BytesDone: 25, BytesTotal: 100, Percent: 25, ETASecs: 30},
	}
	job.ProgressNodes("peer-downloading", "spark-2 is downloading", nodes, 25, 100)
	nodes[1].Message = "mutated by caller"
	got := job.Snapshot()
	if got.Percent != 25 || len(got.Nodes) != 2 || got.Nodes[1].Message == "mutated by caller" ||
		got.Nodes[1].ETASecs != 30 {
		t.Fatalf("distributed progress = %#v", got)
	}
	got.Nodes[0].Phase = "mutated snapshot"
	if again := job.Snapshot(); again.Nodes[0].Phase != "loading" {
		t.Fatal("snapshot exposed the job's node-progress backing slice")
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

func TestOperationProgressUsesExplicitPhaseETA(t *testing.T) {
	job := NewManager().Create("recipe:test")
	job.Progress("starting", "Starting container", 3, 6)
	job.ProgressOperationETA("loading-model", "Loading checkpoint shards", "model", 60, 11, 49, 1127)
	got := job.Snapshot()
	if got.Percent != 60 || got.ItemsDone != 11 || got.ItemsTotal != 49 || got.ETASecs != 1127 || got.LayersDone != 0 || got.LayersTotal != 0 {
		t.Fatalf("explicit progress = %#v", got)
	}
	job.Progress("connecting", "Connecting", 5, 6)
	if got := job.Snapshot(); got.ETASecs == 1127 || got.ItemsDone != 0 || got.ItemsTotal != 0 || got.CurrentItem != "" {
		t.Fatalf("phase-local detail survived the next lifecycle stage: %#v", got)
	}
}

func TestObserverReceivesInitialAndSubsequentProgressOutsideJobLock(t *testing.T) {
	job := NewManager().Create("recipe:test")
	updates := make([]Update, 0, 2)
	job.Observe(func(update Update) {
		// Snapshot takes the same lock and proves the callback is not invoked
		// while apply still owns it.
		_ = job.Snapshot()
		updates = append(updates, update)
	})
	job.Progress("building", "Building runtime", 2, 8)
	if len(updates) != 2 || updates[0].Phase != "pending" || updates[1].Phase != "building" {
		t.Fatalf("observer updates = %#v", updates)
	}
}

func TestListUsesNumericCreationOrderAndBoundsTerminalHistory(t *testing.T) {
	manager := NewManager()
	manager.maxTerminal = 3
	for index := 0; index < 12; index++ {
		job := manager.Create("recipe:test")
		job.Succeed("")
	}
	jobs := manager.List("")
	if len(jobs) != 3 {
		t.Fatalf("terminal history length = %d, want 3", len(jobs))
	}
	if jobs[0].ID != "job-10" || jobs[1].ID != "job-11" || jobs[2].ID != "job-12" {
		t.Fatalf("numeric creation order = %#v", jobs)
	}
}
