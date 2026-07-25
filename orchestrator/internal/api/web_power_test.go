package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebIncludesNativePowerMenu(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`id="power-pop"`,
		`data-power-action="reboot"`,
		`data-power-action="shutdown"`,
		`function openPowerDialog(action)`,
		`'/api/system/reboot'`,
		`'/api/system/shutdown'`,
		`'X-Cloudless-Action': actionHeader`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
	if strings.Contains(page, `confirm('Shut down CloudlessOS?`) {
		t.Fatal("power actions must use the native CloudlessOS dialog, not a browser confirmation")
	}
}
