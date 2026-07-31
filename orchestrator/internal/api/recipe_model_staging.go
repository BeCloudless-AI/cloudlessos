package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
)

func recipeStagingCacheVolume(recipe localrecipes.Recipe) string {
	return filepath.Join(modelcache.Root(), ".download-staging", recipeModelArtifactKey(recipe)[:24])
}

func recipeWithCacheVolume(recipe localrecipes.Recipe, volume string) localrecipes.Recipe {
	copy := recipe
	copy.Runtime.Environment = make(map[string]string, len(recipe.Runtime.Environment)+1)
	for key, value := range recipe.Runtime.Environment {
		copy.Runtime.Environment[key] = value
	}
	copy.Runtime.Environment["HF_CACHE"] = volume
	return copy
}

// promoteStagedRecipeModel copies a verified, resumable download into a
// staging tree on the final Docker volume. Only after a second verification is
// the repository atomically exchanged. The previous cache remains available
// until that final rename and is restored if the exchange fails.
func promoteStagedRecipeModel(ctx context.Context, runtime engine.Engine, job *jobs.Job, recipe, stagedRecipe localrecipes.Recipe, sourceManifest recipeArtifactManifest) error {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok {
		return errors.New("recipe model ID cannot be mapped to a Hugging Face cache")
	}
	sourceMount, err := recipeModelVolumeMountpoint(ctx, runtime, stagedRecipe)
	if err != nil {
		return err
	}
	finalCache, err := recipeCacheVolume(recipe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(finalCache, 0o770); err != nil {
		return fmt.Errorf("create final model cache: %w", err)
	}
	finalMount, err := recipeModelVolumeMountpoint(ctx, runtime, recipe)
	if err != nil {
		return err
	}
	key := recipeModelArtifactKey(recipe)
	sourceRepo := filepath.Join(sourceMount, "hub", cacheName)
	finalRepo := filepath.Join(finalMount, "hub", cacheName)
	stagingRepo := filepath.Join(finalMount, ".cloudless-staging", key, "hub", cacheName)
	backupRepo := filepath.Join(finalMount, ".cloudless-backup", key, "hub", cacheName)
	if err := os.MkdirAll(stagingRepo, 0o700); err != nil {
		return err
	}
	// Preserve other cached revisions while filling only absent or changed
	// content. Rsync's partial directory survives cancellation and is reused by
	// the next operation instead of restarting large files from byte zero.
	if info, statErr := os.Stat(finalRepo); statErr == nil && info.IsDir() {
		if err := runRecipeRsync(ctx, finalRepo, stagingRepo, "--ignore-existing"); err != nil {
			return fmt.Errorf("seed model promotion staging: %w", err)
		}
	}
	job.Progress("promoting-model", "Resuming and verifying the prepared model cache...", -1, -1)
	if err := runRecipeRsync(ctx, sourceRepo, stagingRepo, "--checksum"); err != nil {
		return fmt.Errorf("copy verified model into promotion staging: %w", err)
	}
	stagedManifest, err := buildRecipeArtifactManifest(stagingRepo, filepath.Join(stagingRepo, "snapshots", recipe.Model.Revision), recipe.Model.ID, recipe.Model.Revision)
	if err != nil {
		return fmt.Errorf("verify promotion staging: %w", err)
	}
	if stagedManifest.Digest != sourceManifest.Digest || stagedManifest.Bytes != sourceManifest.Bytes {
		return errors.New("promoted model content differs from the verified download")
	}
	if err := atomicallyPromoteRecipeRepository(finalRepo, stagingRepo, backupRepo); err != nil {
		return err
	}
	if err := saveRecipeArtifactManifest(recipeModelCompleteMarker(recipe, finalMount), stagedManifest); err != nil {
		return fmt.Errorf("record promoted model manifest: %w", err)
	}
	return nil
}

func runRecipeRsync(ctx context.Context, source, destination string, extra ...string) error {
	args := []string{"-a", "--partial", "--partial-dir=.cloudless-rsync-partial"}
	args = append(args, extra...)
	args = append(args, filepath.Clean(source)+string(os.PathSeparator), filepath.Clean(destination)+string(os.PathSeparator))
	cmd := exec.CommandContext(ctx, "rsync", args...)
	configureRecipeTransferProcess(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 800 {
			message = message[len(message)-800:]
		}
		return fmt.Errorf("%w: %s", err, message)
	}
	return nil
}

func atomicallyPromoteRecipeRepository(finalRepo, stagingRepo, backupRepo string) error {
	if info, err := os.Stat(stagingRepo); err != nil || !info.IsDir() {
		return errors.New("verified model promotion staging is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(finalRepo), 0o700); err != nil {
		return err
	}
	if err := os.RemoveAll(backupRepo); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backupRepo), 0o700); err != nil {
		return err
	}
	hadFinal := false
	if _, err := os.Stat(finalRepo); err == nil {
		if err := os.Rename(finalRepo, backupRepo); err != nil {
			return fmt.Errorf("preserve previous model cache: %w", err)
		}
		hadFinal = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stagingRepo, finalRepo); err != nil {
		if hadFinal {
			_ = os.Rename(backupRepo, finalRepo)
		}
		return fmt.Errorf("activate verified model cache: %w", err)
	}
	if err := os.RemoveAll(backupRepo); err != nil {
		// Activation succeeded; a stale backup is safe and can be garbage
		// collected later. Do not roll back a working cache for cleanup failure.
		return nil
	}
	return nil
}
