package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

const deepSeekRuntimeEstimateBytes int64 = 23_000_000_000

type recipeLaunchPlan struct {
	RequiresCustomRuntime bool   `json:"requiresCustomRuntime"`
	RuntimeReady          bool   `json:"runtimeReady"`
	RuntimeImage          string `json:"runtimeImage"`
	RuntimeBytes          int64  `json:"runtimeBytes,omitempty"`
	RuntimeEstimateBytes  int64  `json:"runtimeEstimateBytes,omitempty"`
	RuntimeReason         string `json:"runtimeReason"`
	ModelInstalled        bool   `json:"modelInstalled"`
	ModelReady            bool   `json:"modelReady"`
	ModelID               string `json:"modelId"`
	BuildOnce             bool   `json:"buildOnce"`
	DownloadOnce          bool   `json:"downloadOnce"`
	Nodes                 int    `json:"nodes"`
}

// recipeModelInstalled is the cheap lifecycle gate used by the API and UI. A
// complete snapshot means the exact immutable model revision already exists;
// recipe launch may certify it in place without downloading the weights again.
func recipeModelInstalled(recipe localrecipes.Recipe) bool {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok || strings.TrimSpace(recipe.Model.Revision) == "" {
		return false
	}
	root, err := recipeCacheVolume(recipe)
	if err != nil {
		return false
	}
	return recipeSnapshotComplete(filepath.Join(root, "hub", cacheName), recipe.Model.Revision)
}

func recipeRuntimeExplanation(recipe localrecipes.Recipe) string {
	if recipe.ID == localrecipes.DeepSeekDSparkID ||
		(strings.EqualFold(recipe.Source.URL, localrecipes.DeepSeekDSparkSource) && recipe.Source.Revision == localrecipes.DeepSeekDSparkRevision) {
		return "The standard Cloudless vLLM runtime does not contain this recipe's DeepSeek V4 model support, sparse MLA attention, DSpark speculative decoding, and DGX Spark SM120/SM121 kernel patches. The pinned custom runtime is required for this configuration."
	}
	if recipe.Runtime.Lifecycle.Build.Program != "" {
		return "This recipe declares its own pinned inference runtime instead of the standard Cloudless engine. Cloudless must prepare that exact runtime before it can safely launch the model."
	}
	return "This recipe uses its configured inference-engine image."
}

func recipeRuntimeEstimate(recipe localrecipes.Recipe) int64 {
	if recipe.ID == localrecipes.DeepSeekDSparkID ||
		(strings.EqualFold(recipe.Source.URL, localrecipes.DeepSeekDSparkSource) && recipe.Source.Revision == localrecipes.DeepSeekDSparkRevision) {
		return deepSeekRuntimeEstimateBytes
	}
	return 0
}

func recipeSnapshotComplete(repoRoot, revision string) bool {
	info, err := os.Stat(filepath.Join(repoRoot, "snapshots", revision))
	if err != nil || !info.IsDir() {
		return false
	}
	incomplete := false
	_ = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() && strings.HasSuffix(entry.Name(), ".incomplete") {
			incomplete = true
			return filepath.SkipAll
		}
		return nil
	})
	if incomplete {
		return false
	}
	hasFile, invalid := false, false
	_ = filepath.WalkDir(filepath.Join(repoRoot, "snapshots", revision), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			invalid = true
			return filepath.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			invalid = true
			return filepath.SkipAll
		}
		hasFile = true
		return nil
	})
	return hasFile && !invalid
}

func recipeCachedModelReady(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe) bool {
	if recipeModelInstalled(recipe) {
		return true
	}
	_, err := inspectRecipeModelManifest(ctx, runtime, recipe)
	return err == nil
}

func recipeCachedModelSetReady(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe) bool {
	for _, item := range recipeModelSet(recipe) {
		if !recipeCachedModelReady(ctx, runtime, item) {
			return false
		}
	}
	return true
}

// inspectRecipeModelManifest is the inexpensive UI/inventory path. It proves
// that the internally consistent manifest still maps to files of the expected
// sizes. Full SHA-256 verification is reserved for preparation and the final
// pre-switch gate so opening Model Manager never re-hashes hundreds of GB.
func inspectRecipeModelManifest(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe) (recipeArtifactManifest, error) {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok || strings.TrimSpace(recipe.Model.Revision) == "" {
		return recipeArtifactManifest{}, errors.New("recipe model identity is incomplete")
	}
	mountpoint, err := recipeModelVolumeMountpoint(ctx, runtime, recipe)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	manifest, err := loadRecipeArtifactManifest(recipeModelCompleteMarker(recipe, mountpoint))
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	if manifest.ModelID != recipe.Model.ID || manifest.Revision != recipe.Model.Revision {
		return recipeArtifactManifest{}, errors.New("model artifact manifest identity does not match recipe")
	}
	snapshot := filepath.Join(mountpoint, "hub", cacheName, "snapshots", recipe.Model.Revision)
	for _, file := range manifest.Files {
		path := filepath.Join(snapshot, filepath.FromSlash(file.Path))
		if !pathWithin(path, snapshot) {
			return recipeArtifactManifest{}, errors.New("model artifact manifest contains an unsafe path")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return recipeArtifactManifest{}, errors.New("model artifact files are missing or incomplete")
		}
	}
	return manifest, nil
}

func recipeModelVolumeMountpoint(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe) (string, error) {
	_ = ctx
	_ = runtime
	cache, err := recipeCacheVolume(recipe)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(cache)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("model cache path is not a real directory")
	}
	return cache, nil
}

func verifyRecipeModelCache(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe) (recipeArtifactManifest, error) {
	return verifyRecipeModelCacheWithProgress(ctx, runtime, recipe, nil)
}

func verifyRecipeModelCacheWithProgress(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe, progress func(int64)) (recipeArtifactManifest, error) {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok || strings.TrimSpace(recipe.Model.Revision) == "" {
		return recipeArtifactManifest{}, errors.New("recipe model identity is incomplete")
	}
	mountpoint, err := recipeModelVolumeMountpoint(ctx, runtime, recipe)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	expected, err := loadRecipeArtifactManifest(recipeModelCompleteMarker(recipe, mountpoint))
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	if expected.ModelID != recipe.Model.ID || expected.Revision != recipe.Model.Revision {
		return recipeArtifactManifest{}, errors.New("model artifact manifest identity does not match recipe")
	}
	repoRoot := filepath.Join(mountpoint, "hub", cacheName)
	actual, err := buildRecipeArtifactManifestWithProgress(ctx, repoRoot, filepath.Join(repoRoot, "snapshots", recipe.Model.Revision), recipe.Model.ID, recipe.Model.Revision, progress)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	if actual.Digest != expected.Digest || actual.Bytes != expected.Bytes || len(actual.Files) != len(expected.Files) {
		return recipeArtifactManifest{}, errors.New("model artifact content does not match its verified manifest")
	}
	return actual, nil
}

func certifyRecipeModelCache(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe) (recipeArtifactManifest, error) {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok || strings.TrimSpace(recipe.Model.Revision) == "" {
		return recipeArtifactManifest{}, errors.New("recipe model identity is incomplete")
	}
	mountpoint, err := recipeModelVolumeMountpoint(ctx, runtime, recipe)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	repoRoot := filepath.Join(mountpoint, "hub", cacheName)
	manifest, err := buildRecipeArtifactManifest(repoRoot, filepath.Join(repoRoot, "snapshots", recipe.Model.Revision), recipe.Model.ID, recipe.Model.Revision)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	if err := saveRecipeArtifactManifest(recipeModelCompleteMarker(recipe, mountpoint), manifest); err != nil {
		return recipeArtifactManifest{}, err
	}
	return manifest, nil
}

var errRecipeModelCacheIncomplete = errors.New("existing recipe model cache is incomplete")

// certifyExistingRecipeModelCache adopts an exact revision downloaded through
// Model Manager (or another Cloudless workflow). The immutable Hub inventory
// proves that no file is absent or truncated before the local SHA-256 manifest
// is recorded. This turns certification into verification, never a download.
func certifyExistingRecipeModelCache(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe, inventory map[string]int64, progress func(int64)) (recipeArtifactManifest, error) {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok || strings.TrimSpace(recipe.Model.Revision) == "" {
		return recipeArtifactManifest{}, errors.New("recipe model identity is incomplete")
	}
	mountpoint, err := recipeModelVolumeMountpoint(ctx, runtime, recipe)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	repoRoot := filepath.Join(mountpoint, "hub", cacheName)
	manifest, err := buildRecipeArtifactManifestWithProgress(ctx, repoRoot,
		filepath.Join(repoRoot, "snapshots", recipe.Model.Revision), recipe.Model.ID, recipe.Model.Revision, progress)
	if err != nil {
		return recipeArtifactManifest{}, fmt.Errorf("inspect existing model snapshot: %w", err)
	}
	local := make(map[string]int64, len(manifest.Files))
	for _, file := range manifest.Files {
		local[file.Path] = file.Size
	}
	if len(local) != len(inventory) {
		return recipeArtifactManifest{}, fmt.Errorf("%w: expected %d files, found %d", errRecipeModelCacheIncomplete, len(inventory), len(local))
	}
	for path, expectedSize := range inventory {
		if actualSize, exists := local[path]; !exists || actualSize != expectedSize {
			return recipeArtifactManifest{}, fmt.Errorf("%w: %s", errRecipeModelCacheIncomplete, path)
		}
	}
	if err := saveRecipeArtifactManifest(recipeModelCompleteMarker(recipe, mountpoint), manifest); err != nil {
		return recipeArtifactManifest{}, fmt.Errorf("record existing model manifest: %w", err)
	}
	return manifest, nil
}

func buildRecipeLaunchPlan(ctx context.Context, runtime engine.Engine, recipe localrecipes.Recipe) recipeLaunchPlan {
	plan := recipeLaunchPlan{
		RequiresCustomRuntime: recipe.Runtime.Lifecycle.Build.Program != "",
		RuntimeImage:          recipe.Engine.Image,
		RuntimeEstimateBytes:  recipeRuntimeEstimate(recipe),
		RuntimeReason:         recipeRuntimeExplanation(recipe),
		ModelID:               recipe.Model.ID,
		BuildOnce:             recipe.Runtime.BuildOnce,
		DownloadOnce:          recipe.Runtime.DownloadOnce,
		Nodes:                 max(1, recipe.Distributed.Nodes),
	}
	if image, err := runtime.InspectImage(ctx, recipe.Engine.Image); err == nil {
		plan.RuntimeReady = true
		plan.RuntimeBytes = image.Size
	}
	plan.ModelInstalled = recipeModelInstalled(recipe)
	plan.ModelReady = recipeCachedModelReady(ctx, runtime, recipe)
	return plan
}
