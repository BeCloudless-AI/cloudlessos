package api

import (
	"strings"
	"testing"
)

func TestGuidedTourKeepsZoomSafeExplanationInfrastructure(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`One fixed frosted layer with a four-part CSS mask`,
		`.tour-guard[data-tour-guard="top"]`,
		`.tour-target { z-index: 101 !important; }`,
		`lockTourTarget(tourTarget);`,
		`el.inert = true`,
		`function tourCoordinateSpace()`,
		`function tourLogicalRect(element, scale = tourCoordinateSpace().scale)`,
		`width: innerWidth / scale, height: innerHeight / scale`,
		`rect.left / scale`,
		`r = tourLogicalRect(tourTarget, viewport.scale)`,
		`.tour-dots {`,
		`.tour-dots > i.done`,
		`.tour-dots > i.current`,
		`TOUR_STEPS.map((_, dotIndex)`,
		`dotIndex < tourIndex ? 'done' : dotIndex === tourIndex ? 'current'`,
		`@media (prefers-reduced-motion: reduce)`,
		`.tour-card, .tour-spot, .tour-guard, .tour-corner { animation: none; transition: none; }`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("guided-tour interaction contract is missing %q", want)
		}
	}
	for _, forbidden := range []string{`.tour-progress`, `--tour-progress`, `.tour-target { position:`, `const canContinue`, `tourActionTarget`, `step.action`, `ensureTourSurface`} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("explanation-only tour retains obsolete interaction %q", forbidden)
		}
	}
}

func TestGuidedTourOnlyExplainsLeftAndBottomMenusWithContinue(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	start := strings.Index(page, "const TOUR_STEPS = [")
	if start < 0 {
		t.Fatal("active guided-tour steps could not be isolated")
	}
	end := strings.Index(page[start:], "let tourIndex = 0")
	if end < 0 {
		t.Fatal("active guided-tour steps could not be isolated")
	}
	steps := page[start : start+end]
	for _, want := range []string{
		`title: 'Two menus keep everything within reach'`,
		`target: '#dock'`,
		`title: 'Your main tools stay on the left'`,
		`target: '#home-apps'`,
		`title: 'Files and app shortcuts stay along the bottom'`,
		`path: 'Tour complete'`,
	} {
		if !strings.Contains(steps, want) {
			t.Fatalf("navigation-only tour is missing %q", want)
		}
	}
	if got := strings.Count(steps, "target:"); got != 2 {
		t.Fatalf("navigation-only tour has %d highlighted targets, want left and bottom menus only", got)
	}
	if got := strings.Count(steps, "title:"); got != 4 {
		t.Fatalf("navigation-only tour has %d steps, want welcome, left menu, bottom menu, and completion", got)
	}
	for _, forbidden := range []string{"action:", "surface:", "await:", "#dock-asst", "#dock-models", "#dock-inference", ".cards .card", "#places-quick", "#settings .set"} {
		if strings.Contains(steps, forbidden) {
			t.Fatalf("navigation-only tour contains obsolete step %q", forbidden)
		}
	}
	if !strings.Contains(page, `<button class="btn primary" id="tour-continue" type="button">${continueLabel}</button>`) {
		t.Fatal("every guided-tour step must render a Continue button")
	}
}
