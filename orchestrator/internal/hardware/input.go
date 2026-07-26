package hardware

import (
	"os"
	"regexp"
	"strings"
)

// InputDevices describes the physical input classes currently visible to Linux.
// It deliberately ignores ACPI power/sleep buttons, which advertise the kernel's
// "kbd" handler even though they cannot be used to type.
type InputDevices struct {
	Keyboard      bool     `json:"keyboard"`
	Mouse         bool     `json:"mouse"`
	Pointer       bool     `json:"pointer"`
	KeyboardNames []string `json:"keyboardNames,omitempty"`
	PointerNames  []string `json:"pointerNames,omitempty"`
}

var mouseHandlerPattern = regexp.MustCompile(`(?:^|\s)mouse\d+(?:\s|$)`)

// Inputs returns a best-effort snapshot from Linux's canonical input inventory.
func Inputs() InputDevices {
	path := strings.TrimSpace(os.Getenv("CLOUDLESS_INPUT_DEVICES_PATH"))
	if path == "" {
		path = "/proc/bus/input/devices"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return InputDevices{}
	}
	return parseInputDevices(string(data))
}

func parseInputDevices(data string) InputDevices {
	result := InputDevices{}
	type inputBlock struct{ name, handlers string }
	blocks := make([]inputBlock, 0)
	for _, block := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n\n") {
		name, handlers := inputBlockFields(block)
		if handlers == "" {
			continue
		}
		blocks = append(blocks, inputBlock{name: name, handlers: handlers})
		lowerName := strings.ToLower(name)
		if mouseHandlerPattern.MatchString(handlers) {
			result.Mouse = true
			result.Pointer = true
			result.PointerNames = appendUnique(result.PointerNames, name)
		}
		// Touchpads and pointing sticks occasionally expose only an event handler.
		if strings.Contains(lowerName, "touchpad") || strings.Contains(lowerName, "trackpad") || strings.Contains(lowerName, "pointing stick") {
			result.Pointer = true
			result.PointerNames = appendUnique(result.PointerNames, name)
		}
	}
	for _, block := range blocks {
		lowerName := strings.ToLower(block.name)
		if hasInputHandler(block.handlers, "kbd") && !nonTypingKeyboard(lowerName) && !pointerAuxiliaryKeyboard(block.name, result.PointerNames) {
			result.Keyboard = true
			result.KeyboardNames = appendUnique(result.KeyboardNames, block.name)
		}
	}
	return result
}

func inputBlockFields(block string) (name, handlers string) {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `N: Name="`) {
			name = strings.TrimSuffix(strings.TrimPrefix(line, `N: Name="`), `"`)
		}
		if strings.HasPrefix(line, "H: Handlers=") {
			handlers = strings.TrimSpace(strings.TrimPrefix(line, "H: Handlers="))
		}
	}
	return name, handlers
}

func hasInputHandler(handlers, target string) bool {
	for _, handler := range strings.Fields(handlers) {
		if handler == target {
			return true
		}
	}
	return false
}

func nonTypingKeyboard(name string) bool {
	for _, fragment := range []string{"power button", "sleep button", "lid switch", "video bus", "pc speaker", "consumer control", "system control"} {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return false
}

// Gaming mice and other programmable pointers often expose an auxiliary HID
// keyboard for macros. Its name is commonly the pointer name plus "Keyboard";
// counting it would suppress the on-screen keyboard even though the device has
// no typing keys.
func pointerAuxiliaryKeyboard(name string, pointerNames []string) bool {
	for _, pointerName := range pointerNames {
		if pointerName != "" && strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(pointerName)+" Keyboard") {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		value = "Unknown input device"
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
