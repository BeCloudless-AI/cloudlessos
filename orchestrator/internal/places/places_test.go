package places

import (
	"os/user"
	"reflect"
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

func TestDesktopRunArgsTargetGraphicalUserSession(t *testing.T) {
	u := &user.User{Username: "cloudless", Uid: "114", HomeDir: "/home/cloudless"}
	got := desktopRunArgs(u, ":0", "/usr/bin/pcmanfm", "/home/cloudless/Cloudless/Models")
	want := []string{
		"--quiet", "--collect", "--property=Type=exec", "--uid=cloudless",
		"--setenv=HOME=/home/cloudless",
		"--setenv=DISPLAY=:0",
		"--setenv=XAUTHORITY=/home/cloudless/.Xauthority",
		"--setenv=XDG_RUNTIME_DIR=/run/user/114",
		"--setenv=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/114/bus",
		"/usr/bin/pcmanfm", "--no-desktop", "--new-win", "/home/cloudless/Cloudless/Models",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("desktopRunArgs() = %#v, want %#v", got, want)
	}
}
