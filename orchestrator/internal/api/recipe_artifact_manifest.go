package api

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const recipeArtifactManifestVersion = 1

type recipeArtifactFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type recipeArtifactManifest struct {
	Version  int                  `json:"version"`
	ModelID  string               `json:"modelId"`
	Revision string               `json:"revision"`
	Files    []recipeArtifactFile `json:"files"`
	Bytes    int64                `json:"bytes"`
	Digest   string               `json:"digest"`
}

// buildRecipeArtifactManifest hashes the exact files visible through a
// Hugging Face snapshot. Snapshot symlinks are permitted only when they stay
// inside the containing repository; device files, sockets and named pipes are
// rejected. The sorted manifest makes verification identical on every node.
func buildRecipeArtifactManifest(repoRoot, snapshotRoot, modelID, revision string) (recipeArtifactManifest, error) {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	snapshotRoot, err = filepath.Abs(snapshotRoot)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	if !pathWithin(snapshotRoot, repoRoot) {
		return recipeArtifactManifest{}, errors.New("model snapshot is outside its repository")
	}
	info, err := os.Stat(snapshotRoot)
	if err != nil || !info.IsDir() {
		return recipeArtifactManifest{}, errors.New("model snapshot is unavailable")
	}

	manifest := recipeArtifactManifest{Version: recipeArtifactManifestVersion, ModelID: modelID, Revision: revision}
	err = filepath.WalkDir(snapshotRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(snapshotRoot, path)
		if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return errors.New("unsafe model snapshot path")
		}
		resolved := path
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, err = filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolve model snapshot link %s: %w", filepath.ToSlash(relative), err)
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil || !pathWithin(resolved, repoRoot) {
				return fmt.Errorf("model snapshot link escapes its repository: %s", filepath.ToSlash(relative))
			}
		}
		fileInfo, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("model snapshot contains a non-regular file: %s", filepath.ToSlash(relative))
		}
		digest, err := hashRecipeArtifactFile(resolved)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, recipeArtifactFile{Path: filepath.ToSlash(relative), Size: fileInfo.Size(), SHA256: digest})
		manifest.Bytes += fileInfo.Size()
		return nil
	})
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	if len(manifest.Files) == 0 {
		return recipeArtifactManifest{}, errors.New("model snapshot contains no files")
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	manifest.Digest, err = digestRecipeArtifactManifest(manifest)
	return manifest, err
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func hashRecipeArtifactFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, bufio.NewReaderSize(file, 1024*1024)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestRecipeArtifactManifest(manifest recipeArtifactManifest) (string, error) {
	hash := sha256.New()
	for _, file := range manifest.Files {
		if strings.ContainsRune(file.Path, '\x00') || !regexpSHA256.MatchString(file.SHA256) {
			return "", errors.New("invalid model artifact manifest")
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%s\n", file.Path, file.Size, file.SHA256)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func saveRecipeArtifactManifest(path string, manifest recipeArtifactManifest) error {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func loadRecipeArtifactManifest(path string) (recipeArtifactManifest, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return recipeArtifactManifest{}, err
	}
	var manifest recipeArtifactManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return recipeArtifactManifest{}, err
	}
	if manifest.Version != recipeArtifactManifestVersion || !regexpSHA256.MatchString(manifest.Digest) {
		return recipeArtifactManifest{}, errors.New("unsupported model artifact manifest")
	}
	computed, err := digestRecipeArtifactManifest(manifest)
	if err != nil || computed != manifest.Digest {
		return recipeArtifactManifest{}, errors.New("model artifact manifest integrity check failed")
	}
	var bytes int64
	for _, file := range manifest.Files {
		if file.Size < 0 {
			return recipeArtifactManifest{}, errors.New("model artifact manifest contains an invalid size")
		}
		bytes += file.Size
	}
	if bytes != manifest.Bytes {
		return recipeArtifactManifest{}, errors.New("model artifact manifest byte total is invalid")
	}
	return manifest, nil
}

var regexpSHA256 = regexp.MustCompile(`^[a-f0-9]{64}$`)
