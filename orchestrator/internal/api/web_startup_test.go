package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebPlaysIntroOncePerSystemBoot(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"cloudless.lastBootIntro.v1",
		"function showStartupIntro(next, bootID = '', force = false)",
		"showStartupIntro(afterIntro, (o && o.bootID) || '')",
		"'/api/system/boot-health'",
		"consecutive verified boot",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
	if strings.Contains(page, "cloudless.firstLaunchIntro.v1") {
		t.Fatal("startup animation must not be permanently suppressed after first launch")
	}
}

func TestEmbeddedWebRequiresFirstLaunchSetupConsent(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"Cloudless is recipe-first",
		"Cloudless is designed to start with a ready-to-use recipe",
		"Nothing is downloaded or started from this screen.",
		"Open Model Manager",
		"Open Guide",
		"What is a recipe?",
		"Recipes, explained simply",
		`aria-controls="first-setup-recipe-explanation"`,
		"explanation.classList.toggle('hidden')",
		"Recommended for this DGX Spark",
		"Laguna S 2.1",
		"document.documentElement.dataset.cloudlessPlatform !== 'dgx-spark'",
		"body: JSON.stringify({ choice: 'manual' })",
		"openModelManager('recipes')",
		"openGuide('models-recipes')",
		"/api/onboarding/setup",
		"/api/onboarding/complete",
		"o.setupRequired",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded first-launch setup is missing %q", want)
		}
	}
	firstSetupStart := strings.Index(page, `<div class="first-setup hidden"`)
	if firstSetupStart < 0 {
		t.Fatal("embedded first-launch setup markup could not be isolated")
	}
	firstSetupEnd := strings.Index(page[firstSetupStart:], `<section class="virtual-keyboard"`)
	if firstSetupEnd < 0 {
		t.Fatal("embedded first-launch setup markup could not be isolated")
	}
	firstSetup := page[firstSetupStart : firstSetupStart+firstSetupEnd]
	for _, forbidden := range []string{"Download and Install", "I will do it myself", "first-setup-install"} {
		if strings.Contains(firstSetup, forbidden) {
			t.Fatalf("embedded first-launch setup must not offer automatic installation: found %q", forbidden)
		}
	}
}

func TestEmbeddedAPIAccessRejectsFalseLANSuccess(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"if (!r || r.error) throw new Error(r?.error",
		"toast(enable ? 'API on your network: ' + r.url",
		"await renderApiPage(c);",
		`<option value="both" selected>Model and agent</option>`,
		"const lanUnavailable = keys.length === 0;",
		`data-tooltip="Create an API key first"`,
		`disabled aria-label="Create an API key first"`,
		"title: 'Create an API key first'",
		"confirmLabel: 'Create API key'",
		"lanCard.onclick = () => promptForAPIKey(lanCard)",
		"currentList?.closest('.settings-page-mount')",
		"currentList?.closest('#inf-panel')",
		"await apiRefreshKeys();",
		"keyDialogReturnFocusID = keyDialogReturnFocus?.id || '';",
		`id="gw-tailnet"`,
		`/api/gateway/tailnet`,
		`'X-Cloudless-Action': 'gateway-tailnet'`,
		"title: 'Allow API access through Tailscale?'",
		"tailnet.url",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded API Access LAN flow is missing %q", want)
		}
	}
}
