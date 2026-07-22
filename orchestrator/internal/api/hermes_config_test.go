package api

import (
	"os"
	"path/filepath"
	"testing"

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
}
