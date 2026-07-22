package osupdate

import (
	"path/filepath"
	"testing"
)

func TestStatusRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	t.Setenv("CLOUDLESS_UPDATE_STATUS", path)

	want := DefaultStatus()
	want.State = "available"
	want.CurrentVersion = "0.1.0"
	want.AvailableVersion = "0.1.1"
	want.Progress = 72
	want.Packages = []Package{{Name: "cloudless-orchestrator", Installed: "0.1.0", Candidate: "0.1.1"}}
	if err := Write(want); err != nil {
		t.Fatal(err)
	}
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.State != want.State || got.AvailableVersion != want.AvailableVersion || got.Progress != want.Progress || len(got.Packages) != 1 {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}
}

func TestMissingStatusReturnsIdle(t *testing.T) {
	t.Setenv("CLOUDLESS_UPDATE_STATUS", filepath.Join(t.TempDir(), "missing.json"))
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "idle" || got.Channel != "stable" {
		t.Fatalf("Read() = %#v", got)
	}
}
