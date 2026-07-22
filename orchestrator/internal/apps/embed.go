// Package apps holds embedded Docker build contexts for apps that have no
// upstream image — built locally and pre-configured to use Cloudless AI.
package apps

import (
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
)

//go:embed openclaw hermes
var buildFS embed.FS

var configMu sync.Mutex

const HermesAPIKeyEnv = "API_SERVER_KEY"

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
		if app.ID == "hermes" && cf.File == "hermes.env" {
			if _, err := ensureEnvSecretLocked(host, HermesAPIKeyEnv, "cloudless-hermes-"); err != nil {
				return nil, err
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
			if err := os.MkdirAll(workspace, 0o700); err != nil {
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
// between cloudlessd and Hermes. It is never returned to the browser or clients.
func HermesAPIKey(configDir string) (string, error) {
	configMu.Lock()
	defer configMu.Unlock()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(configDir, "hermes.env")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		def, derr := ReadDefault("hermes", "hermes.env")
		if derr != nil {
			return "", derr
		}
		if err := os.WriteFile(path, def, 0o600); err != nil {
			return "", err
		}
	}
	return ensureEnvSecretLocked(path, HermesAPIKeyEnv, "cloudless-hermes-")
}

func ensureEnvSecretLocked(path, key, prefix string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(b)
	for _, line := range strings.Split(content, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k == key && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := prefix + hex.EncodeToString(raw)
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	out = append(out, key+"="+secret)
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
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
