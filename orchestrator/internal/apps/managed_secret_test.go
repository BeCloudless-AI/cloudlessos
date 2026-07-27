package apps

import (
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
