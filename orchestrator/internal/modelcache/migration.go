// Package modelcache owns the host-visible Hugging Face cache used by
// CloudlessOS. It deliberately lives outside Docker's private volume tree so
// the unprivileged orchestrator can inspect progress without Docker or mount
// namespace authority.
package modelcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultRoot = "/var/lib/cloudless/models-cache"
	markerName  = ".cloudless-model-cache-v1.json"
)

// Root returns the configured host cache location. Packaged services set the
// same value explicitly; the fallback keeps local tests and developer runs
// deterministic.
func Root() string {
	if value := strings.TrimSpace(os.Getenv("CLOUDLESS_MODEL_CACHE")); value != "" {
		return filepath.Clean(value)
	}
	return DefaultRoot
}

type migrationMarker struct {
	Schema int    `json:"schema"`
	Source string `json:"source,omitempty"`
}

// Prepare creates root and, when source is an older Docker named-volume
// mountpoint, imports it exactly once. Existing source data is retained for a
// package rollback. Files are hard-linked when possible and atomically copied
// otherwise, so an interrupted import can safely resume.
func Prepare(root, source string, uid, gid int) error {
	root = filepath.Clean(strings.TrimSpace(root))
	source = filepath.Clean(strings.TrimSpace(source))
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return errors.New("model cache root must be an absolute non-root path")
	}
	if source == "." {
		source = ""
	}
	if source != "" {
		if !filepath.IsAbs(source) || source == string(filepath.Separator) {
			return errors.New("legacy model cache source must be an absolute non-root path")
		}
		if pathWithin(root, source) || pathWithin(source, root) {
			return errors.New("legacy model cache source and destination overlap")
		}
		if err := requireRealDirectory(source); err != nil {
			return fmt.Errorf("inspect legacy model cache: %w", err)
		}
	}
	if err := ensureDirectory(root, os.ModeSetgid|0o750, uid, gid); err != nil {
		return fmt.Errorf("prepare model cache root: %w", err)
	}
	markerPath := filepath.Join(root, markerName)
	if marker, err := readMarker(markerPath); err == nil && marker.Schema == 1 && marker.Source == source {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read model cache migration marker: %w", err)
	}
	if source != "" {
		if err := importTree(source, root, uid, gid); err != nil {
			return fmt.Errorf("import legacy model cache: %w", err)
		}
	}
	payload, err := json.Marshal(migrationMarker{Schema: 1, Source: source})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := writeAtomic(markerPath, payload, 0o640, uid, gid); err != nil {
		return fmt.Errorf("record model cache migration: %w", err)
	}
	return nil
}

func importTree(source, destination string, uid, gid int) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return ensureDirectory(target, directoryMode(info.Mode()), uid, gid)
		case entry.Type()&os.ModeSymlink != 0:
			return importSymlink(source, path, target, uid, gid)
		case info.Mode().IsRegular():
			return importFile(path, target, info.Mode(), uid, gid)
		default:
			return fmt.Errorf("unsupported cache entry %s (%s)", relative, info.Mode().Type())
		}
	})
}

func importSymlink(sourceRoot, source, destination string, uid, gid int) error {
	link, err := os.Readlink(source)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	if !pathWithin(resolved, sourceRoot) {
		return fmt.Errorf("cache symlink %s escapes its source", source)
	}
	if current, err := os.Readlink(destination); err == nil {
		if current != link {
			return fmt.Errorf("cache symlink conflict at %s", destination)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect cache symlink destination: %w", err)
	}
	if err := os.Symlink(link, destination); err != nil {
		return err
	}
	if uid >= 0 || gid >= 0 {
		if err := os.Lchown(destination, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func importFile(source, destination string, mode os.FileMode, uid, gid int) error {
	if current, err := os.Lstat(destination); err == nil {
		if !current.Mode().IsRegular() {
			return fmt.Errorf("cache file destination is not regular: %s", destination)
		}
		equal, compareErr := equalFiles(source, destination)
		if compareErr != nil {
			return compareErr
		}
		if !equal {
			return fmt.Errorf("cache file conflict at %s", destination)
		}
		if err := os.Chmod(destination, fileMode(mode)); err != nil {
			return err
		}
		return setOwnership(destination, uid, gid)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), os.ModeSetgid|0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".cloudless-import-*")
	if err != nil {
		return err
	}
	tempName := temporary.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempName)
		}
	}()
	if err := temporary.Close(); err != nil {
		return err
	}
	// A hard link preserves storage on the common /var/lib filesystem. If the
	// legacy Docker root lives elsewhere, replace it with an atomic byte copy.
	if err := os.Remove(tempName); err != nil {
		return err
	}
	if err := os.Link(source, tempName); err != nil {
		if err := copyFile(source, tempName, mode); err != nil {
			return err
		}
	}
	if err := os.Chmod(tempName, fileMode(mode)); err != nil {
		return err
	}
	if err := setOwnership(tempName, uid, gid); err != nil {
		return err
	}
	if err := os.Rename(tempName, destination); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode(mode))
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func equalFiles(left, right string) (bool, error) {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false, err
	}
	if leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	leftDigest, err := fileDigest(left)
	if err != nil {
		return false, err
	}
	rightDigest, err := fileDigest(right)
	if err != nil {
		return false, err
	}
	return leftDigest == rightDigest, nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeAtomic(path string, payload []byte, mode os.FileMode, uid, gid int) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cloudless-marker-*")
	if err != nil {
		return err
	}
	tempName := temporary.Name()
	defer os.Remove(tempName)
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempName, mode); err != nil {
		return err
	}
	if err := setOwnership(tempName, uid, gid); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func readMarker(path string) (migrationMarker, error) {
	var marker migrationMarker
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return marker, err
	}
	return marker, nil
}

func ensureDirectory(path string, mode os.FileMode, uid, gid int) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is not a real directory", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	return setOwnership(path, uid, gid)
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("path is not a real directory")
	}
	return nil
}

func setOwnership(path string, uid, gid int) error {
	if uid < 0 && gid < 0 {
		return nil
	}
	return os.Chown(path, uid, gid)
}

func directoryMode(mode os.FileMode) os.FileMode {
	// Keep every directory setgid so containers creating deeper cache entries
	// continue to inherit the read-only desktop group.
	return os.ModeSetgid | ((mode.Perm() | 0o750) & 0o750)
}

func fileMode(mode os.FileMode) os.FileMode {
	return (mode.Perm() | 0o640) & 0o750
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
