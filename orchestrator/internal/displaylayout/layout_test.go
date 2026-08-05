package displaylayout

import (
	"reflect"
	"testing"
)

const phantomFixture = `Screen 0: minimum 8 x 8, current 7680 x 2160, maximum 32767 x 32767
USB-C-2.3.1 connected primary 3840x2160+3840+0
   3840x2160 60.00*+
   1920x1080 60.00
HDMI-0 connected 3840x2160+0+0
   3840x2160 60.00*+
   1920x1080 60.00
USB-C-5 disconnected
`

func mustParse(t *testing.T, fixture string) Snapshot {
	t.Helper()
	snapshot, err := Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestUnknownMultipleOutputsMirrorInsteadOfExtending(t *testing.T) {
	plan, err := BuildPlan(mustParse(t, phantomFixture), Preference{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Effective.Layout != LayoutMirror || plan.Effective.Width != 3840 || plan.Effective.Height != 2160 {
		t.Fatalf("effective = %+v", plan.Effective)
	}
	want := []string{"--output", "HDMI-0", "--mode", "3840x2160", "--primary", "--pos", "0x0", "--output", "USB-C-2.3.1", "--mode", "3840x2160", "--same-as", "HDMI-0"}
	if !reflect.DeepEqual(plan.Args, want) {
		t.Fatalf("args = %#v, want %#v", plan.Args, want)
	}
}

func TestSingleLayoutDisablesEveryOtherOutput(t *testing.T) {
	plan, err := BuildPlan(mustParse(t, phantomFixture), Preference{Layout: LayoutSingle, Output: "HDMI-0", Width: 3840, Height: 2160})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--output", "HDMI-0", "--mode", "3840x2160", "--primary", "--pos", "0x0", "--output", "USB-C-2.3.1", "--off"}
	if !reflect.DeepEqual(plan.Args, want) {
		t.Fatalf("args = %#v, want %#v", plan.Args, want)
	}
}

func TestMissingSavedConnectorFallsBackToMirror(t *testing.T) {
	plan, err := BuildPlan(mustParse(t, phantomFixture), Preference{Layout: LayoutSingle, Output: "DP-9", Width: 2560, Height: 1440})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || plan.Effective.Layout != LayoutMirror {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestLegacySavedPreferenceBecomesSafeSingleLayout(t *testing.T) {
	plan, err := BuildPlan(mustParse(t, phantomFixture), Preference{Output: "HDMI-0", Width: 1920, Height: 1080})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Effective.Layout != LayoutSingle || plan.Effective.Output != "HDMI-0" {
		t.Fatalf("effective = %+v", plan.Effective)
	}
}

func TestExtendRequiresExplicitLayoutAndArrangesOutputs(t *testing.T) {
	plan, err := BuildPlan(mustParse(t, phantomFixture), Preference{Layout: LayoutExtend, Output: "HDMI-0", Width: 3840, Height: 2160})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Effective.Layout != LayoutExtend {
		t.Fatalf("effective = %+v", plan.Effective)
	}
	wantTail := []string{"--output", "USB-C-2.3.1", "--mode", "3840x2160", "--right-of", "HDMI-0"}
	if !reflect.DeepEqual(plan.Args[len(plan.Args)-len(wantTail):], wantTail) {
		t.Fatalf("args = %#v", plan.Args)
	}
}

func TestNoCommonMirrorModeFallsBackToSingle(t *testing.T) {
	fixture := `DP-0 connected primary 2560x1440+0+0
   2560x1440 60.00*+
HDMI-0 connected 1920x1080+2560+0
   1920x1080 60.00*+
`
	plan, err := BuildPlan(mustParse(t, fixture), Preference{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Effective.Layout != LayoutSingle || !plan.Fallback {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestNoCommonMirrorModeUsesOutputAtDesktopOrigin(t *testing.T) {
	fixture := `USB-C-2 connected primary 2560x1440+1920+0
   2560x1440 60.00*+
HDMI-0 connected 1920x1080+0+0
   1920x1080 60.00*+
`
	plan, err := BuildPlan(mustParse(t, fixture), Preference{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Effective.Layout != LayoutSingle || plan.Effective.Output != "HDMI-0" || !plan.Fallback {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestMatchesRejectsPhantomExtendedDesktopForMirrorPolicy(t *testing.T) {
	snapshot := mustParse(t, phantomFixture)
	if Matches(snapshot, Preference{Layout: LayoutMirror, Output: "USB-C-2.3.1", Width: 3840, Height: 2160}) {
		t.Fatal("side-by-side desktop matched mirror policy")
	}
}
