package api

import (
	"strings"
	"testing"
)

func TestWebShowsSupportLevelsAcrossCatalogSurfaces(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		"function supportBadge(level)",
		"support-supported",
		"support-preview",
		"support-experimental",
		"supportBadge(a.supportLevel)",
		"supportBadge(e.supportLevel)",
		"trust.supportLevel || 'experimental'",
	} {
		if !strings.Contains(web, want) {
			t.Errorf("support-level UI contract is missing %q", want)
		}
	}
}
