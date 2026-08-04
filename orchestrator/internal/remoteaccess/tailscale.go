// Package remoteaccess wraps the host Tailscale client behind a small,
// testable boundary. Cloudless never handles tailnet credentials: interactive
// authentication remains owned by Tailscale and opens in the system browser.
package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/privileged"
)

type Status struct {
	Installed    bool           `json:"installed"`
	Connected    bool           `json:"connected"`
	BackendState string         `json:"backendState,omitempty"`
	Version      string         `json:"version,omitempty"`
	DeviceName   string         `json:"deviceName,omitempty"`
	DNSName      string         `json:"dnsName,omitempty"`
	Tailnet      string         `json:"tailnet,omitempty"`
	IPs          []string       `json:"ips,omitempty"`
	ServeEnabled bool           `json:"serveEnabled"`
	SSHEnabled   bool           `json:"sshEnabled"`
	WebURL       string         `json:"webURL,omitempty"`
	Error        string         `json:"error,omitempty"`
	TCPForwards  map[int]string `json:"-"`
}

// ServesTCP reports whether Tailscale Serve owns a private tailnet listener on
// port and forwards it to the matching loopback port. Other user-managed Serve
// routes never count as Cloudless API exposure.
func (s Status) ServesTCP(port int) bool {
	target := strings.TrimPrefix(s.TCPForwards[port], "tcp://")
	return target == fmt.Sprintf("127.0.0.1:%d", port) || target == fmt.Sprintf("localhost:%d", port)
}

type Service interface {
	Status(context.Context) Status
	Connect(context.Context) (string, error)
	Logout(context.Context) error
	SetSSH(context.Context, bool) error
	SetServe(context.Context, bool) error
	SetAPIServe(context.Context, bool, int) error
	Install(context.Context) error
}

// ServeApprovalError means the tailnet administrator must enable Tailscale
// Serve before the requested route can be installed. URL is safe to return to
// the local UI because it is an official, short-lived Tailscale approval URL.
type ServeApprovalError struct {
	URL string
}

func (e *ServeApprovalError) Error() string {
	return "Tailscale Serve must be approved for this tailnet"
}

func ServeApprovalURL(err error) string {
	var approval *ServeApprovalError
	if errors.As(err, &approval) {
		return approval.URL
	}
	return ""
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

type Client struct {
	commands     commandRunner
	broker       privileged.Client
	serveTimeout time.Duration
}

func New() *Client {
	return &Client{
		commands: execCommands{},
		broker:   privileged.Client{SocketPath: os.Getenv("CLOUDLESS_PRIVILEGED_SOCKET")},
	}
}

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
	if serveErr == nil {
		result.ServeEnabled, result.TCPForwards = parseServeStatus(serve)
	}
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
	timeout := c.serveTimeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	serveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var err error
	var output []byte
	if enabled {
		output, err = c.commands.Run(serveCtx, "tailscale", "serve", "--bg", "--yes", "--https=443", "http://127.0.0.1:8765")
	} else {
		output, err = c.commands.Run(serveCtx, "tailscale", "serve", "--yes", "--https=443", "off")
	}
	if url := loginURLPattern.FindString(string(output)); url != "" {
		return &ServeApprovalError{URL: url}
	}
	return err
}

func (c *Client) SetAPIServe(ctx context.Context, enabled bool, port int) error {
	if port < 1024 || port > 65535 {
		return errors.New("invalid Cloudless API port")
	}
	flag := fmt.Sprintf("--tcp=%d", port)
	if enabled {
		_, err := c.commands.Run(ctx, "tailscale", "serve", "--bg", "--yes", flag, fmt.Sprintf("tcp://127.0.0.1:%d", port))
		return err
	}
	_, err := c.commands.Run(ctx, "tailscale", "serve", "--yes", flag, "off")
	return err
}

func parseServeStatus(data []byte) (bool, map[int]string) {
	forwards := map[int]string{}
	var config struct {
		TCP map[string]struct {
			TCPForward string `json:"TCPForward"`
		} `json:"TCP"`
		Web map[string]struct {
			Handlers map[string]struct {
				Proxy string `json:"Proxy"`
			} `json:"Handlers"`
		} `json:"Web"`
	}
	dashboard := false
	if json.Unmarshal(data, &config) == nil {
		for rawPort, handler := range config.TCP {
			if port, err := strconv.Atoi(rawPort); err == nil && handler.TCPForward != "" {
				forwards[port] = handler.TCPForward
			}
		}
		for _, host := range config.Web {
			for _, handler := range host.Handlers {
				if strings.TrimSuffix(handler.Proxy, "/") == "http://127.0.0.1:8765" {
					dashboard = true
				}
			}
		}
	}
	return dashboard, forwards
}

func (c *Client) Install(ctx context.Context) error {
	return c.broker.Do(ctx, privileged.ActionTailscaleInstaller)
}

func cleanError(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.TrimPrefix(message, ": ")
	if message == "" {
		return "Tailscale command failed"
	}
	return message
}
