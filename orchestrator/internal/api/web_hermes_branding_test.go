package api

import (
	"strings"
	"testing"
)

func TestCloudlessAssistantCreditsHermesAgent(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"Ask Cloudless",
		"Cloudless Agent",
		"Powered by Hermes Agent",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("assistant branding missing %q", want)
		}
	}
	for _, unwanted := range []string{"Powered by Hermes Agent ·", "Hermes Agent · waiting", "Hermes Agent · load"} {
		if strings.Contains(page, unwanted) {
			t.Fatalf("assistant branding contains unnecessary runtime detail %q", unwanted)
		}
	}
}
