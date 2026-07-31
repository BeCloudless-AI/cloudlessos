package api

import (
	"strings"
	"testing"
)

func TestGuidedTourUsesOneBlurLayerAndActionGatedTargets(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`One fixed frosted layer with a four-part CSS mask`,
		`.tour-guard[data-tour-guard="top"]`,
		`pointer-events: none;`,
		`.tour-target { position: relative; z-index: 101 !important; }`,
		`const canContinue = !step.action`,
		`tourActionTarget.addEventListener('click', tourActionHandler)`,
		`else lockTourTarget(tourTarget)`,
		`el.inert = true`,
		`@media (prefers-reduced-motion: reduce)`,
		`.tour-card, .tour-spot, .tour-guard, .tour-corner { animation: none; transition: none; }`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("guided-tour interaction contract is missing %q", want)
		}
	}
}

func TestGuidedTourExplainsEachOpenedSurface(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, step := range []string{
		`{ surface: 'assistant', target: '#asst'`,
		`{ surface: 'apps', target: '.lp'`,
		`{ surface: 'models', target: '#models .mm'`,
		`{ surface: 'metrics', target: '#inference .mm'`,
		`{ surface: 'places', target: '#places-quick'`,
		`{ surface: 'settings', target: '#settings .set'`,
	} {
		if !strings.Contains(page, step) {
			t.Fatalf("guided tour lacks an explanation step for %q", step)
		}
	}
}
