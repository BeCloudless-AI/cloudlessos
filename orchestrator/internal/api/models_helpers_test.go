package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanModelHub(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, "hub")
	repo := filepath.Join(hub, "models--Qwen--Qwen-Test")
	if err := os.MkdirAll(filepath.Join(repo, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "snapshots", "abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "refs", "main"), []byte("abc123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hub, "datasets--ignore--me"), 0o755); err != nil {
		t.Fatal(err)
	}
	have := scanModelHub(root)
	if !have["Qwen/Qwen-Test"] || len(have) != 1 {
		t.Fatalf("scanModelHub = %#v", have)
	}
}

func TestDirectoryBytesAndProgressMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "weights"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := directoryBytes(root); got != 1024 {
		t.Fatalf("directoryBytes = %d, want 1024", got)
	}
	if got := formatDownloadProgress(512, 1024); got == "" {
		t.Fatal("formatDownloadProgress returned an empty message")
	}
}

func TestExposeModelCacheUsesHardlinks(t *testing.T) {
	cloudlessHome := filepath.Join(t.TempDir(), "Cloudless")
	t.Setenv("CLOUDLESS_HOME", cloudlessHome)
	cache := t.TempDir()
	repoRoot := filepath.Join(cache, "hub", "models--Qwen--Visible")
	revision := "abc123"
	blob := filepath.Join(repoRoot, "blobs", "weights")
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(repoRoot, "snapshots", revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "blobs", "weights"), filepath.Join(snapshot, "model.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "refs", "main"), []byte(revision), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exposeModelCache(cache, map[string]bool{"Qwen/Visible": true}); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(cloudlessHome, "Models", "Qwen--Visible", "model.bin")
	sourceInfo, err := os.Stat(blob)
	if err != nil {
		t.Fatal(err)
	}
	viewInfo, err := os.Stat(view)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, viewInfo) {
		t.Fatal("visible model file is not hard-linked to the cache")
	}
}
