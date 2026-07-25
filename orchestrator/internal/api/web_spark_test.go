package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebIncludesSparkOnlyDGXDashboardSettings(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"hasCapability('dgx-appliance')",
		`html[data-cap-dgx-appliance="1"] .nvidia-brand`,
		`aria-label="NVIDIA DGX Spark"`,
		"id: 'dgx-dashboard'",
		"function renderDGXDashboardPage",
		"http://127.0.0.1:11000/",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
}
