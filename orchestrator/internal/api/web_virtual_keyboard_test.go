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
