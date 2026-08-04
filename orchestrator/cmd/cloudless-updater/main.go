package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/nvidiaupdate"
	"github.com/cloudless/orchestrator/internal/osupdate"
	"github.com/cloudless/orchestrator/internal/platform"
	"github.com/cloudless/orchestrator/internal/securityaudit"
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
	controlPlaneURL   = "http://127.0.0.1:8765"
	qualificationRoot = "/var/lib/cloudless/qualification"
)

type qualificationPointer struct {
	Schema         string `json:"schema"`
	Campaign       string `json:"campaign"`
	CampaignSHA256 string `json:"campaignSha256"`
	Target         string `json:"target"`
	Version        string `json:"version"`
	SourceCommit   string `json:"sourceCommit"`
}

type qualificationCampaign struct {
	Schema       string `json:"schema"`
	Version      string `json:"version"`
	SourceCommit string `json:"sourceCommit"`
	Target       struct {
		ID string `json:"id"`
	} `json:"target"`
}

type workloadSnapshot struct {
	ReadyEngine        string
	RecipeOperationIDs []string
}

type engineContinuityState struct {
	Active   string `json:"active"`
	Ready    bool   `json:"ready"`
	Unloaded bool   `json:"unloaded"`
}

type recipeContinuityState struct {
	Operations []struct {
		ID    string `json:"id"`
		Phase string `json:"phase"`
		Kind  string `json:"kind"`
	} `json:"operations"`
}

var (
	updaterConfigured         = osupdate.Configured
	updaterCandidates         = candidates
	updaterRun                = run
	updaterRunEnv             = runEnv
	updaterCopyDebs           = copyDebs
	updaterCaptureWorkload    = captureWorkload
	updaterWaitContinuity     = waitForWorkloadContinuity
	updaterRollback           = rollback
	updaterControlPlaneURL    = controlPlaneURL
	updaterContinuityWindow   = 90 * time.Second
	updaterRollbackRoot       = "/var/lib/cloudless-updater/rollback"
	updaterCurrentDir         = "/var/lib/cloudless-updater/current"
	updaterAPTCacheDir        = "/var/cache/apt/archives"
	updaterStagedDir          = "/var/lib/cloudless-updater/staged"
	updaterQualificationRoot  = qualificationRoot
	updaterEffectiveUID       = os.Geteuid
	updaterDGXApplianceMarker = "/etc/cloudless/dgx-appliance"
)

type releaseManifest struct {
	Schema                string                       `json:"schema"`
	Version               string                       `json:"version"`
	Channel               string                       `json:"channel"`
	SourceCommit          string                       `json:"sourceCommit"`
	PublishedAt           string                       `json:"publishedAt"`
	Title                 string                       `json:"title"`
	Summary               string                       `json:"summary"`
	Changes               []string                     `json:"changes"`
	Compatibility         releaseCompatibility         `json:"compatibility"`
	PhysicalQualification releasePhysicalQualification `json:"physicalQualification"`
	SecurityReadiness     releaseSecurityReadiness     `json:"securityReadiness"`
	CIQualification       releaseCIQualification       `json:"ciQualification"`
}

type releasePhysicalQualification struct {
	Schema                 string `json:"schema"`
	Status                 string `json:"status"`
	Required               bool   `json:"required"`
	Version                string `json:"version"`
	Channel                string `json:"channel"`
	SourceCommit           string `json:"sourceCommit"`
	QualificationSetSHA256 string `json:"qualificationSetSha256"`
}

type releaseSecurityReadiness struct {
	Schema                      string `json:"schema"`
	Status                      string `json:"status"`
	Required                    bool   `json:"required"`
	Version                     string `json:"version"`
	Channel                     string `json:"channel"`
	SourceCommit                string `json:"sourceCommit"`
	Monitored                   bool   `json:"monitored"`
	SecurityContact             string `json:"securityContact"`
	EscalationOwner             string `json:"escalationOwner"`
	VerifiedAt                  string `json:"verifiedAt"`
	AcknowledgementBusinessDays int    `json:"acknowledgementBusinessDays"`
}

type releaseCIQualification struct {
	Schema           string              `json:"schema"`
	Status           string              `json:"status"`
	Required         bool                `json:"required"`
	Version          string              `json:"version"`
	Channel          string              `json:"channel"`
	SourceCommit     string              `json:"sourceCommit"`
	Runner           string              `json:"runner"`
	Workflow         string              `json:"workflow"`
	RunID            int64               `json:"runId"`
	RunAttempt       int64               `json:"runAttempt"`
	Jobs             []string            `json:"jobs"`
	Artifacts        []string            `json:"artifacts"`
	JobEvidence      []releaseCIEvidence `json:"jobEvidence"`
	ArtifactEvidence []releaseCIEvidence `json:"artifactEvidence"`
}

type releaseCIEvidence struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type releaseCompatibility struct {
	Schema  string          `json:"schema"`
	Targets []releaseTarget `json:"targets"`
}

type releaseTarget struct {
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
}

func main() {
	if len(os.Args) != 2 {
		fatalf("usage: cloudless-updater check|apply|qualification-rollback|status|nvidia-check|nvidia-apply|nvidia-status")
	}
	command := os.Args[1]
	audited := command == "check" || command == "apply" || command == "qualification-rollback" || command == "nvidia-check" || command == "nvidia-apply"
	if audited {
		auditUpdater(command, "started", "")
	}
	var err error
	switch command {
	case "check":
		err = check()
	case "apply":
		err = apply()
	case "qualification-rollback":
		err = qualificationRollback()
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
		if audited {
			auditUpdater(command, "failed", err.Error())
		}
		fatalf("%v", err)
	}
	if audited {
		auditUpdater(command, "succeeded", "")
	}
}

func auditUpdater(command, outcome, detail string) {
	dir := strings.TrimSpace(os.Getenv("CLOUDLESS_STATE_DIR"))
	if dir == "" {
		dir = "/var/lib/cloudless"
	}
	securityaudit.New(dir).Append(securityaudit.Event{
		Category: "update", Event: command, Outcome: outcome, Actor: "cloudless-updater",
		Target: "CloudlessOS", Detail: detail,
	})
}

func checkNVIDIA() error {
	return withLock(func() error {
		if platform.IsDGXSpark() {
			return nvidiaupdate.Write(managedDGXNVIDIAStatus())
		}
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
		if platform.IsDGXSpark() {
			return nvidiaupdate.Write(managedDGXNVIDIAStatus())
		}
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

func managedDGXNVIDIAStatus() nvidiaupdate.Status {
	status := currentNVIDIAStatus("managed", "NVIDIA drivers, CUDA, and firmware are managed by DGX OS.")
	status.HardwareDetected = true
	status.UpdateAvailable = false
	status.Progress = 100
	status.RebootRequired = false
	status.RebootBootID = ""
	status.RecommendedPackage = ""
	status.CandidateVersion = ""
	status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	return status
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
			return binaryPackageName(fields[1]), fields[2]
		}
	}
	return "", ""
}

func binaryPackageName(name string) string {
	return strings.SplitN(name, ":", 2)[0]
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
	return withLock(applyLocked)
}

func applyLocked() error {
	return applyWithMode("", "", false)
}

func readQualificationJSON(path string, limit int64, target any) ([]byte, error) {
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	handle := os.NewFile(uintptr(descriptor), path)
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("qualification identity is not a regular file: %s", path)
	}
	payload, err := io.ReadAll(io.LimitReader(handle, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) == 0 || int64(len(payload)) > limit {
		return nil, fmt.Errorf("qualification identity has an invalid size: %s", path)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return nil, fmt.Errorf("qualification identity is malformed: %w", err)
	}
	return payload, nil
}

func qualificationRollbackPreflight() (qualificationCampaign, error) {
	if updaterEffectiveUID() != 0 {
		return qualificationCampaign{}, errors.New("qualification rollback requires root")
	}
	rootInfo, err := os.Lstat(updaterQualificationRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return qualificationCampaign{}, errors.New("qualification rollback requires a real qualification root")
	}
	root, err := filepath.EvalSymlinks(updaterQualificationRoot)
	if err != nil {
		return qualificationCampaign{}, fmt.Errorf("resolve qualification root: %w", err)
	}
	var pointer qualificationPointer
	_, err = readQualificationJSON(filepath.Join(root, "active-campaign.json"), 64<<10, &pointer)
	if err != nil {
		return qualificationCampaign{}, fmt.Errorf("read active qualification campaign: %w", err)
	}
	if pointer.Schema != "cloudless.physical-active-campaign.v1" {
		return qualificationCampaign{}, errors.New("active qualification campaign has an unsupported schema")
	}
	relative := filepath.Clean(pointer.Campaign)
	if relative == "." || relative == ".." || relative != pointer.Campaign || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return qualificationCampaign{}, errors.New("active qualification campaign path is unsafe")
	}
	campaignPath := filepath.Join(root, relative)
	campaignInfo, err := os.Lstat(campaignPath)
	if err != nil || !campaignInfo.IsDir() || campaignInfo.Mode()&os.ModeSymlink != 0 {
		return qualificationCampaign{}, errors.New("active qualification campaign is not a real directory")
	}
	resolvedCampaign, err := filepath.EvalSymlinks(campaignPath)
	if err != nil {
		return qualificationCampaign{}, fmt.Errorf("resolve active qualification campaign: %w", err)
	}
	withinRoot, err := filepath.Rel(root, resolvedCampaign)
	if err != nil || withinRoot == "." || withinRoot == ".." || strings.HasPrefix(withinRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(withinRoot) {
		return qualificationCampaign{}, errors.New("active qualification campaign escapes the qualification root")
	}
	var campaign qualificationCampaign
	payload, err := readQualificationJSON(filepath.Join(resolvedCampaign, "campaign.json"), 1<<20, &campaign)
	if err != nil {
		return qualificationCampaign{}, fmt.Errorf("read qualification campaign: %w", err)
	}
	if campaign.Schema != "cloudless.physical-evidence.v1" || strings.TrimSpace(campaign.Version) == "" || !validSourceCommit(campaign.SourceCommit) {
		return qualificationCampaign{}, errors.New("active qualification campaign identity is invalid")
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != pointer.CampaignSHA256 {
		return qualificationCampaign{}, errors.New("active qualification campaign identity has changed")
	}
	if pointer.Target != campaign.Target.ID || pointer.Version != campaign.Version || pointer.SourceCommit != campaign.SourceCommit {
		return qualificationCampaign{}, errors.New("active qualification pointer does not match its campaign")
	}
	if _, err := os.Lstat(filepath.Join(resolvedCampaign, "qualification-result.json")); err == nil {
		return qualificationCampaign{}, errors.New("active qualification campaign is already sealed")
	} else if !errors.Is(err, os.ErrNotExist) {
		return qualificationCampaign{}, fmt.Errorf("inspect qualification result: %w", err)
	}
	return campaign, nil
}

func qualificationRollback() error {
	campaign, err := qualificationRollbackPreflight()
	if err != nil {
		return err
	}
	return withLock(func() error { return applyWithMode(campaign.Version, campaign.SourceCommit, true) })
}

func applyWithMode(expectedVersion, expectedSourceCommit string, forceRollback bool) error {
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
	if _, err := updaterRun(ctx, "apt-get", "update"); err != nil {
		return failStatus(status, "Could not refresh the Cloudless repository", err)
	}
	if err := writeProgress(&status, 18, "Resolving the verified package update…"); err != nil {
		return err
	}
	var err error
	status, err = updaterCandidates(ctx, status)
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
	if forceRollback && (status.AvailableVersion != expectedVersion || status.AvailableSourceCommit != expectedSourceCommit) {
		return failStatus(
			status,
			"Qualification rollback refused because the available update is not the active campaign",
			fmt.Errorf(
				"active campaign expects %q at %s, but the signed repository offers %q at %s",
				expectedVersion,
				expectedSourceCommit,
				status.AvailableVersion,
				status.AvailableSourceCommit,
			),
		)
	}

	args := []string{"install", "-y", "--only-upgrade"}
	for _, pkg := range status.Packages {
		args = append(args, pkg.Name)
	}
	if platform.IsDGXSpark() {
		if err := validateDGXUpdatePlan(ctx, args); err != nil {
			return failStatus(status, "Update refused to preserve the NVIDIA-managed DGX OS stack", err)
		}
	}

	if err := writeProgress(&status, 24, "Creating a recovery snapshot…"); err != nil {
		return err
	}
	rollbackDir := filepath.Join(updaterRollbackRoot, time.Now().UTC().Format("20060102T150405Z"))
	rollbackAvailable := updaterCopyDebs(updaterCurrentDir, rollbackDir) == nil

	status.State = "installing"
	if err := writeProgress(&status, 30, fmt.Sprintf("Downloading %d verified package update(s)…", len(status.Packages))); err != nil {
		return err
	}
	downloadArgs := []string{"install", "-y", "--download-only", "--only-upgrade"}
	for _, pkg := range status.Packages {
		downloadArgs = append(downloadArgs, pkg.Name)
	}
	cachedDebs, _ := filepath.Glob(filepath.Join(updaterAPTCacheDir, "cloudless-*.deb"))
	for _, deb := range cachedDebs {
		_ = os.Remove(deb)
	}
	if _, err := updaterRunEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", downloadArgs...); err != nil {
		return failStatus(status, "Could not download the verified update", err)
	}
	if err := writeProgress(&status, 55, "Verifying downloaded packages…"); err != nil {
		return err
	}
	stagedDir := updaterStagedDir
	_ = os.RemoveAll(stagedDir)
	if err := updaterCopyDebs(updaterAPTCacheDir, stagedDir); err != nil {
		return failStatus(status, "Downloaded update packages were not found", err)
	}
	if err := writeProgress(&status, 62, fmt.Sprintf("Installing %d verified package update(s)…", len(status.Packages))); err != nil {
		return err
	}
	workload, err := updaterCaptureWorkload(ctx, updaterControlPlaneURL)
	if err != nil {
		return failStatus(status, "The update could not safely snapshot the current AI workload", err)
	}
	if forceRollback && (workload.ReadyEngine == "" || len(workload.RecipeOperationIDs) == 0) {
		return failStatus(
			status,
			"Qualification rollback requires an active model and a durable preparation operation",
			errors.New("start a model and a recipe preparation before running the qualification rollback"),
		)
	}
	if _, err := updaterRunEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "CLOUDLESS_UPDATE_TRANSACTION=1"}, "apt-get", args...); err != nil {
		return failStatus(status, "Package installation failed", err)
	}

	if err := writeProgress(&status, 86, "Restarting Cloudless services and checking their health…"); err != nil {
		return err
	}
	// cloudlessd and cloudless-engine share a strict typed protocol. Restart the
	// broker first so the newly installed daemon never talks to the previous
	// broker schema during a normal update.
	_, _ = updaterRun(ctx, "systemctl", "try-restart", "cloudless-engine.service")
	_, _ = updaterRun(ctx, "systemctl", "try-restart", "cloudlessd.service")
	continuityErr := updaterWaitContinuity(updaterControlPlaneURL, workload, updaterContinuityWindow)
	if forceRollback && continuityErr == nil {
		continuityErr = errors.New("qualification requested a deliberate rollback after the candidate continuity probe passed")
	}
	if err := continuityErr; err != nil {
		if !rollbackAvailable {
			return failStatus(status, "The update failed its workload continuity check and this legacy installation had no rollback generation", err)
		}
		if rollbackErr := updaterRollback(ctx, rollbackDir); rollbackErr != nil {
			return failStatus(status, "The update failed its workload continuity check and rollback also failed", errors.Join(err, rollbackErr))
		}
		if rollbackContinuityErr := updaterWaitContinuity(updaterControlPlaneURL, workload, updaterContinuityWindow); rollbackContinuityErr != nil {
			return failStatus(status, "The update was rolled back but the previous AI workload could not be recovered", errors.Join(err, rollbackContinuityErr))
		}
		return failStatus(status, "The update failed its workload continuity check and was rolled back", err)
	}

	for _, pkg := range status.Packages {
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
	if err := updaterCopyDebs(stagedDir, updaterCurrentDir); err != nil {
		return failStatus(status, "Update installed but its recovery packages could not be retained", err)
	}
	status.State = "updated"
	status.CurrentVersion = status.AvailableVersion
	status.CurrentSourceCommit = status.AvailableSourceCommit
	status.AvailableVersion = ""
	status.AvailableSourceCommit = ""
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
	if err := osupdate.Write(status); err != nil {
		return err
	}
	restartKioskAfterUpdate(ctx)
	return nil
}

// restartKioskAfterUpdate reloads the UI only after the candidate generation is
// installed, its workload continuity check passes, and the final update status
// is durable. The UI is embedded in cloudless-orchestrator, so tying this to a
// cloudless-shell package upgrade leaves orchestrator-only releases running the
// old page indefinitely. Side-by-side DGX installations do not own LightDM and
// must never have their desktop session restarted by Cloudless.
func restartKioskAfterUpdate(ctx context.Context) {
	if platform.IsDGXSpark() {
		if _, err := os.Stat(updaterDGXApplianceMarker); err != nil {
			return
		}
	}
	if _, err := updaterRun(ctx, "systemctl", "is-active", "--quiet", "lightdm.service"); err != nil {
		return
	}
	_, _ = updaterRun(ctx, "systemctl", "restart", "lightdm.service")
}

func validateDGXUpdatePlan(ctx context.Context, installArgs []string) error {
	simulateArgs := append([]string{"-s"}, installArgs...)
	output, err := run(ctx, "apt-get", simulateArgs...)
	if err != nil {
		return fmt.Errorf("could not simulate the update safely: %w", err)
	}
	protected := protectedDGXPackageChanges(output)
	if len(protected) > 0 {
		return fmt.Errorf("APT planned changes to vendor-owned packages: %s", strings.Join(protected, ", "))
	}
	return nil
}

func protectedDGXPackageChanges(output string) []string {
	seen := make(map[string]bool)
	var protected []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != "Inst" && fields[0] != "Remv" && fields[0] != "Purg") {
			continue
		}
		name := binaryPackageName(strings.Trim(fields[1], "[]"))
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "nvidia") &&
			!strings.HasPrefix(lower, "cuda-") &&
			!strings.HasPrefix(lower, "dgx-") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			protected = append(protected, name)
		}
	}
	return protected
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
	status.Configured = updaterConfigured()
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
	previousVersion := status.CurrentVersion
	previousSourceCommit := status.CurrentSourceCommit
	status.Packages = nil
	status.CurrentVersion = ""
	status.AvailableVersion = ""
	status.AvailableSourceCommit = ""
	status.CurrentSourceCommit = ""
	status.ReleaseTitle = ""
	status.ReleaseSummary = ""
	status.QualificationStatus = ""
	status.QualificationRequired = false
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
	if status.CurrentVersion == previousVersion && validSourceCommit(previousSourceCommit) {
		status.CurrentSourceCommit = strings.ToLower(previousSourceCommit)
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
		status.AvailableSourceCommit = release.SourceCommit
		status.QualificationStatus = release.PhysicalQualification.Status
		status.QualificationRequired = release.PhysicalQualification.Required
	} else if status.CurrentVersion != "" {
		release, err := loadReleaseManifest(ctx, status.Channel, status.CurrentVersion)
		if err != nil {
			return status, fmt.Errorf("could not verify installed release identity: %w", err)
		}
		status.CurrentSourceCommit = release.SourceCommit
		status.QualificationStatus = release.PhysicalQualification.Status
		status.QualificationRequired = release.PhysicalQualification.Required
	}
	return status, nil
}

func validSourceCommit(commit string) bool {
	if len(commit) != 40 {
		return false
	}
	_, err := hex.DecodeString(commit)
	return err == nil
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
	hash, _, err := releaseManifestIdentity(release)
	if err != nil {
		return releaseManifest{}, err
	}
	manifest, err := fetch(ctx, fmt.Sprintf("%s/dists/%s/by-hash/SHA256/%s", baseURL, channel, hash))
	if err != nil {
		return releaseManifest{}, err
	}
	return validateReleaseManifest(release, manifest, version, channel)
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

func validateReleaseManifest(release, manifest []byte, version, channel string) (releaseManifest, error) {
	wantHash, wantSize, err := releaseManifestIdentity(release)
	if err != nil {
		return releaseManifest{}, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(manifest)) != wantHash || int64(len(manifest)) != wantSize {
		return releaseManifest{}, errors.New("release notes do not match signed metadata")
	}
	var parsed releaseManifest
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return releaseManifest{}, fmt.Errorf("invalid release notes: %w", err)
	}
	if parsed.Version != version {
		return releaseManifest{}, fmt.Errorf("release notes describe %s, expected %s", parsed.Version, version)
	}
	if parsed.Schema != "cloudless.release.v2" || parsed.Channel != channel {
		return releaseManifest{}, errors.New("release identity or channel is incompatible")
	}
	if len(parsed.SourceCommit) != 40 {
		return releaseManifest{}, errors.New("release source identity is incomplete")
	}
	if _, err := hex.DecodeString(parsed.SourceCommit); err != nil {
		return releaseManifest{}, errors.New("release source identity is malformed")
	}
	compatible := false
	if parsed.Compatibility.Schema == "cloudless.compatibility.v1" {
		for _, target := range parsed.Compatibility.Targets {
			if target.Platform == platform.Detect() && target.Architecture == platform.Architecture() {
				compatible = true
				break
			}
		}
	}
	if !compatible {
		return releaseManifest{}, fmt.Errorf("release does not support %s/%s", platform.Detect(), platform.Architecture())
	}
	if err := validatePhysicalQualification(parsed); err != nil {
		return releaseManifest{}, err
	}
	if err := validateSecurityReadiness(parsed); err != nil {
		return releaseManifest{}, err
	}
	if err := validateCIQualification(parsed); err != nil {
		return releaseManifest{}, err
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

func validatePhysicalQualification(release releaseManifest) error {
	major := 0
	if fields := strings.SplitN(release.Version, ".", 2); len(fields) > 0 {
		major, _ = strconv.Atoi(fields[0])
	}
	required := release.Channel == "stable" && major >= 1
	physical := release.PhysicalQualification
	if physical.Schema == "" {
		if required {
			return errors.New("stable CloudlessOS 1.0+ release is missing physical qualification")
		}
		return nil // Legacy pre-1.0 signed releases did not carry this descriptor.
	}
	if physical.Schema != "cloudless.physical-release.v1" ||
		physical.Version != release.Version || physical.Channel != release.Channel ||
		physical.SourceCommit != release.SourceCommit || physical.Required != required {
		return errors.New("release physical qualification identity is invalid")
	}
	if physical.Status != "qualified" && physical.Status != "not-qualified" {
		return errors.New("release physical qualification status is invalid")
	}
	if required && physical.Status != "qualified" {
		return errors.New("stable CloudlessOS 1.0+ release is not physically qualified")
	}
	if physical.Status == "qualified" {
		if len(physical.QualificationSetSHA256) != 64 {
			return errors.New("release physical qualification set identity is incomplete")
		}
		if _, err := hex.DecodeString(physical.QualificationSetSHA256); err != nil {
			return errors.New("release physical qualification set identity is malformed")
		}
	}
	return nil
}

func validateSecurityReadiness(release releaseManifest) error {
	major := 0
	if fields := strings.SplitN(release.Version, ".", 2); len(fields) > 0 {
		major, _ = strconv.Atoi(fields[0])
	}
	required := release.Channel == "stable" && major >= 1
	readiness := release.SecurityReadiness
	if readiness.Schema == "" {
		if required {
			return errors.New("stable CloudlessOS 1.0+ release is missing security readiness")
		}
		return nil // Legacy pre-1.0 signed releases did not carry this descriptor.
	}
	if readiness.Schema != "cloudless.security-readiness.v1" ||
		readiness.Version != release.Version || readiness.Channel != release.Channel ||
		readiness.SourceCommit != release.SourceCommit || readiness.Required != required {
		return errors.New("release security readiness identity is invalid")
	}
	if readiness.Status != "operational" && readiness.Status != "not-operational" {
		return errors.New("release security readiness status is invalid")
	}
	if required && readiness.Status != "operational" {
		return errors.New("stable CloudlessOS 1.0+ release has no operational security response owner")
	}
	if readiness.Status != "operational" {
		return nil
	}
	contact, err := url.Parse(readiness.SecurityContact)
	validMail := err == nil && contact.Scheme == "mailto" && strings.Contains(contact.Opaque, "@") && contact.RawQuery == "" && contact.Fragment == ""
	validHTTPS := err == nil && contact.Scheme == "https" && contact.Host != "" && contact.User == nil
	owner := strings.TrimSpace(readiness.EscalationOwner)
	if !readiness.Monitored || owner == "" || len(owner) > 120 || len(readiness.SecurityContact) > 254 || (!validMail && !validHTTPS) {
		return errors.New("release operational security response evidence is incomplete")
	}
	if readiness.AcknowledgementBusinessDays < 1 || readiness.AcknowledgementBusinessDays > 3 {
		return errors.New("release security acknowledgement target is invalid")
	}
	verified, err := time.Parse(time.RFC3339, readiness.VerifiedAt)
	if err != nil {
		return errors.New("release security verification timestamp is invalid")
	}
	published, err := time.Parse(time.RFC3339, release.PublishedAt)
	if err != nil {
		return errors.New("release publication timestamp is invalid")
	}
	if verified.After(published.Add(5*time.Minute)) || published.Sub(verified) > 30*24*time.Hour {
		return errors.New("release security verification is outside the allowed 30-day publication window")
	}
	return nil
}

func validateCIQualification(release releaseManifest) error {
	major := 0
	if fields := strings.SplitN(release.Version, ".", 2); len(fields) > 0 {
		major, _ = strconv.Atoi(fields[0])
	}
	required := release.Channel == "stable" && major >= 1
	qualification := release.CIQualification
	if qualification.Schema == "" {
		if required {
			return errors.New("stable CloudlessOS 1.0+ release is missing exact-commit CI qualification")
		}
		return nil // Legacy pre-1.0 signed releases did not carry this descriptor.
	}
	if qualification.Schema != "cloudless.ci-qualification.v1" ||
		qualification.Version != release.Version || qualification.Channel != release.Channel ||
		qualification.SourceCommit != release.SourceCommit || qualification.Required != required {
		return errors.New("release CI qualification identity is invalid")
	}
	if qualification.Status != "qualified" && qualification.Status != "not-qualified" {
		return errors.New("release CI qualification status is invalid")
	}
	if required && qualification.Status != "qualified" {
		return errors.New("stable CloudlessOS 1.0+ release lacks exact-commit CI qualification")
	}
	if qualification.Status != "qualified" {
		return nil
	}
	if qualification.Runner != "cloudless-local-release" || qualification.Workflow != "distro/scripts/run-local-qualification.sh" || qualification.RunID <= 0 || qualification.RunAttempt <= 0 {
		return errors.New("release CI workflow evidence is invalid")
	}
	requiredJobs := map[string]bool{
		"Source, concurrency and security policies": false,
		"generic / amd64":                  false,
		"generic / arm64":                  false,
		"dgx-spark / arm64":                false,
		"AMD64 and ARM64 package payloads": false,
		"Interface capture smoke test":     false,
		"Durable lifecycle soak":           false,
	}
	for _, job := range qualification.Jobs {
		if _, ok := requiredJobs[job]; !ok || requiredJobs[job] {
			return errors.New("release CI job evidence is invalid")
		}
		requiredJobs[job] = true
	}
	if len(qualification.Jobs) != len(requiredJobs) {
		return errors.New("release CI job evidence is incomplete")
	}
	requiredArtifacts := map[string]bool{
		fmt.Sprintf("package-qualification-%d-%d", qualification.RunID, qualification.RunAttempt): false,
		fmt.Sprintf("visual-regression-%d-%d", qualification.RunID, qualification.RunAttempt):     false,
		fmt.Sprintf("lifecycle-soak-%d-%d", qualification.RunID, qualification.RunAttempt):        false,
	}
	for _, artifact := range qualification.Artifacts {
		if _, ok := requiredArtifacts[artifact]; !ok || requiredArtifacts[artifact] {
			return errors.New("release CI artifact evidence is invalid")
		}
		requiredArtifacts[artifact] = true
	}
	if len(qualification.Artifacts) != len(requiredArtifacts) {
		return errors.New("release CI artifact evidence is incomplete")
	}
	if err := validateRetainedCIEvidence(qualification.JobEvidence, requiredJobs); err != nil {
		return fmt.Errorf("release CI job evidence is invalid: %w", err)
	}
	if err := validateRetainedCIEvidence(qualification.ArtifactEvidence, requiredArtifacts); err != nil {
		return fmt.Errorf("release CI artifact evidence is invalid: %w", err)
	}
	return nil
}

func validateRetainedCIEvidence(records []releaseCIEvidence, required map[string]bool) error {
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if _, ok := required[record.Name]; !ok || seen[record.Name] {
			return errors.New("unexpected or duplicate retained record")
		}
		if record.File == "" || filepath.Base(record.File) != record.File || len(record.SHA256) != sha256.Size*2 || record.Size <= 0 {
			return errors.New("retained record identity is malformed")
		}
		if _, err := hex.DecodeString(record.SHA256); err != nil {
			return errors.New("retained record digest is malformed")
		}
		seen[record.Name] = true
	}
	if len(seen) != len(required) {
		return errors.New("retained record inventory is incomplete")
	}
	return nil
}

func releaseManifestIdentity(release []byte) (string, int64, error) {
	inSHA256 := false
	foundHash := ""
	var foundSize int64
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
		if len(fields) != 3 || fields[2] != "cloudless-release.json" {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", 0, errors.New("release notes have an invalid signed SHA-256 identity")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", 0, errors.New("release notes have an invalid signed SHA-256 identity")
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || size < 1 || size > 1<<20 {
			return "", 0, errors.New("release notes have an invalid signed size")
		}
		if foundHash != "" {
			return "", 0, errors.New("release notes have an ambiguous signed identity")
		}
		foundHash, foundSize = strings.ToLower(fields[0]), size
	}
	if foundHash == "" {
		return "", 0, errors.New("release notes are not covered by signed metadata")
	}
	return foundHash, foundSize, nil
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
	if _, err := runEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive", "CLOUDLESS_UPDATE_TRANSACTION=1"}, "dpkg", args...); err != nil {
		return err
	}
	_, _ = run(ctx, "systemctl", "try-restart", "cloudless-engine.service")
	_, _ = run(ctx, "systemctl", "try-restart", "cloudlessd.service")
	return nil
}

func captureWorkload(ctx context.Context, baseURL string) (workloadSnapshot, error) {
	var engine engineContinuityState
	if err := fetchJSON(ctx, baseURL+"/api/engine", &engine); err != nil {
		return workloadSnapshot{}, fmt.Errorf("read engine state: %w", err)
	}
	var recipes recipeContinuityState
	if err := fetchJSON(ctx, baseURL+"/api/recipes", &recipes); err != nil {
		return workloadSnapshot{}, fmt.Errorf("read recipe operation journal: %w", err)
	}
	snapshot := workloadSnapshot{}
	if engine.Ready && !engine.Unloaded {
		snapshot.ReadyEngine = strings.TrimSpace(engine.Active)
		if snapshot.ReadyEngine == "" {
			return workloadSnapshot{}, errors.New("engine reported ready without an active engine identity")
		}
	}
	for _, operation := range recipes.Operations {
		if recipeOperationNeedsContinuity(operation.Kind, operation.Phase) {
			snapshot.RecipeOperationIDs = append(snapshot.RecipeOperationIDs, operation.ID)
		}
	}
	return snapshot, nil
}

func recipeOperationNeedsContinuity(kind, phase string) bool {
	if kind == "check" && phase == "prepared" {
		return false
	}
	switch phase {
	case "active", "stopped", "failed", "aborted", "":
		return false
	default:
		return true
	}
}

func fetchJSON(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func workloadContinuityError(ctx context.Context, baseURL string, snapshot workloadSnapshot) error {
	var engine engineContinuityState
	if err := fetchJSON(ctx, baseURL+"/api/engine", &engine); err != nil {
		return fmt.Errorf("cloudlessd health is unavailable: %w", err)
	}
	if snapshot.ReadyEngine != "" && (!engine.Ready || engine.Unloaded || engine.Active != snapshot.ReadyEngine) {
		return fmt.Errorf("ready engine %q was not preserved (active=%q ready=%t unloaded=%t)", snapshot.ReadyEngine, engine.Active, engine.Ready, engine.Unloaded)
	}
	if len(snapshot.RecipeOperationIDs) == 0 {
		return nil
	}
	var recipes recipeContinuityState
	if err := fetchJSON(ctx, baseURL+"/api/recipes", &recipes); err != nil {
		return fmt.Errorf("recipe operation journal is unavailable: %w", err)
	}
	byID := make(map[string]string, len(recipes.Operations))
	for _, operation := range recipes.Operations {
		byID[operation.ID] = operation.Phase
	}
	for _, operationID := range snapshot.RecipeOperationIDs {
		phase, ok := byID[operationID]
		if !ok {
			return fmt.Errorf("recipe operation %s disappeared during the update", operationID)
		}
		switch phase {
		case "failed", "aborted", "stopped":
			return fmt.Errorf("recipe operation %s ended as %s during the update", operationID, phase)
		}
	}
	return nil
}

func waitForWorkloadContinuity(baseURL string, snapshot workloadSnapshot, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		lastErr = workloadContinuityError(ctx, baseURL, snapshot)
		cancel()
		if lastErr == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	if lastErr == nil {
		lastErr = errors.New("workload continuity was not established")
	}
	return lastErr
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
