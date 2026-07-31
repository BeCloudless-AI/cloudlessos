package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

type recipeGCEngine struct {
	engine.Engine
	volume     string
	mountpoint string
	removed    []string
}

func (e *recipeGCEngine) ListVolumes(context.Context) ([]string, error) {
	if e.volume == "" {
		return nil, nil
	}
	return []string{e.volume}, nil
}

func (e *recipeGCEngine) VolumeMountpoint(context.Context, string) (string, error) {
	return e.mountpoint, nil
}

func (e *recipeGCEngine) RemoveVolume(_ context.Context, volume string) error {
	e.removed = append(e.removed, volume)
	return os.RemoveAll(e.mountpoint)
}

func (e *recipeGCEngine) Output(_ context.Context, args ...string) (string, error) {
	command := strings.Join(args, " ")
	switch {
	case command == "volume ls --format {{.Name}}":
		return e.volume + "\n", nil
	case strings.HasPrefix(command, "volume inspect "):
		return e.mountpoint + "\n", nil
	case strings.HasPrefix(command, "volume rm "):
		e.removed = append(e.removed, args[len(args)-1])
		return "", os.RemoveAll(e.mountpoint)
	case strings.HasPrefix(command, "ps -aq --filter ancestor="):
		return "", nil
	case strings.HasPrefix(command, "image inspect "):
		return "4096\n", nil
	default:
		return "", fmt.Errorf("unexpected engine command %q", command)
	}
}

func (e *recipeGCEngine) RemoveImage(_ context.Context, image string) error {
	e.removed = append(e.removed, image)
	return nil
}

func (e *recipeGCEngine) InspectImage(context.Context, string) (engine.ImageInfo, error) {
	return engine.ImageInfo{Size: 4096}, nil
}

func (e *recipeGCEngine) ContainerNamesByAncestor(context.Context, string) ([]string, error) {
	return nil, nil
}

func TestRecipeGCReferencesCurrentAndUnreconciledOperations(t *testing.T) {
	current := localrecipes.Recipe{ID: "current", Model: localrecipes.Model{ID: "org/current", Revision: "aaa"}}
	orphan := localrecipes.Recipe{ID: "orphan", Model: localrecipes.Model{ID: "org/orphan", Revision: "bbb"}}
	unresolved := localrecipes.Recipe{ID: "unresolved", Model: localrecipes.Model{ID: "org/unresolved", Revision: "ccc"}}
	refs := recipeGCReferenceSet([]localrecipes.Recipe{current}, []recipeops.Operation{
		{ID: "completed", Phase: recipeops.PhaseFailed, RecipeSnapshot: orphan},
		{ID: "blocked", Phase: recipeops.PhaseFailed, RecipeSnapshot: unresolved, Resources: []recipeops.Resource{{Kind: "process-set", ID: "blocked"}}},
	})
	if _, ok := refs.ArtifactKeys[recipeModelArtifactKey(current)]; !ok {
		t.Fatal("current recipe artifact is not protected")
	}
	if _, ok := refs.ArtifactKeys[recipeModelArtifactKey(unresolved)]; !ok {
		t.Fatal("unreconciled recipe artifact is not protected")
	}
	if _, ok := refs.ArtifactKeys[recipeModelArtifactKey(orphan)]; ok {
		t.Fatal("reconciled terminal recipe artifact remains protected forever")
	}
}

func TestCollectOrphanRecipeDirectoriesKeepsProtectedAndRecent(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-40 * 24 * time.Hour)
	for _, name := range []string{"protected", "old", "recent"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "payload"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"protected", "old"} {
		if err := os.Chtimes(filepath.Join(root, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := collectOrphanRecipeDirectories(root, "checkout", map[string]struct{}{"protected": {}}, time.Now().Add(-30*24*time.Hour))
	if err != nil || len(candidates) != 1 || candidates[0].Identity != "old" {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
	if err := candidates[0].remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old")); !os.IsNotExist(err) {
		t.Fatalf("orphan checkout still exists: %v", err)
	}
}

func TestObsoleteRecipeSnapshotRemovesOnlyUnreferencedContent(t *testing.T) {
	cache := t.TempDir()
	protected := localrecipes.Recipe{ID: "protected", Model: localrecipes.Model{ID: "org/model", Revision: "protected-revision"}}
	obsolete := localrecipes.Recipe{ID: "obsolete", Model: localrecipes.Model{ID: "org/model", Revision: "obsolete-revision"}}
	repository := filepath.Join(cache, "hub", "models--org--model")
	blobs := filepath.Join(repository, "blobs")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	makeSnapshot := func(recipe localrecipes.Recipe, blob string) recipeArtifactManifest {
		if err := os.WriteFile(filepath.Join(blobs, blob), []byte(blob), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(blobs, blob), old, old); err != nil {
			t.Fatal(err)
		}
		snapshot := filepath.Join(repository, "snapshots", recipe.Model.Revision)
		if err := os.MkdirAll(snapshot, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "..", "blobs", blob), filepath.Join(snapshot, "weights.bin")); err != nil {
			t.Fatal(err)
		}
		manifest, err := buildRecipeArtifactManifest(repository, snapshot, recipe.Model.ID, recipe.Model.Revision)
		if err != nil {
			t.Fatal(err)
		}
		marker := recipeModelCompleteMarker(recipe, cache)
		if err := saveRecipeArtifactManifest(marker, manifest); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(marker, old, old); err != nil {
			t.Fatal(err)
		}
		return manifest
	}
	makeSnapshot(protected, "protected-blob")
	makeSnapshot(obsolete, "obsolete-blob")
	refs := recipeGCReferenceSet([]localrecipes.Recipe{protected}, nil)
	candidates, err := collectObsoleteRecipeSnapshots(cache, refs, time.Now().Add(-30*24*time.Hour))
	if err != nil || len(candidates) != 1 || candidates[0].Identity != "org/model@obsolete-revision" {
		t.Fatalf("snapshot candidates = %#v, %v", candidates, err)
	}
	if err := candidates[0].remove(); err != nil {
		t.Fatal(err)
	}
	for path, exists := range map[string]bool{
		filepath.Join(repository, "snapshots", protected.Model.Revision): true,
		filepath.Join(blobs, "protected-blob"):                           true,
		filepath.Join(repository, "snapshots", obsolete.Model.Revision):  false,
		filepath.Join(blobs, "obsolete-blob"):                            false,
	} {
		_, err := os.Stat(path)
		if exists && err != nil {
			t.Fatalf("protected path %s was removed: %v", path, err)
		}
		if !exists && !os.IsNotExist(err) {
			t.Fatalf("obsolete path %s remains: %v", path, err)
		}
	}
}

func TestHuggingFaceRefProtectsOtherwiseObsoleteSnapshot(t *testing.T) {
	cache := t.TempDir()
	recipe := localrecipes.Recipe{ID: "old", Model: localrecipes.Model{ID: "org/model", Revision: "commit"}}
	repository := filepath.Join(cache, "hub", "models--org--model")
	snapshot := filepath.Join(repository, "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "weights.bin"), []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := buildRecipeArtifactManifest(repository, snapshot, recipe.Model.ID, recipe.Model.Revision)
	if err != nil {
		t.Fatal(err)
	}
	marker := recipeModelCompleteMarker(recipe, cache)
	if err := saveRecipeArtifactManifest(marker, manifest); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "refs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "refs", "main"), []byte(recipe.Model.Revision+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates, err := collectObsoleteRecipeSnapshots(cache, recipeGCReferences{ArtifactKeys: map[string]struct{}{}, Models: map[string]struct{}{}}, time.Now().Add(-30*24*time.Hour))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("referenced snapshot candidates = %#v, %v", candidates, err)
	}
}

func TestRecipeGCRemovesOnlyUnprotectedStagingVolumes(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", cache)
	root := filepath.Join(cache, ".download-staging", "deadbeefdeadbeefdeadbeef")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(root, old, old); err != nil {
		t.Fatal(err)
	}
	server := &Server{}
	candidates, err := server.collectRecipeStagingVolumes(context.Background(), recipeGCReferences{ArtifactKeys: map[string]struct{}{}}, time.Now().Add(-7*24*time.Hour))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("staging candidates = %#v, %v", candidates, err)
	}
	if err := candidates[0].remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory still exists: %v", err)
	}
}

func TestRecipeGCRemovesOnlyOrphanedCustomRuntimeImages(t *testing.T) {
	old := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	orphan := localrecipes.NewDraft()
	orphan.Engine.Image = "cloudless/orphan-runtime:test"
	orphan.Runtime.Lifecycle.Build.Program = "docker"
	orphanRecipe := localrecipes.Recipe{ID: "orphan", Engine: orphan.Engine, Runtime: orphan.Runtime}
	protectedRecipe := orphanRecipe
	protectedRecipe.ID, protectedRecipe.Engine.Image = "protected", "cloudless/protected-runtime:test"
	engine := &recipeGCEngine{}
	server := &Server{eng: engine}
	operations := []recipeops.Operation{
		{ID: "old-orphan", Phase: recipeops.PhaseFailed, UpdatedAt: old, RecipeSnapshot: orphanRecipe},
		{ID: "old-protected", Phase: recipeops.PhaseFailed, UpdatedAt: old, RecipeSnapshot: protectedRecipe},
	}
	candidates, err := server.collectObsoleteRecipeImages(context.Background(), []localrecipes.Recipe{protectedRecipe}, operations, time.Now().Add(-30*24*time.Hour))
	if err != nil || len(candidates) != 1 || candidates[0].Identity != orphanRecipe.Engine.Image {
		t.Fatalf("image candidates = %#v, %v", candidates, err)
	}
	if err := candidates[0].remove(); err != nil {
		t.Fatal(err)
	}
	if len(engine.removed) != 1 || engine.removed[0] != orphanRecipe.Engine.Image {
		t.Fatalf("removed images = %v", engine.removed)
	}
}
