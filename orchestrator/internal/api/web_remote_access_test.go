package api

import (
	"strings"
	"testing"
)

func TestBrowserAndTailscaleAreNativeCloudlessTools(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`id="dock-browser"`,
		`<use href="#ui-browser"/>`,
		`function openSystemBrowser(`,
		`function refreshBrowserDock(`,
		`openSystemBrowser('https://www.google.com','show')`,
		`data-system-app="browser"`,
		`<div class="nm">Browser</div>`,
		`/api/system/browser`,
		`id: 'remote-access'`,
		`function renderRemoteAccessPage(c)`,
		`/api/system/tailscale/connect`,
		`[['tailscale-serve','serve'],['tailscale-ssh','ssh']]`,
		`'/api/system/tailscale/'+path`,
		`target.origin !== location.origin`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("remote access UI is missing %q", want)
		}
	}
}

func TestSettingsActionButtonsKeepIconsAndLabelsAligned(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`.btn.control-action`,
		`class="btn primary control-action" id="tailscale-connect"`,
		`class="btn primary control-action" id="api-contract-save"`,
		`class="btn control-action" id="custom-engine-terminal"`,
		`class="btn control-action" id="custom-engine-toggle"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("aligned settings action is missing %q", want)
		}
	}
}
