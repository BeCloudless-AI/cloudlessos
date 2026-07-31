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
		if a.SupportLevel != "experimental" && a.SupportLevel != "preview" && a.SupportLevel != "supported" {
			t.Fatalf("%s has invalid support level %q", a.ID, a.SupportLevel)
		}
	}
}

func TestManifestSupportLevelsDefaultFromTrustAndRejectUnknownValues(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"managed","name":"Managed","image":"x/managed:1","hidden":true,"verified":true},` +
		`{"id":"community","name":"Community","image":"x/community:1","hidden":true}]}`
	doc, err := ParseManifest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Apps[0].SupportLevel != "supported" {
		t.Fatalf("verified app support = %q, want supported", doc.Apps[0].SupportLevel)
	}
	if doc.Apps[1].SupportLevel != "preview" {
		t.Fatalf("unverified app support = %q, want preview", doc.Apps[1].SupportLevel)
	}

	invalid := strings.Replace(raw, `"hidden":true}]`, `"hidden":true,"supportLevel":"unlimited"}]}`, 1)
	if _, err := ParseManifest([]byte(invalid)); err == nil || !strings.Contains(err.Error(), "unsupported support level") {
		t.Fatalf("invalid support level accepted: %v", err)
	}
}

func TestManifestRejectsUnknownFieldsAndDependencyCycles(t *testing.T) {
	badField := strings.Replace(string(embeddedManifest), `"schema":`, `"surprise":true,"schema":`, 1)
	if _, err := ParseManifest([]byte(badField)); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
	cycle := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"a","name":"A","image":"x/a:1","hidden":true,"dependencies":["b"]},` +
		`{"id":"b","name":"B","image":"x/b:1","hidden":true,"dependencies":["a"]}]}`
	if _, err := ParseManifest([]byte(cycle)); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestExposureContractFailsClosed(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"unsafe","name":"Unsafe","image":"x/y:1","hidden":true,"ports":{"9999":80},` +
		`"exposure":{"risk":"control","public":"opt-in"}}]}`
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("public unauthenticated service accepted: %v", err)
	}
}

func TestManifestRejectsEmbeddedAdministratorPassword(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"unsafe","name":"Unsafe","image":"x/y:1","hidden":true,"admin":{"note":"login","pass":"password"}}]}`
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "fixed administrator password") {
		t.Fatalf("embedded administrator password accepted: %v", err)
	}
}

func TestManifestRejectsFixedCredentialEnvironmentValues(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"unsafe","name":"Unsafe","image":"x/y:1","hidden":true,"env":{"OPENAI_API_KEY":"shared-password"}}]}`
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "cloudless-managed credential") {
		t.Fatalf("fixed environment credential accepted: %v", err)
	}

	managed := strings.Replace(raw, `"shared-password"`, `"cloudless-managed://unsafe-model-client"`, 1)
	if _, err := ParseManifest([]byte(managed)); err != nil {
		t.Fatalf("managed environment credential rejected: %v", err)
	}
}

func TestPasswordlessNotebookMustRemainLocalAndNonShareable(t *testing.T) {
	base := `{"schema":"cloudless.apps.v2","version":2,"apps":[` +
		`{"id":"notebook","name":"Notebook","image":"x/y:1","hidden":true,"localOnly":true,` +
		`"command":["--ServerApp.token=","--ServerApp.password="],` +
		`"exposure":{"risk":"code-execution-ui","lan":"none","public":"none"}}]}`
	if _, err := ParseManifest([]byte(base)); err != nil {
		t.Fatalf("local-only passwordless notebook rejected: %v", err)
	}

	unsafe := strings.Replace(base, `"localOnly":true`, `"localOnly":false`, 1)
	if _, err := ParseManifest([]byte(unsafe)); err == nil || !strings.Contains(err.Error(), "passwordless notebook") {
		t.Fatalf("shareable passwordless notebook accepted: %v", err)
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
	want := map[string]bool{"voice": false, "rag": false}
	for _, pack := range doc.Packs {
		if len(pack.Apps) < 2 {
			t.Errorf("%s is not a genuine multi-app pack: %#v", pack.ID, pack.Apps)
		}
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

func TestSingleApplicationsAreVisibleCatalogEntriesNotPacks(t *testing.T) {
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
	for _, id := range []string{"search", "research", "workflows"} {
		if _, ok := packs[id]; ok {
			t.Errorf("single application %s is still represented as a pack", id)
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
		if (id == "searxng" || id == "perplexica" || id == "n8n") && app.Hidden {
			t.Errorf("%s must be a visible launcher application", id)
		}
	}
}

func TestApplicationInstallOrderIncludesDependenciesFirst(t *testing.T) {
	order, err := Dependencies("perplexica")
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0].ID != "searxng" {
		t.Fatalf("Perplexica dependency order = %#v", order)
	}
}

func TestManifestRejectsPackWithUnknownComponent(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[{"id":"known","name":"Known","image":"x/y:1","hidden":true}],"packs":[{"id":"bad","name":"Bad","description":"Bad pack","apps":["known","missing"]}]}`
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown app") {
		t.Fatalf("unknown pack component was accepted: %v", err)
	}
}

func TestManifestRejectsMislabeledSingleApplicationPack(t *testing.T) {
	raw := `{"schema":"cloudless.apps.v2","version":2,"apps":[{"id":"known","name":"Known","image":"x/y:1","hidden":true}],"packs":[{"id":"bad","name":"Bad","description":"Bad pack","apps":["known"]}]}`
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "at least two") {
		t.Fatalf("single-app pack accepted: %v", err)
	}
}

func TestOptionalApplicationsAreAbsentFromCleanBootProvisioning(t *testing.T) {
	bundled := map[string]bool{}
	for _, app := range Bundled() {
		if !app.Service {
			bundled[app.ID] = true
		}
	}
	for _, app := range All() {
		if app.Service || app.Preinstall {
			continue
		}
		if bundled[app.ID] {
			t.Errorf("optional application %s is pulled or launched during clean boot", app.ID)
		}
	}
	for _, core := range []string{"open-webui", "hermes"} {
		if !bundled[core] {
			t.Errorf("core application %s is missing from boot provisioning", core)
		}
	}
}

func TestVisibleApplicationLifecycleContractsAreComplete(t *testing.T) {
	for _, app := range manifestDocument.Apps {
		if !app.Launchable() {
			continue
		}
		if app.PrimaryHostPort() == 0 || !strings.HasPrefix(app.OpenPath, "/") {
			t.Errorf("%s has no deterministic local launch URL", app.ID)
		}
		if app.Health.Kind != "http" && app.Health.Kind != "openai" {
			t.Errorf("%s has no service readiness probe", app.ID)
		}
		healthPort := app.Health.Port
		if healthPort == 0 {
			healthPort = app.PrimaryHostPort()
		}
		if healthPort == 0 || app.Health.TimeoutSeconds <= 0 {
			t.Errorf("%s has an incomplete health contract: %#v", app.ID, app.Health)
		}
		if app.Resources.DiskGB <= 0 || app.Resources.MemoryGB <= 0 {
			t.Errorf("%s has no installation storage/memory estimate: %#v", app.ID, app.Resources)
		}
		if len(app.Volumes) == 0 && app.DataPath == "" && len(app.Config) == 0 {
			t.Errorf("%s has no persistent storage contract", app.ID)
		}
		if app.NeedsEngine && (app.LLM == nil || !app.LLM.Consumes || app.LLM.Route == "") {
			t.Errorf("%s needs the active model but has no model integration contract", app.ID)
		}
		for _, dependency := range app.Dependencies {
			if dependency == "" {
				t.Errorf("%s has an empty dependency", app.ID)
			}
		}
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
	bad := `{"schema":"cloudless.apps.v2","version":2,"apps":[{"id":"demo","name":"Demo","image":"x/y:1","hidden":true,"ports":{"8080":80},"embeddedPath":"/wrong/"}]}`
	if _, err := ParseManifest([]byte(bad)); err == nil || !strings.Contains(err.Error(), "embeddedPath") {
		t.Fatalf("unsafe embedded path accepted: %v", err)
	}
}

func TestAIWorkbenchCatalogContracts(t *testing.T) {
	getManifestApp := func(id string) (App, bool) {
		for _, app := range manifestDocument.Apps {
			if app.ID == id {
				return app, true
			}
		}
		return App{}, false
	}
	for _, id := range []string{"nemo-rl", "data-designer", "molt", "axolotl"} {
		app, ok := getManifestApp(id)
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
	dataDesigner, _ := getManifestApp("data-designer")
	if dataDesigner.Env["NEMO_TELEMETRY_ENABLED"] != "false" || dataDesigner.GPUs != "" {
		t.Fatalf("Data Designer defaults are not private and CPU-safe: %#v", dataDesigner)
	}
	molt, _ := getManifestApp("molt")
	if len(molt.Architectures) != 1 || molt.Architectures[0] != "amd64" {
		t.Fatalf("MoLT published runtime must be x86-64 only: %#v", molt.Architectures)
	}
}
