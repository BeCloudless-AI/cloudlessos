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
