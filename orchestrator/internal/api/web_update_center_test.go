package api

import (
	"os"
	"strings"
	"testing"
)

func readUpdateCenterWeb(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestUpdateCenterIsFirstClassReconnectableSurface(t *testing.T) {
	html := readUpdateCenterWeb(t)
	for _, required := range []string{
		`id="dock-updates"`,
		`id="dock-update-badge"`,
		`id="update-center"`,
		`function openUpdateCenter()`,
		`api('/api/updates')`,
		`'/api/updates/apps/apply'`,
		`X-Cloudless-Action': 'update-apps'`,
		`activeOperationForApp(app.id)`,
		`Applications keep running while images download`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("Update Center UI is missing %q", required)
		}
	}
}

func TestUpdateCenterShowsEveryUpdateAuthority(t *testing.T) {
	html := readUpdateCenterWeb(t)
	for _, required := range []string{"CloudlessOS", "NVIDIA driver", "DGX OS and drivers", "Applications", "Update all"} {
		if !strings.Contains(html, required) {
			t.Fatalf("Update Center does not expose %q", required)
		}
	}
}
