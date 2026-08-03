package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestRecipeSnapshotCompleteRejectsPartialDownload(t *testing.T) {
	repo := t.TempDir()
	revision := "abc123"
	if err := os.MkdirAll(filepath.Join(repo, "snapshots", revision), 0o755); err != nil {
		t.Fatal(err)
	}
	if recipeSnapshotComplete(repo, revision) {
		t.Fatal("empty snapshot was reported complete")
	}
	if err := os.WriteFile(filepath.Join(repo, "snapshots", revision, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !recipeSnapshotComplete(repo, revision) {
		t.Fatal("non-empty complete snapshot was rejected")
	}
	if err := os.MkdirAll(filepath.Join(repo, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "blobs", "weight.incomplete"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if recipeSnapshotComplete(repo, revision) {
		t.Fatal("partial snapshot was reported ready")
	}
}

func TestRecipeSnapshotCompleteAcceptsMarkedRevisionWithOtherPartialBlob(t *testing.T) {
	repo := t.TempDir()
	revision := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	snapshot := filepath.Join(repo, "snapshots", revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	if !recipeSnapshotComplete(repo, revision) {
		t.Fatal("another revision's partial blob invalidated a marked complete snapshot")
	}
}

func TestRecipeModelInstalledRequiresExactCompleteSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "abc123"}}
	if recipeModelInstalled(recipe) {
		t.Fatal("missing model was reported installed")
	}
	repo := filepath.Join(root, "hub", "models--owner--model")
	if err := os.MkdirAll(filepath.Join(repo, "snapshots", recipe.Model.Revision), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "snapshots", recipe.Model.Revision, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !recipeModelInstalled(recipe) {
		t.Fatal("complete exact model snapshot was not reported installed")
	}
	if err := os.WriteFile(filepath.Join(repo, "weights.incomplete"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if recipeModelInstalled(recipe) {
		t.Fatal("partial model download was reported installed")
	}
}

func TestExistingExactRecipeModelIsReadyWithoutPriorManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "abc123"}}
	snapshot := filepath.Join(root, "hub", "models--owner--model", "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "weights.bin"), []byte("already here"), 0o640); err != nil {
		t.Fatal(err)
	}
	if !recipeCachedModelReady(t.Context(), nil, recipe) {
		t.Fatal("exact installed revision was treated as requiring a download")
	}
}

func TestCertifyExistingRecipeModelCacheAdoptsExactFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "abc123"}}
	snapshot := filepath.Join(root, "hub", "models--owner--model", "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("existing exact weights")
	if err := os.WriteFile(filepath.Join(snapshot, "model.bin"), payload, 0o640); err != nil {
		t.Fatal(err)
	}
	var progressed int64
	manifest, err := certifyExistingRecipeModelCache(t.Context(), nil, recipe,
		map[string]int64{"model.bin": int64(len(payload))}, func(delta int64) { progressed += delta })
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Bytes != int64(len(payload)) || progressed != int64(len(payload)) {
		t.Fatalf("manifest bytes=%d progress=%d", manifest.Bytes, progressed)
	}
	if _, err := inspectRecipeModelManifest(t.Context(), nil, recipe); err != nil {
		t.Fatalf("adopted model was not certified: %v", err)
	}
}

func TestCertifyExistingRecipeModelCacheRejectsMissingHubFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "abc123"}}
	snapshot := filepath.Join(root, "hub", "models--owner--model", "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "config.json"), []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := certifyExistingRecipeModelCache(t.Context(), nil, recipe,
		map[string]int64{"config.json": 2, "model.bin": 100}, nil)
	if !errors.Is(err, errRecipeModelCacheIncomplete) {
		t.Fatalf("missing Hub file error = %v", err)
	}
}

func TestVerifyOrCertifyInstalledRecipeModelCacheAdoptsModelManagerSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "owner/model", Revision: "abc123"}}
	repo := filepath.Join(root, "hub", "models--owner--model")
	snapshot := filepath.Join(repo, "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("model-manager-verified-weights")
	if err := os.WriteFile(filepath.Join(snapshot, "model.bin"), payload, 0o640); err != nil {
		t.Fatal(err)
	}
	markers := filepath.Join(repo, ".cloudless-complete")
	if err := os.MkdirAll(markers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markers, recipe.Model.Revision), []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previousInventory := recipeModelRevisionInventory
	recipeModelRevisionInventory = func(_ context.Context, modelID, revision, token string) (map[string]int64, error) {
		if modelID != recipe.Model.ID || revision != recipe.Model.Revision || token != "token" {
			t.Fatalf("inventory request = %s@%s token=%q", modelID, revision, token)
		}
		return map[string]int64{"model.bin": int64(len(payload))}, nil
	}
	t.Cleanup(func() { recipeModelRevisionInventory = previousInventory })

	manifest, err := verifyOrCertifyInstalledRecipeModelCache(t.Context(), nil, recipe, "token", nil)
	if err != nil {
		t.Fatalf("adopt Model Manager snapshot: %v", err)
	}
	if manifest.Bytes != int64(len(payload)) {
		t.Fatalf("manifest bytes = %d, want %d", manifest.Bytes, len(payload))
	}
	if _, err := verifyRecipeModelCache(t.Context(), nil, recipe); err != nil {
		t.Fatalf("adopted snapshot was not reusable: %v", err)
	}
}

func TestDeepSeekLaunchPlanExplainsCustomRuntime(t *testing.T) {
	recipe := localrecipes.Recipe{ID: localrecipes.DeepSeekDSparkID}
	recipe.Source.URL = localrecipes.DeepSeekDSparkSource
	recipe.Source.Revision = localrecipes.DeepSeekDSparkRevision
	recipe.Runtime.Lifecycle.Build.Program = "bash"
	explanation := recipeRuntimeExplanation(recipe)
	for _, want := range []string{"standard Cloudless vLLM", "DeepSeek V4", "sparse MLA", "speculative decoding", "SM120/SM121"} {
		if !strings.Contains(explanation, want) {
			t.Fatalf("custom runtime explanation is missing %q: %s", want, explanation)
		}
	}
	if got := recipeRuntimeEstimate(recipe); got != deepSeekRuntimeEstimateBytes {
		t.Fatalf("runtime estimate = %d", got)
	}
}

func TestGenericRecipeLaunchPlanDoesNotClaimDeepSeekPatches(t *testing.T) {
	recipe := localrecipes.Recipe{}
	recipe.Runtime.Lifecycle.Build.Program = "make"
	explanation := recipeRuntimeExplanation(recipe)
	if !strings.Contains(explanation, "own pinned inference runtime") || strings.Contains(explanation, "DeepSeek") {
		t.Fatalf("generic explanation = %q", explanation)
	}
	if recipeRuntimeEstimate(recipe) != 0 {
		t.Fatal("generic recipe received a fabricated runtime size")
	}
}
