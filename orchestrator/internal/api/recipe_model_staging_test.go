package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestSeedRecipeDownloadStagingReusesExistingBlobs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "abc123"}}
	repo := filepath.Join(root, "hub", "models--owner--model")
	blob := filepath.Join(repo, "blobs", "weight")
	if err := os.MkdirAll(filepath.Dir(blob), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("existing weights"), 0o640); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(repo, "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "blobs", "weight"), filepath.Join(snapshot, "model.bin")); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(root, ".download-staging", "test")
	if err := seedRecipeDownloadStaging(recipe, staging); err != nil {
		t.Fatal(err)
	}
	seededBlob := filepath.Join(staging, "hub", "models--owner--model", "blobs", "weight")
	sourceInfo, err := os.Stat(blob)
	if err != nil {
		t.Fatal(err)
	}
	seededInfo, err := os.Stat(seededBlob)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, seededInfo) {
		t.Fatal("existing blob was copied instead of reused with a hard link")
	}
	if target, err := os.Readlink(filepath.Join(staging, "hub", "models--owner--model", "snapshots", recipe.Model.Revision, "model.bin")); err != nil || target != filepath.Join("..", "..", "blobs", "weight") {
		t.Fatalf("snapshot link was not preserved: target=%q err=%v", target, err)
	}
}
