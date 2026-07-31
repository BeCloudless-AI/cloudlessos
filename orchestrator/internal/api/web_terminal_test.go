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

func TestTerminalHidePreservesSessionsAndTabs(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`const terminalTabs=new Map()`,
		`function hideTerminalWindow(){document.getElementById('terminal-window').classList.add('hidden');hideVirtualKeyboard();}`,
		`function openTerminalWindow(){`,
		`if(!terminalTabs.size)addTerminalTab()`,
		`selectTerminalTab(terminalActiveTab||[...terminalTabs.keys()][0])`,
		`entry.frame.src='about:blank';entry.frame.remove();entry.tab.remove();terminalTabs.delete(id)`,
		`document.getElementById('terminal-minimize').onclick=hideTerminalWindow`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("terminal persistence contract is missing %q", want)
		}
	}

	hideStart := strings.Index(page, `function hideTerminalWindow()`)
	selectStart := strings.Index(page, `function selectTerminalTab(`)
	if hideStart < 0 || selectStart <= hideStart {
		t.Fatal("could not isolate hideTerminalWindow")
	}
	hideBody := page[hideStart:selectStart]
	for _, destructive := range []string{`remove()`, `about:blank`, `terminalTabs.delete`} {
		if strings.Contains(hideBody, destructive) {
			t.Fatalf("hiding the terminal destroys session state through %q", destructive)
		}
	}
}
