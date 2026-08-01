package osupdate

import (
	"os"
	"path/filepath"
	"syscall"
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

func TestWriteStatusRemainsReadableUnderRestrictedServiceUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	t.Setenv("CLOUDLESS_UPDATE_STATUS", path)
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	if err := Write(DefaultStatus()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("status mode = %o, want 644", info.Mode().Perm())
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

func TestReadReconcilesPersistedChannelWithConfiguration(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")
	channelPath := filepath.Join(dir, "update-channel")
	t.Setenv("CLOUDLESS_UPDATE_STATUS", statusPath)
	t.Setenv("CLOUDLESS_UPDATE_CHANNEL_PATH", channelPath)

	if err := os.WriteFile(channelPath, []byte("stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(Status{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(channelPath, []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != "beta" {
		t.Fatalf("Read().Channel = %q, want beta", got.Channel)
	}
}

func TestSetChannelUpdatesAPTAndMetadataTogether(t *testing.T) {
	dir := t.TempDir()
	channelPath := filepath.Join(dir, "update-channel")
	sourcesPath := filepath.Join(dir, "cloudless.sources")
	t.Setenv("CLOUDLESS_UPDATE_CHANNEL_PATH", channelPath)
	t.Setenv("CLOUDLESS_UPDATE_SOURCES_PATH", sourcesPath)
	if err := os.WriteFile(sourcesPath, []byte("Enabled: yes\nTypes: deb\nURIs: https://updates.becloudless.ai/apt\nSuites: stable\nComponents: main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetChannel("beta"); err != nil {
		t.Fatal(err)
	}
	sources, err := os.ReadFile(sourcesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(sources) != "Enabled: yes\nTypes: deb\nURIs: https://updates.becloudless.ai/apt\nSuites: beta\nComponents: main\n" || Channel() != "beta" {
		t.Fatalf("sources=%q channel=%q", sources, Channel())
	}
	for _, path := range []string{sourcesPath, channelPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("%s mode=%v", path, info.Mode().Perm())
		}
	}
}

func TestSetChannelRejectsUnknownChannelWithoutChangingAPT(t *testing.T) {
	dir := t.TempDir()
	sourcesPath := filepath.Join(dir, "cloudless.sources")
	t.Setenv("CLOUDLESS_UPDATE_CHANNEL_PATH", filepath.Join(dir, "update-channel"))
	t.Setenv("CLOUDLESS_UPDATE_SOURCES_PATH", sourcesPath)
	want := []byte("Suites: stable\n")
	if err := os.WriteFile(sourcesPath, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetChannel("nightly"); err == nil {
		t.Fatal("SetChannel accepted nightly")
	}
	got, err := os.ReadFile(sourcesPath)
	if err != nil || string(got) != string(want) {
		t.Fatalf("sources=%q err=%v", got, err)
	}
}

func TestChannelFallsBackToStableForMalformedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-channel")
	t.Setenv("CLOUDLESS_UPDATE_CHANNEL_PATH", path)
	if err := os.WriteFile(path, []byte("nightly\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Channel(); got != "stable" {
		t.Fatalf("Channel()=%q", got)
	}
}
