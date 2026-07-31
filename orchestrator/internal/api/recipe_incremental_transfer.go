package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
)

type recipeTransferEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

type recipeTransferManifest struct {
	Entries []recipeTransferEntry `json:"entries"`
	Bytes   int64                 `json:"bytes"`
}

func buildRecipeTransferManifest(root string) (recipeTransferManifest, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return recipeTransferManifest{}, err
	}
	manifest := recipeTransferManifest{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || !safeRecipeTransferPath(relative) {
			return errors.New("unsafe model cache transfer path")
		}
		item := recipeTransferEntry{Path: filepath.ToSlash(relative)}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil || strings.ContainsRune(target, '\x00') || strings.ContainsAny(target, "\r\n") {
				return errors.New("unsafe model cache symlink")
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil || !pathWithin(resolved, root) {
				return fmt.Errorf("model cache symlink escapes repository: %s", item.Path)
			}
			item.Kind, item.Target = "symlink", filepath.ToSlash(target)
		} else {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("model cache contains non-regular entry: %s", item.Path)
			}
			item.Kind, item.Size = "file", info.Size()
			item.SHA256, err = hashRecipeArtifactFile(path)
			if err != nil {
				return err
			}
			manifest.Bytes += item.Size
		}
		manifest.Entries = append(manifest.Entries, item)
		return nil
	})
	if err != nil {
		return recipeTransferManifest{}, err
	}
	if len(manifest.Entries) == 0 {
		return recipeTransferManifest{}, errors.New("model cache transfer manifest is empty")
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	return manifest, nil
}

func safeRecipeTransferPath(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) &&
		!strings.HasPrefix(path, ".."+string(os.PathSeparator)) &&
		!strings.ContainsRune(path, '\x00') && !strings.ContainsAny(path, "\r\n")
}

func transferEntryEqual(left, right recipeTransferEntry) bool {
	return left.Path == right.Path && left.Kind == right.Kind && left.Size == right.Size &&
		left.SHA256 == right.SHA256 && left.Target == right.Target
}

func missingRecipeTransferEntries(expected, present recipeTransferManifest) ([]recipeTransferEntry, int64) {
	have := make(map[string]recipeTransferEntry, len(present.Entries))
	for _, entry := range present.Entries {
		have[entry.Path] = entry
	}
	missing := make([]recipeTransferEntry, 0)
	var bytes int64
	for _, entry := range expected.Entries {
		if current, ok := have[entry.Path]; ok && transferEntryEqual(entry, current) {
			continue
		}
		missing = append(missing, entry)
		if entry.Kind == "file" {
			bytes += entry.Size
		}
	}
	return missing, bytes
}

func remoteRecipeTransferManifest(ctx context.Context, dir string, env map[string]string, peer recipePeer, image, volume, root string) (recipeTransferManifest, error) {
	const scanner = `import hashlib, json, os, pathlib, sys
root = pathlib.Path(sys.argv[1])
entries = []
total = 0
if root.is_dir():
    for base, dirs, files in os.walk(root, followlinks=False):
        dirs[:] = [name for name in dirs if not (pathlib.Path(base) / name).is_symlink()]
        names = files + [name for name in os.listdir(base) if (pathlib.Path(base) / name).is_symlink()]
        for name in sorted(set(names)):
            path = pathlib.Path(base) / name
            relative = path.relative_to(root).as_posix()
            if path.is_symlink():
                entries.append({"path": relative, "kind": "symlink", "target": os.readlink(path)})
            elif path.is_file():
                digest = hashlib.sha256()
                with path.open("rb") as stream:
                    for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                        digest.update(chunk)
                size = path.stat().st_size
                total += size
                entries.append({"path": relative, "kind": "file", "size": size, "sha256": digest.hexdigest()})
print(json.dumps({"entries": sorted(entries, key=lambda item: item["path"]), "bytes": total}, separators=(",", ":")))`
	out, err := recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer,
		"docker", "run", "--rm", "-v", volume+":/cache:ro", "--entrypoint", "python3", image,
		"-c", scanner, root))
	if err != nil {
		return recipeTransferManifest{}, err
	}
	var manifest recipeTransferManifest
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		return recipeTransferManifest{}, err
	}
	for _, entry := range manifest.Entries {
		if !safeRecipeTransferPath(filepath.FromSlash(entry.Path)) ||
			(entry.Kind != "file" && entry.Kind != "symlink") ||
			(entry.Kind == "file" && (entry.Size < 0 || !regexpSHA256.MatchString(entry.SHA256))) ||
			(entry.Kind == "symlink" && (strings.ContainsRune(entry.Target, '\x00') || strings.ContainsAny(entry.Target, "\r\n"))) {
			return recipeTransferManifest{}, errors.New("peer returned an unsafe model cache manifest")
		}
	}
	return manifest, nil
}

func clearInvalidRemoteTransferEntries(ctx context.Context, dir string, env map[string]string, peer recipePeer, image, volume, root string, entries []recipeTransferEntry) error {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !safeRecipeTransferPath(filepath.FromSlash(entry.Path)) {
			return errors.New("unsafe invalid model cache path")
		}
		paths = append(paths, entry.Path)
	}
	payload, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	const cleanup = `import base64, json, os, pathlib, shutil, sys
root = pathlib.Path(sys.argv[1])
root.mkdir(parents=True, exist_ok=True)
for value in json.loads(base64.b64decode(sys.argv[2])):
    pure = pathlib.PurePosixPath(value)
    if pure.is_absolute() or ".." in pure.parts or not pure.parts:
        raise SystemExit("unsafe staging path")
    current = root
    for part in pure.parts[:-1]:
        current = current / part
        if current.is_symlink() or (current.exists() and not current.is_dir()):
            if current.is_dir() and not current.is_symlink(): shutil.rmtree(current)
            else: current.unlink()
        current.mkdir(exist_ok=True)
    target = current / pure.parts[-1]
    if target.is_symlink() or target.is_file(): target.unlink()
    elif target.exists(): shutil.rmtree(target)`
	_, err = recipeCommandOutput(recipeSSHCommand(ctx, dir, env, peer,
		"docker", "run", "--rm", "-v", volume+":/cache", "--entrypoint", "python3", image,
		"-c", cleanup, root, encoded))
	return err
}

func incrementalRecipePeerTransfer(ctx context.Context, job *jobs.Job, recipeRoot, cacheRelative, stagingRelative string, expected recipeTransferManifest, dir string, env map[string]string, peer recipePeer, image, volume, label string, base, total int64) error {
	remoteRoot := "/cache/" + stagingRelative
	present, err := remoteRecipeTransferManifest(ctx, dir, env, peer, image, volume, remoteRoot)
	if err != nil {
		return fmt.Errorf("inspect resumable staging on %s: %w", peer.Name, err)
	}
	missing, missingBytes := missingRecipeTransferEntries(expected, present)
	if len(missing) == 0 {
		job.ProgressBytes("syncing-model", peer.Name+" already has every staged model file.", base+expected.Bytes, total)
		return nil
	}
	if err := clearInvalidRemoteTransferEntries(ctx, dir, env, peer, image, volume, remoteRoot, missing); err != nil {
		return fmt.Errorf("prepare resumable staging on %s: %w", peer.Name, err)
	}
	list, err := os.CreateTemp("", ".cloudless-model-files-*")
	if err != nil {
		return err
	}
	listPath := list.Name()
	defer os.Remove(listPath)
	for _, entry := range missing {
		archivePath := filepath.ToSlash(filepath.Join(cacheRelative, filepath.FromSlash(entry.Path)))
		if !safeRecipeTransferPath(filepath.FromSlash(archivePath)) {
			list.Close()
			return errors.New("unsafe model cache archive path")
		}
		if _, err := list.Write(append([]byte(archivePath), 0)); err != nil {
			list.Close()
			return err
		}
	}
	if err := list.Close(); err != nil {
		return err
	}
	completedBytes := expected.Bytes - missingBytes
	producer := recipeLocalCommand(ctx, dir, env, "tar", "--null", "-C", recipeRoot, "-cf", "-", "-T", listPath)
	extractionRoot := "/cache/" + filepath.ToSlash(filepath.Dir(filepath.Dir(stagingRelative)))
	consumer := recipeSSHCommand(ctx, dir, env, peer, "docker", "run", "--rm", "-i", "-v", volume+":/cache",
		"--entrypoint", "/bin/sh", image, "-c", `set -eu; mkdir -p "$1"; exec tar -C "$1" -xf -`, "cloudless-cache-receive", extractionRoot)
	if err := runRecipeTransfer(ctx, job, "syncing-model", label, base+completedBytes, total, producer, consumer); err != nil {
		return err
	}
	after, err := remoteRecipeTransferManifest(ctx, dir, env, peer, image, volume, remoteRoot)
	if err != nil {
		return err
	}
	remaining, _ := missingRecipeTransferEntries(expected, after)
	if len(remaining) != 0 {
		return fmt.Errorf("%s resumable staging still has %d missing or invalid files", peer.Name, len(remaining))
	}
	return nil
}

func retryRecipePeerTransfer(ctx context.Context, job *jobs.Job, peerName string, transfer func() error) error {
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := transfer(); err == nil {
			return nil
		} else {
			last = err
		}
		if attempt == 3 {
			break
		}
		job.Progress("retrying-model-transfer", fmt.Sprintf("Connection to %s was interrupted. Keeping verified files and retrying (%d/3)...", peerName, attempt+1), -1, -1)
		timer := time.NewTimer(time.Duration(attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("model transfer to %s failed after 3 resumable attempts: %w", peerName, last)
}
