//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCompleteModelCache(t *testing.T, cacheRoot, modelID, revision string) string {
	t.Helper()
	name := "models--" + modelID[:indexSlash(modelID)] + "--" + modelID[indexSlash(modelID)+1:]
	repo := filepath.Join(cacheRoot, "hub", name)
	blob := filepath.Join(repo, "blobs", "weights")
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte(modelID), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(repo, "snapshots", revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "blobs", "weights"), filepath.Join(snapshot, "model.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "refs", "main"), []byte(revision+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return blob
}

func indexSlash(value string) int {
	for i, char := range value {
		if char == '/' {
			return i
		}
	}
	return -1
}

func TestReconcileManagedModelViewsRemovesOrphansAndMaterializesCache(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	modelsPath := filepath.Join(root, "Models")
	blob := writeCompleteModelCache(t, cacheRoot, "deepseek-ai/DeepSeek-V4-Flash", "deepseek-revision")
	orphan := filepath.Join(modelsPath, "Qwen--Qwen3.6-35B-A3B-FP8")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, ".cloudless-revision"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	userFolder := filepath.Join(modelsPath, "Personal-weights")
	if err := os.MkdirAll(userFolder, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := reconcileManagedModelViewsAt(cacheRoot, modelsPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphaned managed view remains: %v", err)
	}
	if _, err := os.Stat(userFolder); err != nil {
		t.Fatalf("user folder was changed: %v", err)
	}
	view := filepath.Join(modelsPath, "deepseek-ai--DeepSeek-V4-Flash", "model.bin")
	viewInfo, err := os.Stat(view)
	if err != nil {
		t.Fatal(err)
	}
	blobInfo, err := os.Stat(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(blobInfo, viewInfo) {
		t.Fatal("materialized model is not a zero-copy hard link")
	}
}

func TestReconcileManagedModelViewsPreservesUserCollision(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	modelsPath := filepath.Join(root, "Models")
	writeCompleteModelCache(t, cacheRoot, "Qwen/Visible", "revision")
	personal := filepath.Join(modelsPath, "Qwen--Visible")
	if err := os.MkdirAll(personal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personal, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reconcileManagedModelViewsAt(cacheRoot, modelsPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(personal, "notes.txt")); err != nil {
		t.Fatalf("user collision was changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(modelsPath, "Qwen--Visible-Cloudless", "model.bin")); err != nil {
		t.Fatalf("fallback managed view missing: %v", err)
	}
}

func TestReconcileManagedModelViewsAcceptsMarkedRevisionWithOtherPartialBlob(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	modelsPath := filepath.Join(root, "Models")
	revision := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writeCompleteModelCache(t, cacheRoot, "poolside/Laguna-S-2.1-NVFP4", revision)
	repo := filepath.Join(cacheRoot, "hub", "models--poolside--Laguna-S-2.1-NVFP4")
	if err := os.WriteFile(filepath.Join(repo, "blobs", "other.incomplete"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	markers := filepath.Join(repo, ".cloudless-complete")
	if err := os.MkdirAll(markers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markers, revision), []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reconcileManagedModelViewsAt(cacheRoot, modelsPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(modelsPath, "poolside--Laguna-S-2.1-NVFP4", "model.bin")); err != nil {
		t.Fatalf("marked completed revision was not exposed: %v", err)
	}
}

func TestHardlinkManagedModelTreeRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	source := filepath.Join(cacheRoot, "hub", "models--Qwen--Unsafe", "snapshots", "revision")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := hardlinkManagedModelTree(cacheRoot, source, filepath.Join(root, "view")); err == nil {
		t.Fatal("cache-escaping symlink was accepted")
	}
}
