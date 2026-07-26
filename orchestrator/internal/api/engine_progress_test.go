package api

import "testing"

func TestModelCheckpointProgressUsesLatestUpdate(t *testing.T) {
	logs := "Loading safetensors checkpoint shards:  21% Completed | 9/42\r\n" +
		"Loading safetensors checkpoint shards: 100% Completed | 42/42\r\n"
	done, total := modelCheckpointProgress(logs)
	if done != 42 || total != 42 {
		t.Fatalf("progress = %d/%d, want 42/42", done, total)
	}
}

func TestModelCheckpointProgressHandlesNoProgress(t *testing.T) {
	done, total := modelCheckpointProgress("engine is initializing")
	if done != 0 || total != 0 {
		t.Fatalf("progress = %d/%d, want 0/0", done, total)
	}
}
