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
		"Nothing will be downloaded or started until you choose.",
		"Download and Install",
		"I will do it myself",
		"What are recipes?",
		"Recipes, explained simply",
		"/api/onboarding/setup",
		"o.setupRequired",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded first-launch setup is missing %q", want)
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
