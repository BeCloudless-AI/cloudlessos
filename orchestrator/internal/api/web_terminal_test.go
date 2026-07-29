package api

import (
	"strings"
	"testing"
)

func TestTerminalIsAStaticCloudlessDockTool(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`id="dock-terminal"`,
		`<use href="#ui-terminal"/>`,
		`openAppSurface('Terminal', '/terminal/', '>_')`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("terminal UI is missing %q", want)
		}
	}
}
