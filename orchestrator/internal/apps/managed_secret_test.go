package apps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/catalog"
)

func TestSearxNGReceivesStableGeneratedSecret(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "apps", "searxng")
	app := catalog.App{ID: "searxng"}
	first := EnvOverrides(dir, app)["SEARXNG_SECRET"]
	second := EnvOverrides(dir, app)["SEARXNG_SECRET"]
	if first == "" || first != second || !strings.HasPrefix(first, "cloudless-search-") {
		t.Fatalf("generated secret was not stable: %q / %q", first, second)
	}
	if first == "cloudless-local-search" {
		t.Fatal("static catalog secret must never be used")
	}
}

func TestCatalogManagedPasswordMarkersBecomePrivateStableSecrets(t *testing.T) {
	for _, id := range []string{
		"ai-toolkit",
		"unsloth",
		"open-webui",
		"data-designer",
		"openclaw",
		"hermes",
		"perplexica",
	} {
		app, ok := catalog.Get(id)
		if !ok {
			continue // architecture-specific app is unavailable on this test host
		}
		dir := filepath.Join(t.TempDir(), "apps", id)
		first := EnvOverrides(dir, app)
		second := EnvOverrides(dir, app)
		found := false
		for key, catalogValue := range app.Env {
			if !strings.HasPrefix(catalogValue, "cloudless-managed://") {
				continue
			}
			found = true
			if first[key] == "" || first[key] != second[key] || first[key] == catalogValue ||
				!strings.HasPrefix(first[key], "cloudless-"+id+"-") {
				t.Fatalf("%s %s secret was not resolved safely: %q / %q", id, key, first[key], second[key])
			}
		}
		if !found {
			t.Fatalf("%s has no managed credential marker", id)
		}
	}
}

func TestPerplexicaProviderBootstrapReusesPrivateModelClientIdentity(t *testing.T) {
	app, ok := catalog.Get("perplexica")
	if !ok {
		t.Fatal("Perplexica is missing from the catalog")
	}
	root := t.TempDir()
	configDir := filepath.Join(root, "apps", app.ID)
	fromEnvironment := EnvOverrides(configDir, app)["OPENAI_API_KEY"]
	fromBootstrap, err := ManagedSecret(configDir, app.ID, "perplexica-model-client")
	if err != nil {
		t.Fatal(err)
	}
	if fromBootstrap == "" || fromBootstrap != fromEnvironment || fromBootstrap == "cloudless" {
		t.Fatalf("Perplexica identities differ or use a fixed default: %q / %q", fromEnvironment, fromBootstrap)
	}
	info, err := os.Stat(filepath.Join(root, "secrets", "perplexica-model-client"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Perplexica model identity mode = %o, want 600", info.Mode().Perm())
	}
}

func TestConfigVolumesResolveManagedMarkersAndMigrateLegacyHermesKey(t *testing.T) {
	app, ok := catalog.Get("hermes")
	if !ok {
		t.Fatal("Hermes is missing from the catalog")
	}
	configDir := filepath.Join(t.TempDir(), "apps", "hermes")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := "model:\n  provider: custom\n  api_key: \"cloudless\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigVolumes(configDir, app); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if strings.Contains(got, "cloudless-managed://") || strings.Contains(got, `api_key: "cloudless"`) {
		t.Fatalf("legacy or unresolved credential remains in Hermes config: %s", got)
	}
	if !strings.Contains(got, `api_key: "cloudless-hermes-`) {
		t.Fatalf("Hermes config did not receive a per-install model client value: %s", got)
	}
}
