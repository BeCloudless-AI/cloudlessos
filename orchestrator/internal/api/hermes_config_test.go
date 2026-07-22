package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestHermesMountsPersistentDataDirectory(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hermes, ok := catalog.Get("hermes")
	if !ok {
		t.Fatal("Hermes is missing from the catalog")
	}

	s := &Server{state: st}
	volumes := s.configVolumes(hermes)
	wantDir := filepath.Join(st.Dir(), "apps", "hermes")
	if got := volumes[wantDir]; got != "/opt/data" {
		t.Fatalf("Hermes data volume = %q, want /opt/data", got)
	}
	if len(volumes) != 1 {
		t.Fatalf("Hermes should use one complete data mount, got %#v", volumes)
	}
	for _, name := range []string{"config.yaml", "hermes.env"} {
		if _, err := os.Stat(filepath.Join(wantDir, name)); err != nil {
			t.Fatalf("seeded %s: %v", name, err)
		}
	}
	key, err := apps.HermesAPIKey(wantDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "cloudless-hermes-") || len(key) < 50 {
		t.Fatalf("unexpected generated Hermes credential: %q", key)
	}
	again, err := apps.HermesAPIKey(wantDir)
	if err != nil || again != key {
		t.Fatalf("Hermes credential is not stable: %q, %v", again, err)
	}
	if info, err := os.Stat(filepath.Join(wantDir, "workspace")); err != nil || !info.IsDir() {
		t.Fatalf("Hermes workspace missing: %v", err)
	}
}
