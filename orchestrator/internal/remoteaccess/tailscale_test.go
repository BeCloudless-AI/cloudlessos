package remoteaccess

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/privileged"
)

type fakeCommands struct {
	installed bool
	status    string
	serve     string
	prefs     string
	calls     []string
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
	f := &fakeCommands{installed: true, serve: `{"Web":{"cloudless":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8765"}}}}}`, prefs: `{"RunSSH":true}`, status: `{"Version":"1.90.6","BackendState":"Running","TailscaleIPs":["100.64.0.9"],"Self":{"HostName":"cloudless","DNSName":"cloudless.example.ts.net.","Online":true},"CurrentTailnet":{"Name":"example"}}`}
	c := &Client{commands: f}
	status := c.Status(context.Background())
	if !status.Installed || !status.Connected || !status.ServeEnabled || !status.SSHEnabled || status.DNSName != "cloudless.example.ts.net" || len(status.IPs) != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestStatusHandlesMissingClient(t *testing.T) {
	status := (&Client{commands: &fakeCommands{}}).Status(context.Background())
	if status.Installed || status.Connected {
		t.Fatalf("unexpected status: %#v", status)
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
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"tailscale set --ssh=true", "tailscale serve --bg --yes http://127.0.0.1:8765", "tailscale logout"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
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
