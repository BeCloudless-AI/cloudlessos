package nvidiaupdate

import (
	"path/filepath"
	"testing"
)

func TestMissingStatusReturnsDefault(t *testing.T) {
	t.Setenv("CLOUDLESS_NVIDIA_STATUS", filepath.Join(t.TempDir(), "missing.json"))
	status, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "idle" || status.Progress != 0 {
		t.Fatalf("unexpected default: %#v", status)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	t.Setenv("CLOUDLESS_NVIDIA_STATUS", filepath.Join(t.TempDir(), "driver.json"))
	want := DefaultStatus()
	want.HardwareDetected = true
	want.CurrentDriver = "570.124.04"
	want.RecommendedPackage = "nvidia-driver-580"
	want.CandidateVersion = "580.65.06-0ubuntu0.24.04.1"
	want.State = "available"
	want.UpdateAvailable = true
	if err := Write(want); err != nil {
		t.Fatal(err)
	}
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.RecommendedPackage != want.RecommendedPackage || !got.UpdateAvailable {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}
