// Package nvidiaupdate defines the on-disk contract shared by cloudlessd and
// the privileged NVIDIA driver update worker.
package nvidiaupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultStatusPath = "/var/lib/cloudless-updater/nvidia-driver.json"

type Status struct {
	State              string `json:"state"`
	HardwareDetected   bool   `json:"hardwareDetected"`
	CurrentDriver      string `json:"currentDriver,omitempty"`
	InstalledPackage   string `json:"installedPackage,omitempty"`
	InstalledVersion   string `json:"installedVersion,omitempty"`
	RecommendedPackage string `json:"recommendedPackage,omitempty"`
	CandidateVersion   string `json:"candidateVersion,omitempty"`
	UpdateAvailable    bool   `json:"updateAvailable"`
	SecureBoot         string `json:"secureBoot,omitempty"`
	Message            string `json:"message,omitempty"`
	Progress           int    `json:"progress"`
	Error              string `json:"error,omitempty"`
	CheckedAt          string `json:"checkedAt,omitempty"`
	UpdatedAt          string `json:"updatedAt,omitempty"`
	RebootRequired     bool   `json:"rebootRequired"`
	RebootBootID       string `json:"rebootBootId,omitempty"`
}

func StatusPath() string {
	if path := os.Getenv("CLOUDLESS_NVIDIA_STATUS"); path != "" {
		return path
	}
	return defaultStatusPath
}

func DefaultStatus() Status {
	return Status{
		State:   "idle",
		Message: "Driver updates have not been checked yet.",
	}
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
	reconcileReboot(&status, currentBootID())
	return status, nil
}

func Write(status Status) error {
	path := StatusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
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

func reconcileReboot(status *Status, bootID string) {
	if !status.RebootRequired || status.RebootBootID == "" || bootID == "" || status.RebootBootID == bootID {
		return
	}
	status.RebootRequired = false
	status.RebootBootID = ""
	status.State = "idle"
	status.UpdateAvailable = false
	status.Message = "The NVIDIA driver update is active."
	status.Progress = 100
	if status.UpdatedAt == "" {
		status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
}
