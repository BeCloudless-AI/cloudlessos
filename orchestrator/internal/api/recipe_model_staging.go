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

// seedRecipeDownloadStaging reuses every existing blob from the exact model
// repository through hard links. Hugging Face can then resume partial blobs
// and fetch only absent data; no second local copy and no full redownload are
// needed when a snapshot merely lacks a file or its Cloudless certificate.
func seedRecipeDownloadStaging(recipe localrecipes.Recipe, stagingRoot string) error {
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok {
		return errors.New("recipe model ID cannot be mapped to a Hugging Face cache")
	}
	finalRoot, err := recipeCacheVolume(recipe)
	if err != nil {
		return err
	}
	source := filepath.Join(finalRoot, "hub", cacheName)
	if info, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return errors.New("existing model cache repository is not a directory")
	}
	destination := filepath.Join(stagingRoot, "hub", cacheName)
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if _, err := os.Lstat(target); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || !pathWithin(resolved, source) {
				return fmt.Errorf("existing model cache contains an unsafe link: %s", relative)
			}
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("existing model cache contains an unsupported file: %s", relative)
		}
		if err := os.Link(path, target); err != nil {
			return fmt.Errorf("hard-link existing model data %s: %w", relative, err)
		}
		return nil
	})
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
