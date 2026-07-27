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
