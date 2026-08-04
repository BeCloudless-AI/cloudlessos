package api

import (
	"strings"
	"testing"
)

func TestEmbeddedGuideIsPermanentSearchableAndAccessible(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`id="dock-guide"`,
		`id="guide" aria-hidden="true"`,
		`role="dialog" aria-modal="true" aria-labelledby="guide-title"`,
		`id="guide-search" type="search"`,
		`aria-label="Guide parts"`,
		`aria-live="polite"`,
		`const GUIDE_PARTS = [`,
		`function guideMatches(part, query)`,
		`GUIDE_PARTS.filter(part => guideMatches(part, query))`,
		`aria-current="${part.id === guidePart ? 'page' : 'false'}"`,
		`setTimeout(() => document.getElementById('guide-main').focus({ preventScroll: true }), 30);`,
		`else if (guideOpen()) closeGuide();`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded guide is missing %q", want)
		}
	}
	if strings.Contains(page, `setTimeout(() => document.getElementById('guide-search').focus`) {
		t.Fatal("opening the guide must not summon the kiosk virtual keyboard before the user selects Search")
	}
}

func TestEmbeddedGuideCoversCoreConceptsAndTroubleshooting(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`<b>ELI5</b>`,
		`Connect an external Hermes Agent (Desktop)`,
		`What are models and recipes?`,
		`What is Tailscale?`,
		`Engines, containers, and model loading`,
		`Connection troubleshooting`,
		`401 Unauthorized`,
		`403 Forbidden`,
		`A container is a packaged runtime and its dependencies`,
		`It is <b>not a copy of the whole DGX Spark</b>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded guide content is missing %q", want)
		}
	}
}

func TestEmbeddedGuideUsesLiveGatewayContractForHermesDesktop(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`guideGateway = await api('/api/gateway')`,
		`g?.servedName || 'cloudless'`,
		`g?.lan?.url`,
		`g?.tailnet?.url`,
		`g?.tunnel?.modelURL`,
		`Custom endpoint / self-hosted OpenAI-compatible provider`,
		`Use the <b>Model URL ending in /v1</b>`,
		`Do not use <span class="guide-inline-code">/agent/v1</span> here`,
		`Model or Model and agent permission`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("Hermes Desktop guide contract is missing %q", want)
		}
	}
}

func TestEmbeddedGuideDeepLinksToRealCloudlessControls(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`if (action === 'models') openModelManager('language');`,
		`else if (action === 'recipes') openRecipeLibrary();`,
		`else if (action === 'api') { setPage = 'api'; openSettings(); }`,
		`else if (action === 'tailscale') { setPage = 'remote-access'; openSettings(); }`,
		`else if (action === 'engine') { setPage = 'engine'; openSettings(); }`,
		`else if (action === 'tour') showOnboarding();`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded guide deep link is missing %q", want)
		}
	}
}
