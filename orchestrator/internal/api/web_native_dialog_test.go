package api

import (
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedWebKeepsMessagesInsideCloudlessGUI(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	// OAuth is the sole intentional new browser context: it keeps the
	// Cloudless dashboard in place while the identity provider owns its own
	// navigation and returns through the same-origin callback. All application
	// messages and confirmations must still remain inside the Cloudless GUI.
	pageWithoutOAuthPopup := strings.Replace(page, `window.open('', 'cloudless-account-provider', 'popup,width=560,height=760')`, "", 1)
	if pageWithoutOAuthPopup == page {
		t.Fatal("embedded UI is missing the constrained OAuth provider window")
	}

	for name, pattern := range map[string]string{
		"native JavaScript dialog":  `(?i)(?:^|[^[:alnum:]_$])(?:window\.)?(?:alert|confirm|prompt)\s*\(`,
		"native HTML dialog":        `(?i)<dialog(?:\s|>)`,
		"new browser window":        `(?i)(?:window\.open\s*\(|target\s*=\s*["']_blank["'])`,
		"unload prompt":             `(?i)(?:onbeforeunload|beforeunload["'])`,
		"browser validation bubble": `(?i)(?:setCustomValidity|reportValidity|<(?:input|select|textarea)[^>]*\srequired(?:\s|=|>))`,
	} {
		if match := regexp.MustCompile(pattern).FindString(pageWithoutOAuthPopup); match != "" {
			t.Fatalf("embedded UI contains %s %q; messages must use Cloudless GUI", name, match)
		}
	}

	for _, want := range []string{
		"function openCloudlessDecision(options = {})",
		"Revoke this API key?",
		"Install update",
		"Reset app",
		"Create public link",
		"Erase all electricity data?",
		"openPowerDialog('reboot')",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing Cloudless-native message flow %q", want)
		}
	}
}
