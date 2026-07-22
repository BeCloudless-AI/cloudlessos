package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestHermesCannotBeUninstalled(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/apps/hermes/uninstall", nil)
	req.SetPathValue("id", "hermes")
	recorder := httptest.NewRecorder()
	(&Server{}).appUninstall(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("Hermes uninstall status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

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
	secretPath := filepath.Join(st.Dir(), "secrets", "hermes-api-key")
	if info, err := os.Stat(secretPath); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("private Hermes credential has unsafe permissions: %v, %v", info, err)
	}
	if got := apps.EnvOverrides(wantDir, hermes)[apps.HermesAPIKeyEnv]; got != key {
		t.Fatalf("Hermes runtime environment did not receive the private credential")
	}
}
