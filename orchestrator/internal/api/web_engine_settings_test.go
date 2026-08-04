package api

import (
	"strings"
	"testing"
)

func TestStandardEngineSettingsExplainManagedRuntime(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`if (!advancedInterfaceEnabled()) {`,
		`class="inf-page engine-standard-page"`,
		`vLLM is the default engine`,
		`What is vLLM?`,
		`motor that runs an AI model`,
		`Recipes choose what works best`,
		`another engine or a specialized runtime`,
		`Enable Advanced interface in Settings → General`,
		`id="engine-open-advanced"`,
		`setPage = 'general'; renderSettings();`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("standard engine settings missing %q", want)
		}
	}

	renderStart := strings.Index(page, "async function renderEnginePage(c)")
	if renderStart < 0 {
		t.Fatal("engine settings renderer is missing")
	}
	advancedGuard := strings.Index(page[renderStart:], "if (!advancedInterfaceEnabled())")
	engineRequest := strings.Index(page[renderStart:], "api('/api/engine')")
	if advancedGuard < 0 || engineRequest < 0 || advancedGuard > engineRequest {
		t.Fatal("standard engine view must be selected before requesting advanced engine state")
	}
}

func TestAdvancedEngineSettingsKeepRuntimeControls(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{`class="ec-grid"`, `Custom engine builds`, `data-eng="${e.id}"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("advanced engine settings missing %q", want)
		}
	}
}
