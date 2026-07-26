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
		`key-dialog-body power-dialog-body`,
		`power-dialog-summary-detail`,
		`flex: 0 0 15px`,
		`.power-dialog-body .key-dialog-actions .btn { white-space: nowrap; }`,
		`<span>${restarting ? 'CloudlessOS will be available again after the system starts.'`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
	if strings.Contains(page, `confirm('Shut down CloudlessOS?`) {
		t.Fatal("power actions must use the native CloudlessOS dialog, not a browser confirmation")
	}
	if strings.Contains(page, `uninstall-summary power-dialog-summary`) {
		t.Fatal("power dialog must not inherit uninstall-summary icon layout")
	}
}
