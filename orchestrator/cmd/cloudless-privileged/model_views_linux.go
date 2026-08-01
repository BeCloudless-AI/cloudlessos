//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/cloudless/orchestrator/internal/privileged"
)

const (
	managedModelCacheRoot = "/var/lib/cloudless/models-cache"
	managedModelsPath     = "/home/cloudless/Cloudless/Models"
)

type managedModelSnapshot struct {
	name     string
	revision string
	source   string
}

func reconcileManagedModelViews() error {
	return reconcileManagedModelViewsAt(managedModelCacheRoot, managedModelsPath)
}

// reconcileManagedModelViewsAt makes the user-visible Models directory an
// exact, read-only view of complete repositories in Cloudless's managed cache.
// It runs in the narrow root broker because Linux protected_hardlinks prevents
// cloudlessd from linking root-owned Hugging Face blobs even on one filesystem.
func reconcileManagedModelViewsAt(cacheRoot, modelsPath string) error {
	snapshots, err := completeManagedModelSnapshots(cacheRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(modelsPath, 0o755); err != nil {
		return fmt.Errorf("create Models directory: %w", err)
	}

	expected := make(map[string]bool, len(snapshots)*2)
	for _, snapshot := range snapshots {
		expected[snapshot.name] = true
		expected[snapshot.name+"-Cloudless"] = true
	}
	entries, err := os.ReadDir(modelsPath)
	if err != nil {
		return fmt.Errorf("read Models directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(modelsPath, name)
		if strings.HasPrefix(name, ".materializing-") {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove interrupted model view %s: %w", name, err)
			}
			continue
		}
		if expected[name] || !managedModelView(path) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove orphaned model view %s: %w", name, err)
		}
	}

	names := make([]string, 0, len(snapshots))
	for name := range snapshots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := materializeManagedModelView(cacheRoot, modelsPath, snapshots[name]); err != nil {
			return err
		}
	}
	return nil
}

func completeManagedModelSnapshots(cacheRoot string) (map[string]managedModelSnapshot, error) {
	result := map[string]managedModelSnapshot{}
	entries, err := os.ReadDir(filepath.Join(cacheRoot, "hub"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read managed model cache: %w", err)
	}
	for _, entry := range entries {
		rest, ok := strings.CutPrefix(entry.Name(), "models--")
		if !ok || !entry.IsDir() {
			continue
		}
		org, model, ok := strings.Cut(rest, "--")
		modelID := org + "/" + model
		if !ok || !privileged.ValidModelID(modelID) {
			continue
		}
		repoRoot := filepath.Join(cacheRoot, "hub", entry.Name())
		revisionBytes, readErr := os.ReadFile(filepath.Join(repoRoot, "refs", "main"))
		if readErr != nil {
			continue
		}
		revision := strings.TrimSpace(string(revisionBytes))
		if revision == "" || revision == "." || revision == ".." || filepath.Base(revision) != revision {
			continue
		}
		source := filepath.Join(repoRoot, "snapshots", revision)
		if stat, statErr := os.Stat(source); statErr != nil || !stat.IsDir() {
			continue
		}
		incomplete := false
		_ = filepath.WalkDir(filepath.Join(repoRoot, "blobs"), func(path string, item os.DirEntry, walkErr error) error {
			if walkErr == nil && !item.IsDir() && strings.HasSuffix(path, ".incomplete") {
				incomplete = true
			}
			return nil
		})
		if incomplete {
			continue
		}
		name := strings.ReplaceAll(modelID, "/", "--")
		result[name] = managedModelSnapshot{name: name, revision: revision, source: source}
	}
	return result, nil
}

func managedModelView(path string) bool {
	marker, err := os.Lstat(filepath.Join(path, ".cloudless-revision"))
	return err == nil && marker.Mode().IsRegular()
}

func materializeManagedModelView(cacheRoot, modelsPath string, snapshot managedModelSnapshot) error {
	destination := filepath.Join(modelsPath, snapshot.name)
	if _, err := os.Lstat(destination); err == nil && !managedModelView(destination) {
		destination += "-Cloudless"
	}
	if marker, err := os.ReadFile(filepath.Join(destination, ".cloudless-revision")); err == nil && strings.TrimSpace(string(marker)) == snapshot.revision {
		return nil
	}
	if _, err := os.Lstat(destination); err == nil {
		if !managedModelView(destination) {
			return fmt.Errorf("Models folder entry is reserved by the user: %s", destination)
		}
		if err := os.RemoveAll(destination); err != nil {
			return fmt.Errorf("replace stale model view %s: %w", snapshot.name, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	temporary := filepath.Join(modelsPath, ".materializing-"+snapshot.name)
	_ = os.RemoveAll(temporary)
	if err := hardlinkManagedModelTree(cacheRoot, snapshot.source, temporary); err != nil {
		_ = os.RemoveAll(temporary)
		return fmt.Errorf("expose %s in Models: %w", strings.ReplaceAll(snapshot.name, "--", "/"), err)
	}
	if err := os.WriteFile(filepath.Join(temporary, ".cloudless-revision"), []byte(snapshot.revision+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	if err := os.Chmod(filepath.Join(temporary, ".cloudless-revision"), 0o644); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.RemoveAll(temporary)
		return fmt.Errorf("publish model view %s: %w", snapshot.name, err)
	}
	return nil
}

func hardlinkManagedModelTree(cacheRoot, source, destination string) error {
	cacheRoot, err := filepath.Abs(cacheRoot)
	if err != nil {
		return err
	}
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
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			return os.Chmod(target, 0o755)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(cacheRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("model snapshot link escapes the managed cache: %s", path)
		}
		if err := os.Link(resolved, target); err != nil {
			if !errors.Is(err, syscall.EXDEV) {
				return fmt.Errorf("hard-link %s: %w", entry.Name(), err)
			}
			// ProtectSystem/ReadWritePaths presents the two writable roots as
			// separate mounts inside the broker, even when both live on the same
			// ext4 filesystem. An immutable leaf symlink preserves the same
			// zero-copy view without weakening the broker sandbox.
			if err := os.Symlink(resolved, target); err != nil {
				return fmt.Errorf("link %s across protected mounts: %w", entry.Name(), err)
			}
		}
		return nil
	})
}
