package remoteaccess

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/privileged"
)

type fakeCommands struct {
	installed bool
	status    string
	serve     string
	prefs     string
	calls     []string
}

type approvalCommands struct{}

func (approvalCommands) LookPath(string) (string, error) { return "/usr/bin/tailscale", nil }
func (approvalCommands) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return []byte("Serve is not enabled. To enable, visit:\nhttps://login.tailscale.com/f/serve?node=example"), ctx.Err()
}

func (f *fakeCommands) LookPath(string) (string, error) {
	if !f.installed {
		return "", errors.New("missing")
	}
	return "/usr/bin/tailscale", nil
}
func (f *fakeCommands) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if strings.Contains(call, "status --json") && !strings.Contains(call, "serve") {
		return []byte(f.status), nil
	}
	if strings.Contains(call, "serve status") {
		return []byte(f.serve), nil
	}
	if strings.Contains(call, "debug prefs") {
		return []byte(f.prefs), nil
	}
	return nil, nil
}

func TestStatusReportsConnectedTailnet(t *testing.T) {
	f := &fakeCommands{installed: true, serve: `{"Web":{"cloudless":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8765"}}}},"TCP":{"8766":{"TCPForward":"127.0.0.1:8766"}}}`, prefs: `{"RunSSH":true}`, status: `{"Version":"1.90.6","BackendState":"Running","TailscaleIPs":["100.64.0.9"],"Self":{"HostName":"cloudless","DNSName":"cloudless.example.ts.net.","Online":true},"CurrentTailnet":{"Name":"example"}}`}
	c := &Client{commands: f}
	status := c.Status(context.Background())
	if !status.Installed || !status.Connected || !status.ServeEnabled || !status.SSHEnabled || !status.ServesTCP(8766) || status.ServesTCP(9999) || status.DNSName != "cloudless.example.ts.net" || len(status.IPs) != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestServeStatusAcceptsCurrentPrettyPrintedTailscaleJSON(t *testing.T) {
	data := []byte(`{
  "TCP": {"443": {"HTTPS": true}, "8766": {"TCPForward": "127.0.0.1:8766"}},
  "Web": {"spark.tail.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:8765"}}}}
}`)
	dashboard, forwards := parseServeStatus(data)
	if !dashboard || forwards[8766] != "127.0.0.1:8766" {
		t.Fatalf("dashboard = %v, forwards = %#v", dashboard, forwards)
	}
}

func TestStatusHandlesMissingClient(t *testing.T) {
	status := (&Client{commands: &fakeCommands{}}).Status(context.Background())
	if status.Installed || status.Connected {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestStatusReportsPersistentFreshInstallFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailscale-install.json")
	if err := os.WriteFile(path, []byte(`{"state":"failed","message":"Repository key verification failed."}`), 0o644); err != nil {
		t.Fatal(err)
	}
	status := (&Client{commands: &fakeCommands{}, installStatusPath: path}).Status(context.Background())
	if status.Installed || status.InstallState != "failed" || status.InstallMessage != "Repository key verification failed." {
		t.Fatalf("unexpected install status: %#v", status)
	}
}

func TestInstalledClientOverridesStaleInstallFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailscale-install.json")
	if err := os.WriteFile(path, []byte(`{"state":"failed","message":"old failure"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeCommands{installed: true, status: `{}`}
	status := (&Client{commands: f, installStatusPath: path}).Status(context.Background())
	if !status.Installed || status.InstallState != "installed" || status.InstallMessage != "" {
		t.Fatalf("stale failure survived installed client detection: %#v", status)
	}
}

func TestManagedActionsUseConstrainedCommands(t *testing.T) {
	f := &fakeCommands{installed: true}
	c := &Client{commands: f}
	if err := c.SetSSH(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetServe(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetServe(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAPIServe(context.Background(), true, 8766); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAPIServe(context.Background(), false, 8766); err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{
		"tailscale set --ssh=true",
		"tailscale serve --bg --yes --https=443 http://127.0.0.1:8765",
		"tailscale serve --yes --https=443 off",
		"tailscale serve --bg --yes --tcp=8766 tcp://127.0.0.1:8766",
		"tailscale serve --yes --tcp=8766 off",
		"tailscale logout",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, "serve reset") {
		t.Fatalf("a scoped Serve toggle reset unrelated routes: %s", joined)
	}
}

func TestSetServeReturnsTailnetApprovalWithoutHanging(t *testing.T) {
	c := &Client{commands: approvalCommands{}, serveTimeout: 10 * time.Millisecond}
	err := c.SetServe(context.Background(), true)
	if url := ServeApprovalURL(err); url != "https://login.tailscale.com/f/serve?node=example" {
		t.Fatalf("approval URL = %q, error = %v", url, err)
	}
}

func TestInstallUsesPrivilegedBroker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan privileged.Action, 1)
	go func() {
		_ = privileged.Serve(ctx, listener, func(net.Conn) error { return nil }, func(_ context.Context, action privileged.Action, _ string) error {
			called <- action
			return nil
		})
	}()
	client := &Client{broker: privileged.Client{SocketPath: path}}
	if err := client.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if action := <-called; action != privileged.ActionTailscaleInstaller {
		t.Fatalf("action = %q", action)
	}
}
