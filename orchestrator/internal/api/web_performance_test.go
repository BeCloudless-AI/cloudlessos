package api

import (
	"strings"
	"testing"
)

func TestSparkDashboardDoesNotRepaintBehindFullScreenSurfaces(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function dashboardVisualsCovered()`,
		`if (!dashboardVisualsCovered() && now - lastDashboardVisualRefresh >= visualCadence)`,
		`const visualCadence = spark ? 12000 : 4000;`,
		`if (systemBuilt === signature) return;`,
		`if (appsBuilt === nextAppsSignature) return;`,
		`setInterval(refreshCoreDashboard, 4000);`,
		`setInterval(tickClock, 60000);`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("4K kiosk repaint guard is missing %q", want)
		}
	}
	if strings.Contains(page, `setInterval(() => { refreshStatus(); renderGPUs(); renderSystem(); renderEngine(); }, 4000);`) {
		t.Fatal("legacy unconditional 4K dashboard repaint loop is still present")
	}
}
