package hardware

import "testing"

func TestParseInputDevicesIgnoresButtonsAndFindsPhysicalInput(t *testing.T) {
	data := `I: Bus=0019 Vendor=0000 Product=0001 Version=0000
N: Name="Power Button"
H: Handlers=kbd event0

I: Bus=0003 Vendor=046d Product=c52b Version=0111
N: Name="Logitech USB Keyboard"
H: Handlers=sysrq kbd event4 leds

I: Bus=0003 Vendor=046d Product=c077 Version=0111
N: Name="Logitech USB Optical Mouse"
H: Handlers=mouse0 event5
`
	got := parseInputDevices(data)
	if !got.Keyboard || !got.Mouse || !got.Pointer {
		t.Fatalf("input state = %+v, want keyboard, mouse, and pointer", got)
	}
	if len(got.KeyboardNames) != 1 || got.KeyboardNames[0] != "Logitech USB Keyboard" {
		t.Fatalf("keyboard names = %v, want only the physical keyboard", got.KeyboardNames)
	}
}

func TestParseInputDevicesMouseWithoutKeyboard(t *testing.T) {
	data := `N: Name="Power Button"
H: Handlers=kbd event0

N: Name="USB Mouse"
H: Handlers=mouse1 event3
`
	got := parseInputDevices(data)
	if got.Keyboard || !got.Mouse || !got.Pointer {
		t.Fatalf("input state = %+v, want mouse/pointer without keyboard", got)
	}
}

func TestParseInputDevicesRecognizesTouchpadAsPointer(t *testing.T) {
	got := parseInputDevices("N: Name=\"ELAN Touchpad\"\nH: Handlers=event8\n")
	if !got.Pointer || got.Mouse {
		t.Fatalf("input state = %+v, want pointer without mouse", got)
	}
}

func TestParseInputDevicesIgnoresProgrammableMouseKeyboardInterfaces(t *testing.T) {
	data := `N: Name="ASUSTeK ROG HARPE ACE AIM LAB EDITION"
H: Handlers=mouse0 event3

N: Name="ASUSTeK ROG HARPE ACE AIM LAB EDITION Consumer Control"
H: Handlers=kbd event4

N: Name="ASUSTeK ROG HARPE ACE AIM LAB EDITION System Control"
H: Handlers=kbd event5

N: Name="ASUSTeK ROG HARPE ACE AIM LAB EDITION Keyboard"
H: Handlers=sysrq kbd event7
`
	got := parseInputDevices(data)
	if got.Keyboard || !got.Mouse || !got.Pointer {
		t.Fatalf("input state = %+v, want pointer without a typing keyboard", got)
	}
}
