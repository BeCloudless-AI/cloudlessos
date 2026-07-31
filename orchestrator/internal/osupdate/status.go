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
	b, err := os.ReadFile(channelPath)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return "stable"
	}
	return strings.TrimSpace(string(b))
}

func Configured() bool {
	if _, err := os.Stat(keyringPath); err != nil {
		return false
	}
	b, err := os.ReadFile(sourcesPath)
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
	if status.Channel == "" {
		status.Channel = Channel()
	}
	reconcileReboot(&status, currentBootID(), systemBootTime())
	return status, nil
}

func Write(status Status) error {
	path := StatusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	status.Configured = Configured()
	if status.Channel == "" {
		status.Channel = Channel()
	}
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
