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
