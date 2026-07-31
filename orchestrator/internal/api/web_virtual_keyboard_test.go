package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebIncludesConditionalVirtualKeyboard(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`id="virtual-keyboard"`,
		`id="vk-rows"`,
		`No physical keyboard detected`,
		`'/api/system/input'`,
		`function virtualKeyboardEligible(target)`,
		`inputHardware.mouse || inputHardware.pointer || virtualPointerObserved`,
		`let virtualPointerObserved = false, physicalKeyboardObserved = false`,
		`if (!keyboard.contains(event.target)) virtualPointerObserved = true`,
		`physicalKeyboardObserved = true`,
		`document.addEventListener('focusin'`,
		`keyboard.addEventListener('pointerdown'`,
		`target.type || 'text'`,
		`'password'`,
		`replaceVirtualSelection(target`,
		`backspaceVirtualSelection(target)`,
		`target.closest('form')?.requestSubmit()`,
		`setInterval(refreshInputHardware, 5000)`,
		`'/api/system/input/key'`,
		`'X-Cloudless-Action': 'virtual-keyboard'`,
		`function embeddedVirtualKeyboardTarget(target)`,
		`let embeddedVirtualKeyQueue = Promise.resolve()`,
		`document.activeElement === embeddedFrame`,
		`window.addEventListener('blur', syncEmbeddedFrameFocus)`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing virtual keyboard behavior %q", want)
		}
	}
}

func TestVirtualKeyboardUsesCloudlessInputForEmbeddedSurfaces(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`target === document.getElementById('app-surface-frame') && appSurfaceOpen()`,
		`target.classList.contains('terminal-frame') && terminalWindowOpen()`,
		`void emitEmbeddedVirtualKey('text', virtualKeyboardCharacter(key))`,
		`void emitEmbeddedVirtualKey(virtualKeyboardShift ? 'shift-enter' : 'enter')`,
		`'/api/system/input/key'`,
		`showVirtualKeyboard(embeddedFrame)`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("Cloudless embedded-input contract is missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`matchbox-keyboard`,
		`florence`,
	} {
		if strings.Contains(strings.ToLower(page), forbidden) {
			t.Fatalf("frontend must not substitute an external keyboard: found %q", forbidden)
		}
	}
}
