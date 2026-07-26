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

	for name, pattern := range map[string]string{
		"native JavaScript dialog":  `(?i)(?:^|[^[:alnum:]_$])(?:window\.)?(?:alert|confirm|prompt)\s*\(`,
		"native HTML dialog":        `(?i)<dialog(?:\s|>)`,
		"new browser window":        `(?i)(?:window\.open\s*\(|target\s*=\s*["']_blank["'])`,
		"unload prompt":             `(?i)(?:onbeforeunload|beforeunload["'])`,
		"browser validation bubble": `(?i)(?:setCustomValidity|reportValidity|<(?:input|select|textarea)[^>]*\srequired(?:\s|=|>))`,
	} {
		if match := regexp.MustCompile(pattern).FindString(page); match != "" {
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
