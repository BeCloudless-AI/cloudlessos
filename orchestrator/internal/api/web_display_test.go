package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebIncludesResponsiveDisplaySettings(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"cloudless.uiZoom",
		`{ id: '3', label: '300%', detail: 'Maximum' }`,
		"function automaticUIZoom()",
		"function renderDisplayPage(c)",
		"function openDisplayResolutionDialog(output, width, height)",
		"'/api/system/display'",
		"'X-Cloudless-Action': 'display'",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
}
