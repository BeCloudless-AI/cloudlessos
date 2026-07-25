package main

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/osupdate"
)

func TestParseRecommendedNVIDIAPackage(t *testing.T) {
	output := `vendor   : NVIDIA Corporation
model    : AD102 [GeForce RTX 5090]
driver   : nvidia-driver-570 - distro non-free
driver   : nvidia-driver-580-open - distro non-free recommended
driver   : xserver-xorg-video-nouveau - distro free builtin`
	got, err := parseRecommendedNVIDIAPackage(output)
	if err != nil {
		t.Fatal(err)
	}
	if got != "nvidia-driver-580-open" {
		t.Fatalf("got %q", got)
	}
}

func TestParseRecommendedNVIDIAPackageRequiresRecommendation(t *testing.T) {
	if _, err := parseRecommendedNVIDIAPackage("driver : nvidia-driver-580 - distro non-free"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestBinaryPackageNameIsArchitectureNeutral(t *testing.T) {
	for _, input := range []string{"nvidia-driver-580:amd64", "nvidia-driver-580:arm64", "nvidia-driver-580"} {
		if got := binaryPackageName(input); got != "nvidia-driver-580" {
			t.Fatalf("binaryPackageName(%q) = %q", input, got)
		}
	}
}

func TestWriteProgressClampsPercentage(t *testing.T) {
	t.Setenv("CLOUDLESS_UPDATE_STATUS", filepath.Join(t.TempDir(), "status.json"))
	status := osupdate.DefaultStatus()

	if err := writeProgress(&status, -4, "Starting"); err != nil {
		t.Fatal(err)
	}
	if status.Progress != 0 || status.Message != "Starting" {
		t.Fatalf("writeProgress() = %#v", status)
	}

	if err := writeProgress(&status, 140, "Finishing"); err != nil {
		t.Fatal(err)
	}
	if status.Progress != 100 || status.Message != "Finishing" {
		t.Fatalf("writeProgress() = %#v", status)
	}
}

func TestValidateReleaseManifest(t *testing.T) {
	manifest := []byte(`{"version":"0.1.4","title":"What is new","summary":"A safer update.","changes":["Shows verified release notes."]}`)
	hash := sha256.Sum256(manifest)
	release := []byte(fmt.Sprintf("Origin: Cloudless\nSHA256:\n %x %d cloudless-release.json\n", hash, len(manifest)))

	got, err := validateReleaseManifest(release, manifest, "0.1.4")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "What is new" || len(got.Changes) != 1 {
		t.Fatalf("validateReleaseManifest() = %#v", got)
	}
}

func TestValidateReleaseManifestRejectsUnsignedNotes(t *testing.T) {
	manifest := []byte(`{"version":"0.1.4","title":"What is new","changes":["A change."]}`)
	if _, err := validateReleaseManifest([]byte("SHA256:\n"), manifest, "0.1.4"); err == nil {
		t.Fatal("validateReleaseManifest() accepted notes missing from signed metadata")
	}
}

func TestValidateReleaseManifestRejectsWrongVersion(t *testing.T) {
	manifest := []byte(`{"version":"0.1.3","title":"Old notes","changes":["A change."]}`)
	hash := sha256.Sum256(manifest)
	release := []byte(fmt.Sprintf("SHA256:\n %x %d cloudless-release.json\n", hash, len(manifest)))
	if _, err := validateReleaseManifest(release, manifest, "0.1.4"); err == nil {
		t.Fatal("validateReleaseManifest() accepted notes for another version")
	}
}
