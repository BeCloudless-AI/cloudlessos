// Package apps holds embedded Docker build contexts for apps that have no
// upstream image — built locally and pre-configured to use Cloudless AI.
package apps

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
)

//go:embed openclaw hermes nemo-rl data-designer molt axolotl locateanything
var buildFS embed.FS

var configMu sync.Mutex

const HermesAPIKeyEnv = "API_SERVER_KEY"

// ReadDefault returns the embedded default content of an app's config file
// (e.g. ReadDefault("openclaw", "openclaw.json")).
func ReadDefault(appID, file string) ([]byte, error) {
	return buildFS.ReadFile(appID + "/" + file)
}

// ResetHermesModel restores only Hermes' model block to the CloudlessOS
// defaults. The rest of config.yaml, hermes.env, memory, sessions and workspace
// are deliberately left untouched.
func ResetHermesModel(configDir string) error {
	configMu.Lock()
	defer configMu.Unlock()

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	defaults, err := ReadDefault("hermes", "config.yaml")
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, "config.yaml")
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		current = defaults
	} else if err != nil {
		return err
	}
	modelBlock, ok := yamlTopLevelBlock(string(defaults), "model")
	if !ok {
		return fmt.Errorf("embedded Hermes config has no model block")
	}
	updated := replaceYAMLTopLevelBlock(string(current), "model", modelBlock)
	tmp := path + ".cloudless-reset"
	if err := os.WriteFile(tmp, []byte(updated), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func yamlTopLevelBlock(content, key string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		if line == key+":" {
			start = i
			continue
		}
		if start >= 0 && line != "" && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(line, "#") {
			end = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	return strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n") + "\n", true
}

func replaceYAMLTopLevelBlock(content, key, replacement string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	old, ok := yamlTopLevelBlock(normalized, key)
	if !ok {
		return strings.TrimRight(replacement, "\n") + "\n\n" + strings.TrimLeft(normalized, "\n")
	}
	return strings.Replace(normalized, old, replacement, 1)
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
	// Hermes' credential belongs to Cloudless, not to Hermes' writable data
	// directory. The official container takes ownership of /opt/data, which can
	// make files there unreadable to an unprivileged development daemon after
	// the first launch. Keep the credential in daemon-owned state and inject it
	// directly into the container environment on every start.
	if app.ID == "hermes" {
		if key, err := HermesAPIKey(configDir); err == nil {
			out[HermesAPIKeyEnv] = key
		}
	}
	if app.ID == "searxng" {
		if key, err := managedSecret(configDir, "searxng-secret", "cloudless-search-"); err == nil {
			out["SEARXNG_SECRET"] = key
		}
	}
	return out
}

// ConfigVolumes seeds an app's complete editable configuration and returns the
// mounts required by its container. Stateful apps mount the containing data
// directory so their own UI can persist sessions and settings alongside it.
func ConfigVolumes(configDir string, app catalog.App) (map[string]string, error) {
	configMu.Lock()
	defer configMu.Unlock()

	vols := map[string]string{}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	for _, cf := range app.Config {
		host := filepath.Join(configDir, cf.File)
		if _, err := os.Stat(host); os.IsNotExist(err) {
			def, derr := ReadDefault(app.ID, cf.File)
			if derr != nil {
				return nil, fmt.Errorf("default %s/%s: %w", app.ID, cf.File, derr)
			}
			if err := os.WriteFile(host, def, 0o644); err != nil {
				return nil, fmt.Errorf("seed %s: %w", host, err)
			}
		}
		if app.DataUID > 0 {
			// cloudlessd runs as root on the appliance. Chown may fail in a
			// non-root development environment, where no UID translation is needed.
			_ = os.Chown(host, app.DataUID, app.DataUID)
			_ = os.Chmod(host, 0o600)
		}
		if cf.Env || app.DataPath != "" {
			continue
		}
		vols[host] = cf.Path
	}
	if app.DataPath != "" {
		if app.ID == "hermes" {
			workspace := filepath.Join(configDir, "workspace")
			if err := os.MkdirAll(workspace, 0o700); err != nil && !os.IsPermission(err) {
				return nil, err
			}
			_ = os.Chmod(workspace, 0o700)
			if app.DataUID > 0 {
				_ = os.Chown(workspace, app.DataUID, app.DataUID)
			}
		}
		_ = os.Chmod(configDir, 0o700)
		if app.DataUID > 0 {
			_ = os.Chown(configDir, app.DataUID, app.DataUID)
		}
		vols[configDir] = app.DataPath
	}
	return vols, nil
}

// HermesAPIKey returns the stable, randomly generated loopback credential used
// between cloudlessd and Hermes. It is stored outside Hermes' container-writable
// data mount and is never returned to the browser or clients.
func HermesAPIKey(configDir string) (string, error) {
	return managedSecret(configDir, "hermes-api-key", "cloudless-hermes-")
}

func managedSecret(configDir, name, prefix string) (string, error) {
	configMu.Lock()
	defer configMu.Unlock()
	stateDir := filepath.Dir(filepath.Dir(filepath.Clean(configDir)))
	secretDir := filepath.Join(stateDir, "secrets")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(secretDir, name)
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := prefix + hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", err
	}
	_ = os.Chmod(secretDir, 0o700)
	_ = os.Chmod(path, 0o600)
	return secret, nil
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

// EnsureBuild makes an embedded adapter image available without rebuilding it
// on every daemon restart. The image contents are part of the signed Cloudless
// package; no remote build scripts are executed.
func EnsureBuild(ctx context.Context, eng engine.Engine, build, image string, onLine func(string)) error {
	if build == "" || image == "" {
		return nil
	}
	if _, err := eng.Output(ctx, "image", "inspect", image); err == nil {
		return nil
	}
	dir, err := Materialize(build)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return eng.Build(ctx, image, dir, onLine)
}
