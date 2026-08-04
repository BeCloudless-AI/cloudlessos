package api

import (
	"strings"
	"testing"
)

func TestAPIAccessExplainsAndConfirmsExternalGatewayPolicy(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(content)
	for _, want := range []string{
		"Security activity",
		"Prompts, responses and key secrets are never logged",
		"Hermes administration and its dashboard are never exposed",
		"Allow local-network API access?",
		"Create a public AI gateway?",
		"'X-Cloudless-Action': 'gateway-lan'",
		"'X-Cloudless-Action': 'gateway-public'",
		"'X-Cloudless-Action': 'gateway-key-create'",
		"'X-Cloudless-Action': 'gateway-key-revoke'",
		"'X-Cloudless-Action': 'gateway-contract'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("API access security UI missing %q", want)
		}
	}
}

func TestAPIAccessAdvancedSectionsAreCollapsedBelowNetworkAccess(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(content)
	network := strings.Index(html, `<div class="api-section-title">Network access</div>`)
	client := strings.Index(html, `<details class="api-section api-collapsible" id="api-client-section">`)
	security := strings.Index(html, `<details class="api-section api-collapsible" id="api-security-section">`)
	if network < 0 || client <= network || security <= client {
		t.Fatalf("collapsed API sections are not ordered below Network access: network=%d client=%d security=%d", network, client, security)
	}
	for _, want := range []string{
		`id="ui-shield"`,
		`const advanced = advancedInterfaceEnabled();`,
		"${advanced ? `<section class=\"api-section\" id=\"api-identity-section\">",
		`const contractSave = document.getElementById('api-contract-save'); if (contractSave)`,
		`.api-collapsible > summary`,
		`.api-collapsible-copy .api-section-title`,
		`.api-collapsible[open] .api-collapsible-chevron`,
		`<span class="api-section-title">Connect a client</span>`,
		`<span class="api-section-title">Security activity</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("collapsed API section is missing %q", want)
		}
	}
}
