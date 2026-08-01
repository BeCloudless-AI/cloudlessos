// Package places defines well-known folders the UI can open in the host file
// manager (the "open a specific folder" capability of the OS home screen).
package places

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"time"

	"github.com/cloudless/orchestrator/internal/desktop"
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
	home := desktopHome(b)
	return []def{
		{"models", "Models", filepath.Join(b, "Models")},
		{"outputs", "Outputs", filepath.Join(b, "Outputs")},
		{"workspace", "Workspace", filepath.Join(b, "Workspace")},
		{"downloads", "Downloads", filepath.Join(home, "Downloads")},
	}
}

func desktopHome(fallback string) string {
	if home := os.Getenv("CLOUDLESS_DESKTOP_HOME"); home != "" {
		return home
	}
	if name := os.Getenv("CLOUDLESS_DESKTOP_USER"); name != "" {
		if u, err := user.Lookup(name); err == nil && u.HomeDir != "" {
			return u.HomeDir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fallback
	}
	return home
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

// Open ensures the folder exists and opens it in the desktop user's graphical
// session. Installed CloudlessOS systems set CLOUDLESS_DESKTOP_USER; local
// development environments retain the conventional xdg-open fallback.
func Open(p Place) error {
	if desktopUser := os.Getenv("CLOUDLESS_DESKTOP_USER"); desktopUser != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := desktop.NewClient().OpenPlace(ctx, p.ID); err != nil {
			return fmt.Errorf("open folder in desktop session: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(p.Path, 0o755); err != nil {
		return err
	}
	bin, err := exec.LookPath("xdg-open")
	if err != nil {
		return fmt.Errorf("no file manager available (xdg-open not found)")
	}
	return exec.Command(bin, p.Path).Start()
}
