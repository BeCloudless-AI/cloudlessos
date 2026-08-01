package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestRunRecipeModelDownloadCertifiesExistingRevisionWithoutDownload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "abc123"}}
	recipe.Runtime.Lifecycle.Download.Program = "this-command-must-not-run"
	snapshot := filepath.Join(root, "hub", "models--owner--model", "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o750); err != nil {
		t.Fatal(err)
	}
	payload := []byte("weights already downloaded by Model Manager")
	if err := os.WriteFile(filepath.Join(snapshot, "model.bin"), payload, 0o640); err != nil {
		t.Fatal(err)
	}

	original := recipeModelRevisionInventory
	recipeModelRevisionInventory = func(context.Context, string, string, string) (map[string]int64, error) {
		return map[string]int64{"model.bin": int64(len(payload))}, nil
	}
	t.Cleanup(func() { recipeModelRevisionInventory = original })

	manager := jobs.NewManager()
	job := manager.Create("recipe:test")
	server := &Server{}
	if _, err := server.runRecipeModelDownload(t.Context(), job, "operation", recipe, recipe, t.TempDir(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectRecipeModelManifest(t.Context(), nil, recipe); err != nil {
		t.Fatalf("existing exact revision was not certified: %v", err)
	}
}
