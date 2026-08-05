package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestClientExecutesAllowListedAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan Action, 1)
	go func() {
		_ = Serve(ctx, listener, func(net.Conn) error { return nil }, func(_ context.Context, action Action, _ string) error {
			called <- action
			return nil
		})
	}()
	if err := (Client{SocketPath: path}).Do(context.Background(), ActionReboot); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-called:
		if action != ActionReboot {
			t.Fatalf("action = %q", action)
		}
	case <-time.After(time.Second):
		t.Fatal("action was not executed")
	}
}

func TestRejectsUnknownActionAndFields(t *testing.T) {
	for _, body := range []map[string]any{
		{"action": "shell.exec"},
		{"action": string(ActionPowerOff), "command": "id"},
	} {
		server, client := net.Pipe()
		done := make(chan struct{})
		go func() {
			handleConnection(context.Background(), server, func(net.Conn) error { return nil }, func(context.Context, Action, string) error {
				t.Error("executor must not be called")
				return nil
			})
			close(done)
		}()
		if err := json.NewEncoder(client).Encode(body); err != nil {
			t.Fatal(err)
		}
		var reply response
		if err := json.NewDecoder(client).Decode(&reply); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		<-done
		if reply.OK || reply.Error != "unsupported privileged action" {
			t.Fatalf("reply = %#v", reply)
		}
	}
}

func TestRejectsTrailingJSONDocument(t *testing.T) {
	server, client := net.Pipe()
	go handleConnection(context.Background(), server, func(net.Conn) error { return nil }, func(context.Context, Action, string) error {
		t.Fatal("executor must not be called")
		return nil
	})
	if _, err := client.Write([]byte(`{"action":"power.restart"} {"action":"power.shutdown"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var reply response
	if err := json.NewDecoder(client).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.OK {
		t.Fatalf("reply = %#v", reply)
	}
}

func TestAuthorizationFailureIsGeneric(t *testing.T) {
	server, client := net.Pipe()
	go handleConnection(context.Background(), server, func(net.Conn) error {
		return errors.New("sensitive detail")
	}, func(context.Context, Action, string) error {
		t.Fatal("executor must not be called")
		return nil
	})
	var reply response
	if err := json.NewDecoder(client).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error != "caller is not authorized" {
		t.Fatalf("error = %q", reply.Error)
	}
}

func TestTimezoneRequiresCanonicalTypedValue(t *testing.T) {
	tests := []struct {
		request request
		valid   bool
	}{
		{request{Action: ActionTimezoneSet, Value: "Asia/Dubai"}, true},
		{request{Action: ActionTimezoneSet, Value: "not/a-zone"}, false},
		{request{Action: ActionTimezoneSet, Value: "Asia/Dubai; reboot"}, false},
		{request{Action: ActionReboot, Value: "now"}, false},
	}
	for _, tt := range tests {
		err := validateRequest(tt.request)
		if (err == nil) != tt.valid {
			t.Fatalf("request %#v valid=%v err=%v", tt.request, tt.valid, err)
		}
	}
}

func TestUpdateChannelAcceptsOnlyNamedChannels(t *testing.T) {
	for _, value := range []string{"stable", "beta"} {
		if err := validateRequest(request{Action: ActionSystemUpdateChannel, Value: value}); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"", "testing", "stable beta", "stable\nSuites: beta", "../beta"} {
		if validateRequest(request{Action: ActionSystemUpdateChannel, Value: value}) == nil {
			t.Fatalf("%q was accepted", value)
		}
	}
}

func TestRecipeContainerRemovalAcceptsOnlyOneExactName(t *testing.T) {
	for _, value := range []string{"deepseek-v4-flash-dual-dspark-1m-vllm-1", "cloudless-engine"} {
		if err := validateRequest(request{Action: ActionRecipeContainerRemove, Value: value}); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"", "../docker", "name;reboot", "name other", "/absolute", "$(id)"} {
		if validateRequest(request{Action: ActionRecipeContainerRemove, Value: value}) == nil {
			t.Fatalf("%q was accepted", value)
		}
	}
}

func TestModelUninstallAcceptsOnlyCanonicalRepositoryIDs(t *testing.T) {
	for _, value := range []string{"Qwen/Qwen3.6-35B-A3B", "deepseek-ai/DeepSeek_V4.Flash", "org/team/model"} {
		if err := validateRequest(request{Action: ActionModelUninstall, Value: value}); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"", "model", "../model", "org/../model", "/absolute/model", "org/model;reboot", "org/model other", "org\\model"} {
		if validateRequest(request{Action: ActionModelUninstall, Value: value}) == nil {
			t.Fatalf("%q was accepted", value)
		}
	}
}

func TestModelStorageAcceptsOnlyValidatedNFSConfiguration(t *testing.T) {
	valid := `{"mode":"nfs","server":"nas.example.com","export":"/cloudless/models","version":"4.2","markerId":"0123456789abcdef0123456789abcdef"}`
	if err := validateRequest(request{Action: ActionModelStorageNFSApply, Value: valid}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		`{"mode":"nfs","server":"nas;reboot","export":"/models","version":"4.2","markerId":"0123456789abcdef0123456789abcdef"}`,
		`{"mode":"nfs","server":"nas","export":"/models/../etc","version":"4.2","markerId":"0123456789abcdef0123456789abcdef"}`,
		`{"mode":"nfs","server":"nas","export":"/models","version":"3","markerId":"0123456789abcdef0123456789abcdef"}`,
	} {
		if validateRequest(request{Action: ActionModelStorageNFSApply, Value: value}) == nil {
			t.Fatalf("unsafe NFS configuration was accepted: %s", value)
		}
	}
	if err := validateRequest(request{Action: ActionModelStorageLocal}); err != nil {
		t.Fatalf("local storage action was rejected: %v", err)
	}
	if err := validateRequest(request{Action: ActionModelStorageRefresh}); err != nil {
		t.Fatalf("model-storage service refresh was rejected: %v", err)
	}
}

func TestClusterNetworkRequiresTwoSafeInterfacesAndBoundedIndex(t *testing.T) {
	for _, value := range []string{"1|enp1s0f0|enp1s0f1", "8|enP2p1s0f1np1|enP2p1s0f1np2"} {
		if err := validateRequest(request{Action: ActionClusterNetworkApply, Value: value}); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{
		"0|enp1s0f0|enp1s0f1", "9|enp1s0f0|enp1s0f1",
		"1|lo|enp1s0f1", "1|same|same", "1|eth0:\nnetwork:|eth1",
		"1|eth0|eth1|extra",
	} {
		if validateRequest(request{Action: ActionClusterNetworkApply, Value: value}) == nil {
			t.Fatalf("%q was accepted", value)
		}
	}
}
