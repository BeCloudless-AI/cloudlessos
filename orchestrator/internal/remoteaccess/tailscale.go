// Package remoteaccess wraps the host Tailscale client behind a small,
// testable boundary. Cloudless never handles tailnet credentials: interactive
// authentication remains owned by Tailscale and opens in the system browser.
package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Status struct {
	Installed    bool     `json:"installed"`
	Connected    bool     `json:"connected"`
	BackendState string   `json:"backendState,omitempty"`
	Version      string   `json:"version,omitempty"`
	DeviceName   string   `json:"deviceName,omitempty"`
	DNSName      string   `json:"dnsName,omitempty"`
	Tailnet      string   `json:"tailnet,omitempty"`
	IPs          []string `json:"ips,omitempty"`
	ServeEnabled bool     `json:"serveEnabled"`
	SSHEnabled   bool     `json:"sshEnabled"`
	WebURL       string   `json:"webURL,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type Service interface {
	Status(context.Context) Status
	Connect(context.Context) (string, error)
	Logout(context.Context) error
	SetSSH(context.Context, bool) error
	SetServe(context.Context, bool) error
	Install(context.Context) error
}

type commandRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) ([]byte, error)
}

type execCommands struct{}

func (execCommands) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (execCommands) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return output, nil
}

type Client struct{ commands commandRunner }

func New() *Client { return &Client{commands: execCommands{}} }

type cliStatus struct {
	Version      string   `json:"Version"`
	BackendState string   `json:"BackendState"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Self         struct {
		HostName string `json:"HostName"`
		DNSName  string `json:"DNSName"`
		Online   bool   `json:"Online"`
	} `json:"Self"`
	CurrentTailnet struct {
		Name string `json:"Name"`
	} `json:"CurrentTailnet"`
}

func (c *Client) Status(ctx context.Context) Status {
	if _, err := c.commands.LookPath("tailscale"); err != nil {
		return Status{Installed: false}
	}
	result := Status{Installed: true, WebURL: "http://100.100.100.100"}
	out, err := c.commands.Run(ctx, "tailscale", "status", "--json")
	if err != nil {
		result.Error = cleanError(err)
		return result
	}
	var raw cliStatus
	if err := json.Unmarshal(out, &raw); err != nil {
		result.Error = "Tailscale returned an unreadable status"
		return result
	}
	result.BackendState = raw.BackendState
	result.Version = raw.Version
	result.DeviceName = raw.Self.HostName
	result.DNSName = strings.TrimSuffix(raw.Self.DNSName, ".")
	result.Tailnet = raw.CurrentTailnet.Name
	result.IPs = raw.TailscaleIPs
	result.Connected = raw.BackendState == "Running" && raw.Self.Online
	serve, serveErr := c.commands.Run(ctx, "tailscale", "serve", "status", "--json")
	result.ServeEnabled = serveErr == nil && len(strings.TrimSpace(string(serve))) > 2 && strings.TrimSpace(string(serve)) != "null"
	if prefs, prefsErr := c.commands.Run(ctx, "tailscale", "debug", "prefs"); prefsErr == nil {
		var rawPrefs struct {
			RunSSH bool `json:"RunSSH"`
		}
		if json.Unmarshal(prefs, &rawPrefs) == nil {
			result.SSHEnabled = rawPrefs.RunSSH
		}
	}
	return result
}

var loginURLPattern = regexp.MustCompile(`https://login\.tailscale\.com/[A-Za-z0-9/_?=&.%-]+`)

func (c *Client) Connect(ctx context.Context) (string, error) {
	if _, err := c.commands.LookPath("tailscale"); err != nil {
		return "", errors.New("Tailscale is not installed")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out, err := c.commands.Run(connectCtx, "tailscale", "up", "--timeout=10s")
	if match := loginURLPattern.FindString(string(out)); match != "" {
		return match, nil
	}
	if err != nil {
		if status := c.Status(ctx); status.Connected {
			return "", nil
		}
		return "", errors.New(cleanError(err))
	}
	return "", nil
}

func (c *Client) Logout(ctx context.Context) error {
	_, err := c.commands.Run(ctx, "tailscale", "logout")
	return err
}

func (c *Client) SetSSH(ctx context.Context, enabled bool) error {
	_, err := c.commands.Run(ctx, "tailscale", "set", "--ssh="+fmt.Sprint(enabled))
	return err
}

func (c *Client) SetServe(ctx context.Context, enabled bool) error {
	var err error
	if enabled {
		_, err = c.commands.Run(ctx, "tailscale", "serve", "--bg", "--yes", "http://127.0.0.1:8765")
	} else {
		_, err = c.commands.Run(ctx, "tailscale", "serve", "reset")
	}
	return err
}

func (c *Client) Install(ctx context.Context) error {
	_, err := c.commands.Run(ctx, "systemctl", "start", "--no-block", "cloudless-tailscale-install.service")
	return err
}

func cleanError(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.TrimPrefix(message, ": ")
	if message == "" {
		return "Tailscale command failed"
	}
	return message
}
