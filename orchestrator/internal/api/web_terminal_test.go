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
		`function openTerminalWindow()`,
		`id="terminal-window"`,
		`id="terminal-tab-add"`,
		`function terminalWindowScale(win)`,
		`function clampTerminalWindow()`,
		`window.addEventListener('pointermove'`,
		`.terminal-window.dragging::after`,
		`.terminal-window.maximized { inset: 0 !important;`,
		`maximized?'#ui-collapse':'#ui-expand'`,
		`document.getElementById('dock-terminal').onclick = toggleTerminalWindow`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("terminal UI is missing %q", want)
		}
	}
}
