// Package osupdate exposes the on-disk contract shared by cloudlessd and the
// privileged CloudlessOS update worker.
package osupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	State            string    `json:"state"`
	Channel          string    `json:"channel"`
	Configured       bool      `json:"configured"`
	CurrentVersion   string    `json:"currentVersion,omitempty"`
	AvailableVersion string    `json:"availableVersion,omitempty"`
	Message          string    `json:"message,omitempty"`
	Progress         int       `json:"progress"`
	Error            string    `json:"error,omitempty"`
	CheckedAt        string    `json:"checkedAt,omitempty"`
	UpdatedAt        string    `json:"updatedAt,omitempty"`
	RebootRequired   bool      `json:"rebootRequired"`
	Packages         []Package `json:"packages,omitempty"`
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
