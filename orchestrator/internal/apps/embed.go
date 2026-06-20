// Package apps holds embedded Docker build contexts for apps that have no
// upstream image — built locally and pre-configured to use Cloudless AI.
package apps

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed openclaw hermes
var buildFS embed.FS

// Materialize writes the embedded build context named `name` (e.g. "openclaw")
// to a fresh temp directory and returns its path. The caller should
// os.RemoveAll the directory when the build is done.
func Materialize(name string) (string, error) {
	sub, err := fs.Sub(buildFS, name)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "cloudless-build-")
	if err != nil {
		return "", err
	}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(dir, p)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := fs.ReadFile(sub, p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}
