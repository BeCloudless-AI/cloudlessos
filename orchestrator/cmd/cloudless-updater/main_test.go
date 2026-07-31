package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/osupdate"
	"github.com/cloudless/orchestrator/internal/platform"
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

func TestDGXSparkDriverStatusPreservesVendorOwnership(t *testing.T) {
	t.Setenv("CLOUDLESS_NVIDIA_STATUS", filepath.Join(t.TempDir(), "nvidia.json"))
	status := managedDGXNVIDIAStatus()
	if status.State != "managed" || !status.HardwareDetected || status.UpdateAvailable {
		t.Fatalf("unexpected DGX-managed status: %#v", status)
	}
	if status.RecommendedPackage != "" || status.RebootRequired {
		t.Fatalf("DGX status must not recommend a generic driver: %#v", status)
	}
}

func TestProtectedDGXPackageChanges(t *testing.T) {
	output := `Reading package lists...
Inst cloudless-orchestrator [0.1.5] (0.2.0 Cloudless:stable [arm64])
Inst libnvidia-container1 [1.19.0] (1.20.0 NVIDIA [arm64])
Remv linux-modules-nvidia-580-6.17.0-1026-nvidia [6.17.0-1026.26]
Inst cuda-toolkit-13-1 (13.1 NVIDIA [arm64])
Inst dgx-release [25.07] (25.08 NVIDIA [arm64])`
	got := protectedDGXPackageChanges(output)
	want := []string{
		"libnvidia-container1",
		"linux-modules-nvidia-580-6.17.0-1026-nvidia",
		"cuda-toolkit-13-1",
		"dgx-release",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("protectedDGXPackageChanges() = %v, want %v", got, want)
	}
}

func TestProtectedDGXPackageChangesAllowsCloudlessOnlyPlan(t *testing.T) {
	output := `Inst cloudless-orchestrator [0.1.5] (0.2.0 Cloudless:stable [arm64])
Inst cloudless-hardware [0.1.1] (0.2.0 Cloudless:stable [arm64])`
	if got := protectedDGXPackageChanges(output); len(got) != 0 {
		t.Fatalf("unexpected protected packages: %v", got)
	}
}

func TestValidateReleaseManifest(t *testing.T) {
	manifest := validReleaseManifest("0.1.4")
	hash := sha256.Sum256(manifest)
	release := []byte(fmt.Sprintf("Origin: Cloudless\nSHA256:\n %x %d cloudless-release.json\n", hash, len(manifest)))

	got, err := validateReleaseManifest(release, manifest, "0.1.4", "stable")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "What is new" || len(got.Changes) != 1 {
		t.Fatalf("validateReleaseManifest() = %#v", got)
	}
}

func TestValidateReleaseManifestRejectsUnsignedNotes(t *testing.T) {
	manifest := validReleaseManifest("0.1.4")
	if _, err := validateReleaseManifest([]byte("SHA256:\n"), manifest, "0.1.4", "stable"); err == nil {
		t.Fatal("validateReleaseManifest() accepted notes missing from signed metadata")
	}
}

func TestValidateReleaseManifestRejectsWrongVersion(t *testing.T) {
	manifest := validReleaseManifest("0.1.3")
	hash := sha256.Sum256(manifest)
	release := []byte(fmt.Sprintf("SHA256:\n %x %d cloudless-release.json\n", hash, len(manifest)))
	if _, err := validateReleaseManifest(release, manifest, "0.1.4", "stable"); err == nil {
		t.Fatal("validateReleaseManifest() accepted notes for another version")
	}
}

func TestValidateReleaseManifestRejectsWrongChannelOrPlatform(t *testing.T) {
	manifest := validReleaseManifest("0.1.4")
	hash := sha256.Sum256(manifest)
	release := []byte(fmt.Sprintf("SHA256:\n %x %d cloudless-release.json\n", hash, len(manifest)))
	if _, err := validateReleaseManifest(release, manifest, "0.1.4", "beta"); err == nil {
		t.Fatal("validateReleaseManifest accepted another channel")
	}
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	if _, err := validateReleaseManifest(release, manifest, "0.1.4", "stable"); err == nil {
		t.Fatal("validateReleaseManifest accepted an incompatible platform")
	}
}

func TestValidSourceCommitRequiresFullHexIdentity(t *testing.T) {
	if !validSourceCommit("89abcdef0123456789abcdef0123456789abcdef") {
		t.Fatal("full release commit was rejected")
	}
	for _, value := range []string{
		"89abcdef",
		"89abcdef0123456789abcdef0123456789abcdeg",
		strings.Repeat("a", 41),
	} {
		if validSourceCommit(value) {
			t.Fatalf("invalid release commit %q was accepted", value)
		}
	}
}

func validReleaseManifest(version string) []byte {
	return []byte(fmt.Sprintf(`{"schema":"cloudless.release.v2","version":%q,"channel":"stable","sourceCommit":"0123456789abcdef0123456789abcdef01234567","title":"What is new","summary":"A safer update.","changes":["Shows verified release notes."],"compatibility":{"schema":"cloudless.compatibility.v1","targets":[{"platform":"generic","architecture":"%s"}]}}`, version, platform.Architecture()))
}

func TestReleaseManifestIdentityRejectsMalformedOrAmbiguousMetadata(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for name, release := range map[string]string{
		"invalid hash": "SHA256:\n not-a-hash 12 cloudless-release.json\n",
		"zero size":    "SHA256:\n " + hash + " 0 cloudless-release.json\n",
		"oversize":     "SHA256:\n " + hash + " 1048577 cloudless-release.json\n",
		"duplicate":    "SHA256:\n " + hash + " 12 cloudless-release.json\n " + strings.Repeat("b", 64) + " 13 cloudless-release.json\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := releaseManifestIdentity([]byte(release)); err == nil {
				t.Fatalf("releaseManifestIdentity accepted %q", release)
			}
		})
	}
}

func TestCaptureWorkloadBindsReadyEngineAndDurableOperations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/engine":
			_, _ = w.Write([]byte(`{"active":"vllm","ready":true,"unloaded":false}`))
		case "/api/recipes":
			_, _ = w.Write([]byte(`{"operations":[{"id":"run-1","kind":"run","phase":"preparing"},{"id":"check-1","kind":"check","phase":"prepared"},{"id":"old","kind":"run","phase":"failed"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := captureWorkload(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReadyEngine != "vllm" || fmt.Sprint(snapshot.RecipeOperationIDs) != "[run-1]" {
		t.Fatalf("captureWorkload() = %#v", snapshot)
	}
}

func TestWorkloadContinuityRejectsLostReadyEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/engine" {
			_, _ = w.Write([]byte(`{"active":"vllm","ready":false,"unloaded":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"operations":[]}`))
	}))
	defer server.Close()

	err := workloadContinuityError(context.Background(), server.URL, workloadSnapshot{ReadyEngine: "vllm"})
	if err == nil || !strings.Contains(err.Error(), "was not preserved") {
		t.Fatalf("workloadContinuityError() = %v", err)
	}
}

func TestWorkloadContinuityAcceptsRecoveredRecipeOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/engine" {
			_, _ = w.Write([]byte(`{"active":"vllm","ready":true,"unloaded":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"operations":[{"id":"run-1","kind":"run","phase":"recovering"}]}`))
	}))
	defer server.Close()

	snapshot := workloadSnapshot{ReadyEngine: "vllm", RecipeOperationIDs: []string{"run-1"}}
	if err := workloadContinuityError(context.Background(), server.URL, snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestWorkloadContinuityRejectsFailedOrMissingRecipeOperation(t *testing.T) {
	for name, operations := range map[string]string{
		"failed":  `[{"id":"run-1","kind":"run","phase":"failed"}]`,
		"missing": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/engine" {
					_, _ = w.Write([]byte(`{"active":"","ready":false,"unloaded":true}`))
					return
				}
				_, _ = fmt.Fprintf(w, `{"operations":%s}`, operations)
			}))
			defer server.Close()

			err := workloadContinuityError(context.Background(), server.URL, workloadSnapshot{RecipeOperationIDs: []string{"run-1"}})
			if err == nil {
				t.Fatal("workloadContinuityError accepted a lost operation")
			}
		})
	}
}

func TestApplyRollsBackWhenActiveWorkloadIsLost(t *testing.T) {
	statusPath := filepath.Join(t.TempDir(), "status.json")
	t.Setenv("CLOUDLESS_UPDATE_STATUS", statusPath)
	t.Setenv("CLOUDLESS_PLATFORM", "generic")

	originalConfigured := updaterConfigured
	originalCandidates := updaterCandidates
	originalRun := updaterRun
	originalRunEnv := updaterRunEnv
	originalCopyDebs := updaterCopyDebs
	originalCapture := updaterCaptureWorkload
	originalWait := updaterWaitContinuity
	originalRollback := updaterRollback
	originalRollbackRoot := updaterRollbackRoot
	originalContinuityWindow := updaterContinuityWindow
	t.Cleanup(func() {
		updaterConfigured = originalConfigured
		updaterCandidates = originalCandidates
		updaterRun = originalRun
		updaterRunEnv = originalRunEnv
		updaterCopyDebs = originalCopyDebs
		updaterCaptureWorkload = originalCapture
		updaterWaitContinuity = originalWait
		updaterRollback = originalRollback
		updaterRollbackRoot = originalRollbackRoot
		updaterContinuityWindow = originalContinuityWindow
	})

	updaterConfigured = func() bool { return true }
	updaterCandidates = func(_ context.Context, status osupdate.Status) (osupdate.Status, error) {
		status.CurrentVersion = "0.2.3"
		status.AvailableVersion = "0.2.4"
		status.AvailableSourceCommit = "89abcdef0123456789abcdef0123456789abcdef"
		status.Packages = []osupdate.Package{{Name: "cloudless-orchestrator", Installed: "0.2.3", Candidate: "0.2.4"}}
		return status, nil
	}
	var commands []string
	updaterRun = func(_ context.Context, name string, args ...string) (string, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return "", nil
	}
	var packageTransactions int
	updaterRunEnv = func(_ context.Context, _ []string, name string, args ...string) (string, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		packageTransactions++
		return "", nil
	}
	var copies [][2]string
	updaterCopyDebs = func(from, to string) error {
		copies = append(copies, [2]string{from, to})
		return nil
	}
	snapshot := workloadSnapshot{ReadyEngine: "vllm", RecipeOperationIDs: []string{"run-1"}}
	updaterCaptureWorkload = func(context.Context, string) (workloadSnapshot, error) { return snapshot, nil }
	var continuityChecks int
	updaterWaitContinuity = func(_ string, got workloadSnapshot, _ time.Duration) error {
		continuityChecks++
		if got.ReadyEngine != snapshot.ReadyEngine || fmt.Sprint(got.RecipeOperationIDs) != fmt.Sprint(snapshot.RecipeOperationIDs) {
			t.Fatalf("continuity snapshot changed: %#v", got)
		}
		if continuityChecks == 1 {
			return errors.New("ready engine disappeared")
		}
		return nil
	}
	var rollbacks int
	updaterRollback = func(_ context.Context, dir string) error {
		rollbacks++
		if !strings.HasPrefix(dir, updaterRollbackRoot) {
			t.Fatalf("rollback directory %q is outside %q", dir, updaterRollbackRoot)
		}
		return nil
	}
	updaterRollbackRoot = t.TempDir()
	updaterContinuityWindow = time.Millisecond

	err := applyLocked()
	if err == nil || !strings.Contains(err.Error(), "was rolled back") {
		t.Fatalf("applyLocked() = %v", err)
	}
	if packageTransactions != 2 {
		t.Fatalf("package transactions = %d, want download and install", packageTransactions)
	}
	if continuityChecks != 2 || rollbacks != 1 {
		t.Fatalf("continuity checks=%d rollbacks=%d", continuityChecks, rollbacks)
	}
	if len(copies) != 2 {
		t.Fatalf("package snapshots = %#v; rollback path must not save the failed generation", copies)
	}
	status, readErr := osupdate.Read()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if status.State != "failed" || !strings.Contains(status.Message, "rolled back") {
		t.Fatalf("persisted status = %#v", status)
	}
	if status.CurrentSourceCommit != "" {
		t.Fatal("failed generation became the installed release identity")
	}
	if strings.Contains(strings.Join(commands, "\n"), "lightdm") {
		t.Fatal("failed generation restarted the kiosk")
	}
}
