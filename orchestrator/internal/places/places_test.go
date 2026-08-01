package places

import (
	"testing"
)

func TestDefsUseConfiguredCloudlessHome(t *testing.T) {
	t.Setenv("CLOUDLESS_HOME", "/home/cloudless/Cloudless")
	t.Setenv("CLOUDLESS_DESKTOP_HOME", "/home/cloudless")

	got := defs()
	wantPaths := []string{
		"/home/cloudless/Cloudless/Models",
		"/home/cloudless/Cloudless/Outputs",
		"/home/cloudless/Cloudless/Workspace",
		"/home/cloudless/Downloads",
	}
	for i, want := range wantPaths {
		if got[i].path != want {
			t.Fatalf("defs()[%d].path = %q, want %q", i, got[i].path, want)
		}
	}
}
