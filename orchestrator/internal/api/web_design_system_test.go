package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebDefinesSharedControlContract(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	required := []string{
		"--control-height: 36px",
		"--control-gap: 8px",
		"--radius-control: 10px",
		"--window-settings-width: 1180px",
		"--window-primary-width: 1280px",
		":where(button, a, input, select, textarea):focus-visible",
		"display: inline-flex; align-items: center; justify-content: center; gap: var(--control-gap)",
		"line-height: 1; white-space: nowrap",
		".btn > .ui-icon { width: 15px; height: 15px; flex: 0 0 15px",
	}
	for _, want := range required {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded frontend is missing shared design-system contract %q", want)
		}
	}
}

func TestEmbeddedWebPrimaryWindowsUseSharedGeometry(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	required := []string{
		"width: min(var(--window-settings-width), calc(var(--ui-vw, 100vw) - 118px))",
		"height: min(var(--window-settings-height), calc(var(--ui-vh, 100vh) - 100px))",
		"width: min(var(--window-primary-width), var(--ui-vw-96, 96vw))",
		"height: min(var(--ui-vh-92, 92vh), var(--window-primary-height))",
	}
	for _, want := range required {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded frontend is missing shared window geometry %q", want)
		}
	}
}

func TestEmbeddedWebRecipeActionsKeepDestructiveControlReadable(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		".recipe-actions { width: 100%; display: grid; gap: 8px; }",
		".recipe-secondary-actions { display: grid; grid-template-columns: repeat(auto-fit,minmax(76px,1fr)); gap: 7px; }",
		".recipe-actions .recipe-primary-action { grid-column: 1 / -1;",
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded recipe action layout is missing %q", want)
		}
	}
}

func TestEmbeddedWebAppSurfacesFollowRuntimeLifecycle(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	required := []string{
		`.filter(c => c.state === 'running')`,
		`catalog.filter(a => a.id !== 'hermes' && !a.service && !a.hidden && installed.has(a.id))`,
		`if (isRun || pinned) acts +=`,
		"${installed && launchApp ? `<button class=\"ac-btn pin",
	}
	for _, want := range required {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded frontend does not enforce app lifecycle contract %q", want)
		}
	}
}
