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
		`class="btn uc-check-button`,
		`uc-grid uc-system-grid`,
		`filter(app => app.hasUpdate || !!activeOperationForApp(app.id))`,
		`visibleApps.map(updateCenterAppCard)`,
		`function updateQualificationNote(system = {})`,
		`The Cloudless interface refreshes automatically when installation finishes`,
		`Physical hardware qualification is complete for this exact release.`,
		`Pre-release build — the complete physical hardware matrix is not yet qualified.`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("Update Center UI is missing %q", required)
		}
	}
}

func TestUpdateCenterOnlyRendersActionableApplications(t *testing.T) {
	html := readUpdateCenterWeb(t)
	if strings.Contains(html, `(updateCenterData.apps || []).map(updateCenterAppCard)`) {
		t.Fatal("Update Center still renders every installed application")
	}
	if !strings.Contains(html, `visibleApps.length ?`) {
		t.Fatal("application section is not hidden when no app update is actionable")
	}
}

func TestUpdateCenterOnlyRendersActionableManagedEngines(t *testing.T) {
	html := readUpdateCenterWeb(t)
	for _, required := range []string{
		`activeOperationForEngine(engine.id)`,
		`visibleEngines.length ?`,
		`visibleEngines.map(updateCenterEngineCard)`,
		`/api/updates/engines/${engine.id}/apply`,
		`X-Cloudless-Action': 'update-engine'`,
		`Cloudless-managed runtimes only; custom local builds stay untouched`,
		`Custom local engines are never changed.`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("managed inference engine update UI is missing %q", required)
		}
	}
}

func TestUpdateCenterShowsEveryUpdateAuthority(t *testing.T) {
	html := readUpdateCenterWeb(t)
	for _, required := range []string{"CloudlessOS", "Inference engine updates", "NVIDIA driver", "DGX OS and drivers", "Application updates", "Update all"} {
		if !strings.Contains(html, required) {
			t.Fatalf("Update Center does not expose %q", required)
		}
	}
}
