// Package apps holds embedded Docker build contexts for apps that have no
// upstream image — built locally and pre-configured to use Cloudless AI.
package apps

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudless/orchestrator/internal/catalog"
)

//go:embed openclaw hermes
var buildFS embed.FS

// ReadDefault returns the embedded default content of an app's config file
// (e.g. ReadDefault("openclaw", "openclaw.json")).
func ReadDefault(appID, file string) ([]byte, error) {
	return buildFS.ReadFile(appID + "/" + file)
}

// EnvOverrides reads the app's env-injected config files (ConfigFile.Env) from
// configDir (the app's state dir; falling back to the embedded default), parses
// their KEY=VALUE lines, and returns them to overlay onto the container env. This
// is the single source of truth used by BOTH the API (on a settings change) and
// the boot provisioner, so an env setting like WEBUI_AUTH survives a reboot.
// Best-effort: unreadable files yield no overrides.
func EnvOverrides(configDir string, app catalog.App) map[string]string {
	out := map[string]string{}
	for _, cf := range app.Config {
		if !cf.Env {
			continue
		}
		content := ""
		if b, err := os.ReadFile(filepath.Join(configDir, cf.File)); err == nil {
			content = string(b)
		} else if b, err := ReadDefault(app.ID, cf.File); err == nil {
			content = string(b)
		}
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				if k = strings.TrimSpace(k); k != "" {
					out[k] = strings.TrimSpace(v)
				}
			}
		}
	}
	return out
}

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
