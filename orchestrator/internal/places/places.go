// Package places defines well-known folders the UI can open in the host file
// manager (the "open a specific folder" capability of the OS home screen).
package places

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Place is a well-known folder.
type Place struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// base resolves the Cloudless data root: $CLOUDLESS_HOME, else ~/Cloudless.
func base() string {
	if d := os.Getenv("CLOUDLESS_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, "Cloudless")
}

type def struct{ id, label, path string }

func defs() []def {
	b := base()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = b
	}
	return []def{
		{"models", "Models", filepath.Join(b, "Models")},
		{"outputs", "Outputs", filepath.Join(b, "Outputs")},
		{"workspace", "Workspace", filepath.Join(b, "Workspace")},
		{"downloads", "Downloads", filepath.Join(home, "Downloads")},
	}
}

// List returns the known places with their on-disk existence.
func List() []Place {
	out := []Place{}
	for _, d := range defs() {
		_, err := os.Stat(d.path)
		out = append(out, Place{ID: d.id, Label: d.label, Path: d.path, Exists: err == nil})
	}
	return out
}

// Get returns the place with the given id.
func Get(id string) (Place, bool) {
	for _, p := range List() {
		if p.ID == id {
			return p, true
		}
	}
	return Place{}, false
}

// Open ensures the folder exists and opens it in the host file manager via
// xdg-open. Returns an error if no file manager is available (e.g. headless dev).
func Open(p Place) error {
	if err := os.MkdirAll(p.Path, 0o755); err != nil {
		return err
	}
	bin, err := exec.LookPath("xdg-open")
	if err != nil {
		return fmt.Errorf("no file manager available (xdg-open not found)")
	}
	return exec.Command(bin, p.Path).Start()
}
