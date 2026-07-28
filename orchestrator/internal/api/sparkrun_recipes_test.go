package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestSparkRunFetcherRejectsNonPublicAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.10", "169.254.1.2", "::1", "fc00::1"} {
		if publicAddress(net.ParseIP(raw)) {
			t.Fatalf("private address %s was accepted", raw)
		}
	}
	if !publicAddress(net.ParseIP("1.1.1.1")) {
		t.Fatal("public address was rejected")
	}
}

const sparkRunAPIRecipe = `model: Qwen/Qwen3-1.7B
model_revision: abc123
runtime: vllm
container: ghcr.io/example/vllm:1
defaults:
  port: 8000
  tensor_parallel: 1
command: vllm serve {model} --port {port}
`

func fakeSparkRunProvider(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sparkrun")
	script := "#!/bin/sh\nif [ \"${1:-}\" = --version ]; then echo 'sparkrun " + localrecipes.SparkRunProviderVersion + "'; exit 0; fi\nif [ \"${1:-}\" = recipe ] && [ \"${2:-}\" = validate ]; then grep -q rejected \"$3\" && { echo rejected >&2; exit 2; }; echo valid; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNormalizeSparkRunGitHubBlobURL(t *testing.T) {
	got, err := normalizeSparkRunURL("https://github.com/acme/recipes/blob/main/qwen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://raw.githubusercontent.com/acme/recipes/main/qwen.yaml" {
		t.Fatalf("normalized URL = %q", got)
	}
}

func TestNormalizeSparkArenaShortcut(t *testing.T) {
	got, err := normalizeSparkRunURL("@spark-arena/12345678-abcd")
	if err != nil || got != "https://spark-arena.com/api/recipes/12345678-abcd/raw" {
		t.Fatalf("normalized URL = %q, %v", got, err)
	}
}

func TestSparkRunDefaultRegistriesAreMapped(t *testing.T) {
	for _, name := range []string{"official", "experimental", "community", "eugr", "atlas", "sparkrun-transitional", "sparkrun-testing"} {
		if _, ok := sparkRunRegistries[name]; !ok {
			t.Fatalf("default registry %q is missing", name)
		}
	}
}

func TestSparkRunRecipePreviewAcceptsInlineYAML(t *testing.T) {
	body := `{"yaml":` + strconv.Quote(sparkRunAPIRecipe) + `}`
	recorder := httptest.NewRecorder()
	(&Server{sparkRunPath: fakeSparkRunProvider(t)}).sparkRunRecipePreview(recorder, httptest.NewRequest(http.MethodPost, "/api/recipes/sparkrun/preview", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"compatible":true`) {
		t.Fatalf("preview = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSparkRunRecipeImportPersistsNativeRecipe(t *testing.T) {
	body := `{"yaml":` + strconv.Quote(sparkRunAPIRecipe) + `}`
	recorder := httptest.NewRecorder()
	server := &Server{recipes: localrecipes.New(t.TempDir()), sparkRunPath: fakeSparkRunProvider(t)}
	server.sparkRunRecipeImport(recorder, httptest.NewRequest(http.MethodPost, "/api/recipes/sparkrun/import", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"origin":"sparkrun"`) {
		t.Fatalf("import = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSparkRunRecipeImportRejectsProviderValidationFailure(t *testing.T) {
	body := `{"yaml":` + strconv.Quote(sparkRunAPIRecipe+"# rejected\n") + `}`
	recorder := httptest.NewRecorder()
	server := &Server{recipes: localrecipes.New(t.TempDir()), sparkRunPath: fakeSparkRunProvider(t)}
	server.sparkRunRecipeImport(recorder, httptest.NewRequest(http.MethodPost, "/api/recipes/sparkrun/import", strings.NewReader(body)))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "rejected") {
		t.Fatalf("import = %d: %s", recorder.Code, recorder.Body.String())
	}
}
