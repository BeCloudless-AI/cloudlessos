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
		"function openDisplayResolutionDialog(layout, output, width, height)",
		"Mirror displays (safe default)",
		"Extend desktop (advanced)",
		"function showDisplayKeepDialog(result, output, width, height)",
		"'/api/system/display'",
		"'/api/system/display/confirm'",
		"'/api/system/display/revert'",
		"'X-Cloudless-Action': 'display'",
		"'display-confirm'",
		"'display-revert'",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
}
