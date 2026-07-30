package catalog

import (
	"strings"
	"testing"
)

func TestEmbeddedManifestV2IsValidAndAuthoritative(t *testing.T) {
	doc, err := ParseManifest(embeddedManifest)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Schema != ManifestSchema || len(doc.Apps) < 9 {
		t.Fatalf("unexpected manifest: schema=%q apps=%d", doc.Schema, len(doc.Apps))
	}
	if len(apps) != len(doc.Apps) {
		t.Fatalf("runtime catalog=%d manifest=%d", len(apps), len(doc.Apps))
	}
	for _, a := range apps {
		if a.Health.Kind == "" || a.Exposure.Risk == "" {
			t.Fatalf("%s lacks operational contracts", a.ID)
		}
	}
}

func TestManifestRejectsUnknownFieldsAndDependencyCycles(t *testing.T) {
	badField := strings.Replace(string(embeddedManifest), `"schema":`, `"surprise":true,"schema":`, 1)
	if _, err := ParseManifest([]byte(badField)); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
	cycle := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"a","name":"A","image":"x/a:1","dependencies":["b"]},` +
		`{"id":"b","name":"B","image":"x/b:1","dependencies":["a"]}]}`
	if _, err := ParseManifest([]byte(cycle)); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestExposureContractFailsClosed(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"unsafe","name":"Unsafe","image":"x/y:1","ports":{"9999":80},` +
		`"exposure":{"risk":"control","public":"opt-in"}}]}`
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("public unauthenticated service accepted: %v", err)
	}
}

func TestDependenciesAreTopologicallyOrdered(t *testing.T) {
	original := apps
	t.Cleanup(func() { apps = original })
	apps = []App{
		{ID: "db", Name: "DB", Image: "x/db:1"},
		{ID: "search", Name: "Search", Image: "x/search:1", Dependencies: []string{"db"}},
		{ID: "research", Name: "Research", Image: "x/research:1", Dependencies: []string{"search"}},
	}
	deps, err := Dependencies("research")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 || deps[0].ID != "db" || deps[1].ID != "search" {
		t.Fatalf("dependency order = %#v", deps)
	}
}

func TestEmbeddedManifestDefinesOptionalCapabilityPacks(t *testing.T) {
	doc, err := ParseManifest(embeddedManifest)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"voice": false, "rag": false, "search": false, "research": false, "workflows": false}
	for _, pack := range doc.Packs {
		if _, ok := want[pack.ID]; ok {
			if pack.Category == "" || pack.Tagline == "" || pack.Long == "" || len(pack.Examples) < 3 {
				t.Errorf("optional %s pack lacks launcher metadata", pack.ID)
			}
			want[pack.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing optional %s pack", id)
		}
	}
}

func TestSingleApplicationCapabilitiesUseProductNames(t *testing.T) {
	doc, err := ParseManifest(embeddedManifest)
	if err != nil {
		t.Fatal(err)
	}
	packs := make(map[string]Pack, len(doc.Packs))
	for _, pack := range doc.Packs {
		packs[pack.ID] = pack
	}
	apps := make(map[string]App, len(doc.Apps))
	for _, app := range doc.Apps {
		apps[app.ID] = app
	}
	for id, want := range map[string]string{"search": "SearXNG", "research": "Perplexica", "workflows": "n8n"} {
		pack, ok := packs[id]
		if !ok {
			t.Fatalf("missing capability %s", id)
		}
		if pack.Name != want {
			t.Errorf("%s is shown as %q, want product name %q", id, pack.Name, want)
		}
	}
	for id, want := range map[string]string{"searxng": "SearXNG", "perplexica": "Perplexica", "n8n": "n8n", "qdrant": "Qdrant", "embeddings": "Hugging Face TEI"} {
		app, ok := apps[id]
		if !ok {
			t.Fatalf("missing app %s", id)
		}
		if app.Name != want {
			t.Errorf("%s is shown as %q, want %q", id, app.Name, want)
		}
	}
}

func TestPackInstallOrderIncludesDependenciesFirst(t *testing.T) {
	order, err := PackInstallOrder("research")
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0].ID != "searxng" || order[1].ID != "perplexica" {
		t.Fatalf("research install order = %#v", order)
	}
}

func TestManifestRejectsPackWithUnknownComponent(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[{"id":"known","name":"Known","image":"x/y:1"}],"packs":[{"id":"bad","name":"Bad","description":"Bad pack","apps":["missing"]}]}`
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown app") {
		t.Fatalf("unknown pack component was accepted: %v", err)
	}
}

func TestWorkflowsUsesValidatedSameOriginPath(t *testing.T) {
	app, ok := Get("n8n")
	if !ok {
		t.Fatal("n8n is missing")
	}
	if app.EmbeddedPath != "/apps/n8n/" || app.Env["N8N_PATH"] != app.EmbeddedPath {
		t.Fatalf("n8n path is not aligned: embedded=%q env=%q", app.EmbeddedPath, app.Env["N8N_PATH"])
	}
	bad := `{"schema":"cloudless.apps.v2","version":2,"apps":[{"id":"demo","name":"Demo","image":"x/y:1","ports":{"8080":80},"embeddedPath":"/wrong/"}]}`
	if _, err := ParseManifest([]byte(bad)); err == nil || !strings.Contains(err.Error(), "embeddedPath") {
		t.Fatalf("unsafe embedded path accepted: %v", err)
	}
}

func TestAIWorkbenchCatalogContracts(t *testing.T) {
	for _, id := range []string{"nemo-rl", "data-designer", "molt", "axolotl"} {
		app, ok := Get(id)
		if !ok {
			t.Fatalf("missing AI workbench %s", id)
		}
		if app.Build != id || app.OpenPath == "" || !app.LocalOnly || app.Exposure.LAN != "none" || app.Exposure.Public != "none" {
			t.Fatalf("%s has an unsafe or incomplete workbench contract: %#v", id, app)
		}
		workspace := "cloudless-" + id + "-workspace"
		mount := app.Volumes[workspace]
		if mount == "" || mount == "/workspace" || mount == "/molt" {
			t.Fatalf("%s workspace hides image-provided source: %q", id, mount)
		}
	}
	dataDesigner, _ := Get("data-designer")
	if dataDesigner.Env["NEMO_TELEMETRY_ENABLED"] != "false" || dataDesigner.GPUs != "" {
		t.Fatalf("Data Designer defaults are not private and CPU-safe: %#v", dataDesigner)
	}
	molt, _ := Get("molt")
	if len(molt.Architectures) != 1 || molt.Architectures[0] != "amd64" {
		t.Fatalf("MoLT published runtime must be x86-64 only: %#v", molt.Architectures)
	}
}
