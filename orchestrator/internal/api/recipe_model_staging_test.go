package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestRecipeStagingVolumeIsStableAndModelSpecific(t *testing.T) {
	first := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "one"}}
	second := first
	second.Model.Revision = "two"
	if recipeStagingCacheVolume(first) != recipeStagingCacheVolume(first) {
		t.Fatal("staging volume is not stable across retries")
	}
	if recipeStagingCacheVolume(first) == recipeStagingCacheVolume(second) {
		t.Fatal("different revisions share a staging volume")
	}
}

func TestAtomicRecipePromotionReplacesOnlyAfterStagingExists(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "hub", "model")
	stage := filepath.Join(root, "stage", "model")
	backup := filepath.Join(root, "backup", "model")
	if err := os.MkdirAll(final, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(final, "weights"), []byte("known-good"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicallyPromoteRecipeRepository(final, stage, backup); err == nil {
		t.Fatal("missing staging tree was promoted")
	}
	content, err := os.ReadFile(filepath.Join(final, "weights"))
	if err != nil || string(content) != "known-good" {
		t.Fatalf("failed promotion damaged the good cache: %q %v", content, err)
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "weights"), []byte("verified-new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicallyPromoteRecipeRepository(final, stage, backup); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(filepath.Join(final, "weights"))
	if err != nil || string(content) != "verified-new" {
		t.Fatalf("new cache was not activated: %q %v", content, err)
	}
}
