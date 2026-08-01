// Package osupdate exposes the on-disk contract shared by cloudlessd and the
// privileged CloudlessOS update worker.
package osupdate

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultStatusPath = "/var/lib/cloudless-updater/status.json"
	channelPath       = "/etc/cloudless/update-channel"
	keyringPath       = "/usr/share/keyrings/cloudless-archive-keyring.pgp"
	sourcesPath       = "/etc/apt/sources.list.d/cloudless.sources"
)

type Package struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Candidate string `json:"candidate"`
}

type Status struct {
	State                 string    `json:"state"`
	Channel               string    `json:"channel"`
	Configured            bool      `json:"configured"`
	CurrentVersion        string    `json:"currentVersion,omitempty"`
	CurrentSourceCommit   string    `json:"currentSourceCommit,omitempty"`
	AvailableVersion      string    `json:"availableVersion,omitempty"`
	AvailableSourceCommit string    `json:"availableSourceCommit,omitempty"`
	ReleaseTitle          string    `json:"releaseTitle,omitempty"`
	ReleaseSummary        string    `json:"releaseSummary,omitempty"`
	QualificationStatus   string    `json:"qualificationStatus,omitempty"`
	QualificationRequired bool      `json:"qualificationRequired,omitempty"`
	Changelog             []string  `json:"changelog,omitempty"`
	Message               string    `json:"message,omitempty"`
	Progress              int       `json:"progress"`
	Error                 string    `json:"error,omitempty"`
	CheckedAt             string    `json:"checkedAt,omitempty"`
	UpdatedAt             string    `json:"updatedAt,omitempty"`
	RebootRequired        bool      `json:"rebootRequired"`
	RebootBootID          string    `json:"rebootBootId,omitempty"`
	Packages              []Package `json:"packages,omitempty"`
}

func StatusPath() string {
	if path := os.Getenv("CLOUDLESS_UPDATE_STATUS"); path != "" {
		return path
	}
	return defaultStatusPath
}

func DefaultStatus() Status {
	return Status{
		State:      "idle",
		Channel:    Channel(),
		Configured: Configured(),
	}
}

func Channel() string {
	b, err := os.ReadFile(ChannelPath())
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return "stable"
	}
	channel := strings.TrimSpace(string(b))
	if !ValidChannel(channel) {
		return "stable"
	}
	return channel
}

func ValidChannel(channel string) bool {
	return channel == "stable" || channel == "beta"
}

// SetChannel changes both the administrator-visible channel and the APT suite.
// Keeping those files in one operation prevents the UI and package manager from
// silently following different release streams.
func SetChannel(channel string) error {
	if !ValidChannel(channel) {
		return errors.New("update channel must be stable or beta")
	}
	sourcePath := SourcesPath()
	sources, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(sources), "\n")
	found := 0
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Suites:") {
			lines[index] = "Suites: " + channel
			found++
		}
	}
	if found != 1 {
		return errors.New("Cloudless APT source must contain exactly one Suites field")
	}
	if err := atomicWrite(sourcePath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return err
	}
	if err := atomicWrite(ChannelPath(), []byte(channel+"\n"), 0o644); err != nil {
		_ = atomicWrite(sourcePath, sources, 0o644)
		return err
	}
	return nil
}

func SourcesPath() string {
	if path := os.Getenv("CLOUDLESS_UPDATE_SOURCES_PATH"); path != "" {
		return path
	}
	return sourcesPath
}

func atomicWrite(path string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cloudless-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func ChannelPath() string {
	if path := os.Getenv("CLOUDLESS_UPDATE_CHANNEL_PATH"); path != "" {
		return path
	}
	return channelPath
}

func Configured() bool {
	if _, err := os.Stat(keyringPath); err != nil {
		return false
	}
	b, err := os.ReadFile(SourcesPath())
	if err != nil {
		return false
	}
	return !strings.Contains(strings.ToLower(string(b)), "enabled: no")
}

func Read() (Status, error) {
	b, err := os.ReadFile(StatusPath())
	if errors.Is(err, os.ErrNotExist) {
		return DefaultStatus(), nil
	}
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal(b, &status); err != nil {
		return Status{}, err
	}
	status.Configured = Configured()
	// The channel file is the administrator-controlled source of truth. A
	// persisted status document may describe an earlier channel and must never
	// redirect release-note verification after the configured channel changes.
	status.Channel = Channel()
	reconcileReboot(&status, currentBootID(), systemBootTime())
	return status, nil
}

func Write(status Status) error {
	path := StatusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	status.Configured = Configured()
	status.Channel = Channel()
	if status.RebootRequired && status.RebootBootID == "" {
		status.RebootBootID = currentBootID()
	}
	b, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	// Update services intentionally use a restrictive umask for their private
	// staging data. The status document is the narrow, non-secret handoff to
	// the unprivileged UI daemon, so enforce its read-only public mode after the
	// write instead of weakening the service-wide umask.
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func currentBootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func systemBootTime() time.Time {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		seconds, err := strconv.ParseInt(fields[1], 10, 64)
		if err == nil {
			return time.Unix(seconds, 0).UTC()
		}
	}
	return time.Time{}
}

func reconcileReboot(status *Status, bootID string, bootedAt time.Time) {
	if !status.RebootRequired {
		return
	}
	rebooted := status.RebootBootID != "" && bootID != "" && status.RebootBootID != bootID
	if status.RebootBootID == "" && !bootedAt.IsZero() {
		if updatedAt, err := time.Parse(time.RFC3339, status.UpdatedAt); err == nil {
			rebooted = bootedAt.After(updatedAt)
		}
	}
	if !rebooted {
		return
	}
	status.RebootRequired = false
	status.RebootBootID = ""
	status.AvailableVersion = ""
	if status.State == "reboot_required" {
		status.State = "updated"
		status.Message = "Update installed successfully."
	}
}
