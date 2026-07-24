package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/nvidiaupdate"
	"github.com/cloudless/orchestrator/internal/osupdate"
)

var managedPackages = []string{
	"cloudless-orchestrator",
	"cloudless-shell",
	"cloudless-branding",
	"cloudless-hardware",
	"cloudless-firstboot",
	"cloudless-updater",
}

const (
	defaultAPTBaseURL = "https://updates.becloudless.ai/apt"
	archiveKeyring    = "/usr/share/keyrings/cloudless-archive-keyring.pgp"
)

type releaseManifest struct {
	Version string   `json:"version"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Changes []string `json:"changes"`
}

func main() {
	if len(os.Args) != 2 {
		fatalf("usage: cloudless-updater check|apply|status|nvidia-check|nvidia-apply|nvidia-status")
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = check()
	case "apply":
		err = apply()
	case "status":
		var status osupdate.Status
		status, err = osupdate.Read()
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(status)
		}
	case "nvidia-check":
		err = checkNVIDIA()
	case "nvidia-apply":
		err = applyNVIDIA()
	case "nvidia-status":
		var status nvidiaupdate.Status
		status, err = nvidiaupdate.Read()
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(status)
		}
	default:
		fatalf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func checkNVIDIA() error {
	return withLock(func() error {
		status := currentNVIDIAStatus("checking", "Checking Ubuntu's signed NVIDIA drivers…")
		_ = nvidiaupdate.Write(status)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if !nvidiaHardwareDetected(ctx) {
			status.State = "unavailable"
			status.Message = "No NVIDIA graphics hardware was detected."
			status.Progress = 100
			status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
			return nvidiaupdate.Write(status)
		}
		status.HardwareDetected = true
		status.Progress = 15
		status.Message = "Refreshing Ubuntu's signed driver repository…"
		_ = nvidiaupdate.Write(status)
		if _, err := run(ctx, "apt-get", "update"); err != nil {
			return failNVIDIAStatus(status, "Could not refresh Ubuntu's driver repository", err)
		}
		status.Progress = 65
		status.Message = "Matching the safest driver to this GPU…"
		_ = nvidiaupdate.Write(status)
		var err error
		status, err = inspectNVIDIA(ctx, status)
		if err != nil {
			return failNVIDIAStatus(status, "Could not inspect NVIDIA driver updates", err)
		}
		status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		status.Progress = 100
		status.State = "idle"
		status.Message = "The recommended NVIDIA driver is installed."
		if status.UpdateAvailable {
			status.State = "available"
			status.Message = "A compatible NVIDIA driver update is available."
		}
		return nvidiaupdate.Write(status)
	})
}

func applyNVIDIA() error {
	return withLock(func() error {
		status := currentNVIDIAStatus("installing", "Preparing the NVIDIA driver update…")
		_ = nvidiaupdate.Write(status)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		if !nvidiaHardwareDetected(ctx) {
			status.State = "unavailable"
			status.Message = "No NVIDIA graphics hardware was detected."
			status.Progress = 100
			return nvidiaupdate.Write(status)
		}
		status.HardwareDetected = true
		status.Progress = 10
		status.Message = "Refreshing Ubuntu's signed driver repository…"
		_ = nvidiaupdate.Write(status)
		if _, err := run(ctx, "apt-get", "update"); err != nil {
			return failNVIDIAStatus(status, "Could not refresh Ubuntu's driver repository", err)
		}
		var err error
		status, err = inspectNVIDIA(ctx, status)
		if err != nil {
			return failNVIDIAStatus(status, "Could not select a compatible NVIDIA driver", err)
		}
		if !status.UpdateAvailable {
			status.State = "idle"
			status.Message = "The recommended NVIDIA driver is already installed."
			status.Progress = 100
			status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
			return nvidiaupdate.Write(status)
		}
		status.State = "installing"
		status.Progress = 35
		status.Message = fmt.Sprintf("Downloading %s from Ubuntu…", status.RecommendedPackage)
		_ = nvidiaupdate.Write(status)
		if _, err := runEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a"}, "apt-get", "install", "-y", status.RecommendedPackage); err != nil {
			return failNVIDIAStatus(status, "NVIDIA driver installation failed", err)
		}
		status.Progress = 90
		status.Message = "Finishing the driver installation…"
		_ = nvidiaupdate.Write(status)
		status.State = "reboot_required"
		status.UpdateAvailable = false
		status.RebootRequired = true
		status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		status.CheckedAt = status.UpdatedAt
		status.Progress = 100
		status.Message = "Driver installed. Restart CloudlessOS to activate it."
		status.Error = ""
		return nvidiaupdate.Write(status)
	})
}

func currentNVIDIAStatus(state, message string) nvidiaupdate.Status {
	status, err := nvidiaupdate.Read()
	if err != nil {
		status = nvidiaupdate.DefaultStatus()
	}
	status.State = state
	status.Message = message
	status.Error = ""
	status.Progress = 0
	return status
}

func inspectNVIDIA(ctx context.Context, status nvidiaupdate.Status) (nvidiaupdate.Status, error) {
	status.HardwareDetected = true
	status.CurrentDriver = nvidiaDriverVersion(ctx)
	status.SecureBoot = secureBootState(ctx)
	recommended, err := recommendedNVIDIAPackage(ctx)
	if err != nil {
		return status, err
	}
	status.RecommendedPackage = recommended
	status.CandidateVersion, err = candidateVersion(ctx, recommended)
	if err != nil {
		return status, err
	}
	status.InstalledPackage, status.InstalledVersion = installedNVIDIAPackage(ctx, recommended)
	status.UpdateAvailable = status.InstalledPackage != recommended || status.InstalledVersion == ""
	if !status.UpdateAvailable && status.CandidateVersion != "" {
		status.UpdateAvailable = exec.CommandContext(ctx, "dpkg", "--compare-versions", status.CandidateVersion, "gt", status.InstalledVersion).Run() == nil
	}
	return status, nil
}

func nvidiaHardwareDetected(ctx context.Context) bool {
	if out, err := run(ctx, "lspci", "-nn"); err == nil && strings.Contains(strings.ToLower(out), "nvidia") {
		return true
	}
	return exec.CommandContext(ctx, "nvidia-smi", "-L").Run() == nil
}

func recommendedNVIDIAPackage(ctx context.Context) (string, error) {
	out, err := run(ctx, "ubuntu-drivers", "devices")
	if err != nil {
		return "", err
	}
	return parseRecommendedNVIDIAPackage(out)
}

func parseRecommendedNVIDIAPackage(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "recommended") || !strings.Contains(line, "driver") {
			continue
		}
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == ":" && i > 0 && fields[i-1] == "driver" && i+1 < len(fields) {
				return fields[i+1], nil
			}
		}
	}
	return "", errors.New("Ubuntu did not report a recommended NVIDIA driver for this GPU")
}

func installedNVIDIAPackage(ctx context.Context, recommended string) (string, string) {
	if version, err := installedVersion(ctx, recommended); err == nil && version != "" {
		return recommended, version
	}
	out, err := run(ctx, "dpkg-query", "-W", "-f=${db:Status-Abbrev}\t${binary:Package}\t${Version}\n", "nvidia-driver-*")
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "ii" {
			return strings.TrimSuffix(fields[1], ":amd64"), fields[2]
		}
	}
	return "", ""
}

func nvidiaDriverVersion(ctx context.Context) string {
	out, err := run(ctx, "nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader")
	if err != nil {
		return ""
	}
	if line, _, ok := strings.Cut(strings.TrimSpace(out), "\n"); ok {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(out)
}

func secureBootState(ctx context.Context) string {
	out, err := run(ctx, "mokutil", "--sb-state")
	if err != nil {
		return "unknown"
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "enabled") {
		return "enabled"
	}
	if strings.Contains(lower, "disabled") {
		return "disabled"
	}
	return "unknown"
}

func failNVIDIAStatus(status nvidiaupdate.Status, message string, err error) error {
	status.State = "failed"
	status.Message = message
	status.Error = err.Error()
	_ = nvidiaupdate.Write(status)
	return fmt.Errorf("%s: %w", message, err)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cloudless-updater: "+format+"\n", args...)
	os.Exit(1)
}

func withLock(fn func() error) error {
	if err := os.MkdirAll("/run/cloudless-updater", 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile("/run/cloudless-updater/lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another update operation is already running")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func check() error {
	return withLock(func() error {
		status := currentStatus("checking", "Checking updates.becloudless.ai…")
		if !status.Configured {
			status.State = "not_configured"
			status.Message = "Cloudless update signing has not been configured yet."
			return osupdate.Write(status)
		}
		if err := writeProgress(&status, 5, "Preparing the update check…"); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := writeProgress(&status, 15, "Refreshing the signed Cloudless repository…"); err != nil {
			return err
		}
		if _, err := run(ctx, "apt-get", "update"); err != nil {
			return failStatus(status, "Could not refresh the Cloudless repository", err)
		}
		if err := writeProgress(&status, 75, "Comparing installed packages…"); err != nil {
			return err
		}
		status, err := candidates(ctx, status)
		if err != nil {
			return failStatus(status, "Could not inspect package updates", err)
		}
		status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		status.Message = "CloudlessOS is up to date."
		if len(status.Packages) > 0 {
			status.State = "available"
			status.Message = fmt.Sprintf("CloudlessOS %s is ready.", status.AvailableVersion)
		} else {
			status.State = "idle"
		}
		status.Progress = 100
		return osupdate.Write(status)
	})
}

func apply() error {
	return withLock(func() error {
		status := currentStatus("installing", "Preparing the CloudlessOS update…")
		if !status.Configured {
			status.State = "not_configured"
			status.Message = "Cloudless update signing has not been configured yet."
			return osupdate.Write(status)
		}
		if err := writeProgress(&status, 3, "Preparing the CloudlessOS update…"); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := writeProgress(&status, 8, "Refreshing the signed Cloudless repository…"); err != nil {
			return err
		}
		if _, err := run(ctx, "apt-get", "update"); err != nil {
			return failStatus(status, "Could not refresh the Cloudless repository", err)
		}
		if err := writeProgress(&status, 18, "Resolving the verified package update…"); err != nil {
			return err
		}
		var err error
		status, err = candidates(ctx, status)
		if err != nil {
			return failStatus(status, "Could not inspect package updates", err)
		}
		if len(status.Packages) == 0 {
			status.State = "idle"
			status.Message = "CloudlessOS is already up to date."
			status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
			status.Progress = 100
			return osupdate.Write(status)
		}

		if err := writeProgress(&status, 24, "Creating a recovery snapshot…"); err != nil {
			return err
		}
		rollbackDir := filepath.Join("/var/lib/cloudless-updater/rollback", time.Now().UTC().Format("20060102T150405Z"))
		rollbackAvailable := copyDebs("/var/lib/cloudless-updater/current", rollbackDir) == nil

		status.State = "installing"
		if err := writeProgress(&status, 30, fmt.Sprintf("Downloading %d verified package update(s)…", len(status.Packages))); err != nil {
			return err
		}
		args := []string{"install", "-y", "--only-upgrade"}
		for _, pkg := range status.Packages {
			args = append(args, pkg.Name)
		}
		downloadArgs := []string{"install", "-y", "--download-only", "--only-upgrade"}
		for _, pkg := range status.Packages {
			downloadArgs = append(downloadArgs, pkg.Name)
		}
		cachedDebs, _ := filepath.Glob("/var/cache/apt/archives/cloudless-*.deb")
		for _, deb := range cachedDebs {
			_ = os.Remove(deb)
		}
		if _, err := runEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", downloadArgs...); err != nil {
			return failStatus(status, "Could not download the verified update", err)
		}
		if err := writeProgress(&status, 55, "Verifying downloaded packages…"); err != nil {
			return err
		}
		stagedDir := "/var/lib/cloudless-updater/staged"
		_ = os.RemoveAll(stagedDir)
		if err := copyDebs("/var/cache/apt/archives", stagedDir); err != nil {
			return failStatus(status, "Downloaded update packages were not found", err)
		}
		if err := writeProgress(&status, 62, fmt.Sprintf("Installing %d verified package update(s)…", len(status.Packages))); err != nil {
			return err
		}
		if _, err := runEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a"}, "apt-get", args...); err != nil {
			return failStatus(status, "Package installation failed", err)
		}

		if err := writeProgress(&status, 86, "Restarting Cloudless services and checking their health…"); err != nil {
			return err
		}
		_, _ = run(ctx, "systemctl", "try-restart", "cloudlessd.service")
		if !waitHealthy(90 * time.Second) {
			if !rollbackAvailable {
				return failStatus(status, "The update failed its health check and this legacy installation had no rollback generation", errors.New("cloudlessd did not become healthy"))
			}
			if rollbackErr := rollback(ctx, rollbackDir); rollbackErr != nil {
				return failStatus(status, "The update failed its health check and rollback also failed", rollbackErr)
			}
			return failStatus(status, "The update failed its health check and was rolled back", errors.New("cloudlessd did not become healthy"))
		}

		for _, pkg := range status.Packages {
			if pkg.Name == "cloudless-shell" {
				_, _ = run(ctx, "systemctl", "try-restart", "lightdm.service")
			}
			if pkg.Name == "cloudless-branding" || pkg.Name == "cloudless-hardware" {
				status.RebootRequired = true
			}
		}
		if _, err := os.Stat("/run/reboot-required"); err == nil {
			status.RebootRequired = true
		}
		if err := writeProgress(&status, 96, "Saving recovery packages…"); err != nil {
			return err
		}
		if err := copyDebs(stagedDir, "/var/lib/cloudless-updater/current"); err != nil {
			return failStatus(status, "Update installed but its recovery packages could not be retained", err)
		}
		status.State = "updated"
		status.CurrentVersion = status.AvailableVersion
		status.AvailableVersion = ""
		status.ReleaseTitle = ""
		status.ReleaseSummary = ""
		status.Changelog = nil
		status.Packages = nil
		status.Error = ""
		status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		status.CheckedAt = status.UpdatedAt
		status.Message = "Update installed successfully."
		if status.RebootRequired {
			status.State = "reboot_required"
			status.Message = "Update installed. Restart CloudlessOS to finish."
		}
		status.Progress = 100
		return osupdate.Write(status)
	})
}

func currentStatus(state, message string) osupdate.Status {
	status, err := osupdate.Read()
	if err != nil {
		status = osupdate.DefaultStatus()
	}
	status.State = state
	status.Message = message
	status.Error = ""
	status.Progress = 0
	status.Configured = osupdate.Configured()
	return status
}

func writeProgress(status *osupdate.Status, progress int, message string) error {
	if progress < 0 {
		progress = 0
	} else if progress > 100 {
		progress = 100
	}
	status.Progress = progress
	status.Message = message
	return osupdate.Write(*status)
}

func candidates(ctx context.Context, status osupdate.Status) (osupdate.Status, error) {
	status.Packages = nil
	status.AvailableVersion = ""
	status.ReleaseTitle = ""
	status.ReleaseSummary = ""
	status.Changelog = nil
	for _, name := range managedPackages {
		installed, err := installedVersion(ctx, name)
		if err != nil {
			continue
		}
		candidate, err := candidateVersion(ctx, name)
		if err != nil {
			return status, err
		}
		if name == "cloudless-orchestrator" {
			status.CurrentVersion = installed
		}
		if candidate == "" || candidate == installed {
			continue
		}
		newer := exec.CommandContext(ctx, "dpkg", "--compare-versions", candidate, "gt", installed).Run() == nil
		if !newer {
			continue
		}
		status.Packages = append(status.Packages, osupdate.Package{Name: name, Installed: installed, Candidate: candidate})
		if name == "cloudless-orchestrator" {
			status.AvailableVersion = candidate
		}
	}
	if status.AvailableVersion == "" && len(status.Packages) > 0 {
		status.AvailableVersion = status.Packages[0].Candidate
	}
	if status.AvailableVersion != "" {
		release, err := loadReleaseManifest(ctx, status.Channel, status.AvailableVersion)
		if err != nil {
			return status, fmt.Errorf("could not verify release notes: %w", err)
		}
		status.ReleaseTitle = release.Title
		status.ReleaseSummary = release.Summary
		status.Changelog = release.Changes
	}
	return status, nil
}

func loadReleaseManifest(ctx context.Context, channel, version string) (releaseManifest, error) {
	baseURL := strings.TrimRight(os.Getenv("CLOUDLESS_APT_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = defaultAPTBaseURL
	}
	inRelease, err := fetch(ctx, fmt.Sprintf("%s/dists/%s/InRelease", baseURL, channel))
	if err != nil {
		return releaseManifest{}, err
	}
	manifest, err := fetch(ctx, fmt.Sprintf("%s/dists/%s/cloudless-release.json", baseURL, channel))
	if err != nil {
		return releaseManifest{}, err
	}

	tmp, err := os.MkdirTemp("", "cloudless-release-verify-")
	if err != nil {
		return releaseManifest{}, err
	}
	defer os.RemoveAll(tmp)
	inReleasePath := filepath.Join(tmp, "InRelease")
	releasePath := filepath.Join(tmp, "Release")
	if err := os.WriteFile(inReleasePath, inRelease, 0o600); err != nil {
		return releaseManifest{}, err
	}
	if output, err := exec.CommandContext(ctx, "gpgv", "--keyring", archiveKeyring, "--output", releasePath, inReleasePath).CombinedOutput(); err != nil {
		return releaseManifest{}, fmt.Errorf("archive signature is invalid: %w: %s", err, strings.TrimSpace(string(output)))
	}
	release, err := os.ReadFile(releasePath)
	if err != nil {
		return releaseManifest{}, err
	}
	return validateReleaseManifest(release, manifest, version)
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func validateReleaseManifest(release, manifest []byte, version string) (releaseManifest, error) {
	wantHash := fmt.Sprintf("%x", sha256.Sum256(manifest))
	wantSize := strconv.Itoa(len(manifest))
	covered := false
	inSHA256 := false
	for _, line := range strings.Split(string(release), "\n") {
		if line == "SHA256:" {
			inSHA256 = true
			continue
		}
		if inSHA256 && !strings.HasPrefix(line, " ") {
			inSHA256 = false
		}
		if !inSHA256 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == wantHash && fields[1] == wantSize && fields[2] == "cloudless-release.json" {
			covered = true
			break
		}
	}
	if !covered {
		return releaseManifest{}, errors.New("release notes are not covered by signed metadata")
	}
	var parsed releaseManifest
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return releaseManifest{}, fmt.Errorf("invalid release notes: %w", err)
	}
	if parsed.Version != version {
		return releaseManifest{}, fmt.Errorf("release notes describe %s, expected %s", parsed.Version, version)
	}
	if strings.TrimSpace(parsed.Title) == "" || len(parsed.Changes) == 0 {
		return releaseManifest{}, errors.New("release notes are incomplete")
	}
	for _, change := range parsed.Changes {
		if strings.TrimSpace(change) == "" {
			return releaseManifest{}, errors.New("release notes contain an empty change")
		}
	}
	return parsed, nil
}

func installedVersion(ctx context.Context, name string) (string, error) {
	out, err := run(ctx, "dpkg-query", "-W", "-f=${Version}", name)
	return strings.TrimSpace(out), err
}

func candidateVersion(ctx context.Context, name string) (string, error) {
	out, err := run(ctx, "apt-cache", "policy", name)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Candidate:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Candidate:"))
			if value == "(none)" {
				return "", nil
			}
			return value, nil
		}
	}
	return "", nil
}

func rollback(ctx context.Context, dir string) error {
	debs, _ := filepath.Glob(filepath.Join(dir, "*.deb"))
	if len(debs) == 0 {
		return errors.New("no previous packages were available for rollback")
	}
	args := append([]string{"-i"}, debs...)
	if _, err := run(ctx, "dpkg", args...); err != nil {
		return err
	}
	_, _ = run(ctx, "systemctl", "try-restart", "cloudlessd.service")
	return nil
}

func waitHealthy(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://127.0.0.1:8765/api/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

func failStatus(status osupdate.Status, message string, err error) error {
	status.State = "failed"
	status.Message = message
	status.Error = err.Error()
	_ = osupdate.Write(status)
	return fmt.Errorf("%s: %w", message, err)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	return runEnv(ctx, nil, name, args...)
}

func runEnv(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func copyDebs(from, to string) error {
	debs, err := filepath.Glob(filepath.Join(from, "cloudless-*.deb"))
	if err != nil {
		return err
	}
	if len(debs) == 0 {
		return fmt.Errorf("no Cloudless packages in %s", from)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	for _, src := range debs {
		base := filepath.Base(src)
		if split := strings.IndexByte(base, '_'); split > 0 {
			old, _ := filepath.Glob(filepath.Join(to, base[:split+1]+"*.deb"))
			for _, path := range old {
				_ = os.Remove(path)
			}
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(to, base)
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
