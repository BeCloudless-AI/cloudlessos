package osupdate

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStatusRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	t.Setenv("CLOUDLESS_UPDATE_STATUS", path)

	want := DefaultStatus()
	want.State = "available"
	want.CurrentVersion = "0.1.0"
	want.CurrentSourceCommit = "0123456789abcdef0123456789abcdef01234567"
	want.AvailableVersion = "0.1.1"
	want.AvailableSourceCommit = "89abcdef0123456789abcdef0123456789abcdef"
	want.ReleaseTitle = "A better update"
	want.Changelog = []string{"Shows release notes before installation."}
	want.Progress = 72
	want.Packages = []Package{{Name: "cloudless-orchestrator", Installed: "0.1.0", Candidate: "0.1.1"}}
	if err := Write(want); err != nil {
		t.Fatal(err)
	}
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.State != want.State || got.CurrentSourceCommit != want.CurrentSourceCommit || got.AvailableVersion != want.AvailableVersion || got.AvailableSourceCommit != want.AvailableSourceCommit || got.ReleaseTitle != want.ReleaseTitle || len(got.Changelog) != 1 || got.Progress != want.Progress || len(got.Packages) != 1 {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}
}

func TestReconcileRebootClearsRequirementAfterNewBoot(t *testing.T) {
	status := Status{
		State:            "reboot_required",
		AvailableVersion: "0.1.1",
		RebootRequired:   true,
		RebootBootID:     "old-boot",
	}
	reconcileReboot(&status, "new-boot", time.Time{})
	if status.State != "updated" || status.RebootRequired || status.AvailableVersion != "" {
		t.Fatalf("reconcileReboot() = %#v", status)
	}
}

func TestReconcileRebootMigratesLegacyStatus(t *testing.T) {
	updatedAt := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	status := Status{State: "reboot_required", UpdatedAt: updatedAt.Format(time.RFC3339), RebootRequired: true}
	reconcileReboot(&status, "current-boot", updatedAt.Add(time.Minute))
	if status.State != "updated" || status.RebootRequired {
		t.Fatalf("reconcileReboot() = %#v", status)
	}
}

func TestReconcileRebootKeepsRequirementOnSameBoot(t *testing.T) {
	status := Status{State: "reboot_required", RebootRequired: true, RebootBootID: "same-boot"}
	reconcileReboot(&status, "same-boot", time.Now())
	if status.State != "reboot_required" || !status.RebootRequired {
		t.Fatalf("reconcileReboot() = %#v", status)
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
