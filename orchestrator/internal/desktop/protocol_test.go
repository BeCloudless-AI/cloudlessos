package desktop

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExecutor struct {
	mu      sync.Mutex
	applied string
	key     string
	place   string
}

func (f *fakeExecutor) QueryDisplay(context.Context) (string, error) {
	return "DP-1 connected primary 1920x1080\n", nil
}
func (f *fakeExecutor) ApplyDisplay(_ context.Context, output string, width, height int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = output + ":1920x1080"
	if width != 1920 || height != 1080 {
		return errors.New("unexpected dimensions")
	}
	return nil
}
func (f *fakeExecutor) EmitKey(_ context.Context, action, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = action + ":" + value
	return nil
}
func (f *fakeExecutor) OpenPlace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.place = id
	return nil
}

func TestDesktopProtocolIsTypedAndRoundTrips(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "desktop.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := &fakeExecutor{}
	go func() { _ = Serve(ctx, listener, func(net.Conn) error { return nil }, executor) }()
	client := &Client{Socket: socket}
	requestCtx, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	if output, err := client.QueryDisplay(requestCtx); err != nil || !strings.Contains(output, "DP-1 connected") {
		t.Fatalf("query = %q, %v", output, err)
	}
	if err := client.ApplyDisplay(requestCtx, "DP-1", 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if err := client.EmitKey(requestCtx, "text", "@"); err != nil {
		t.Fatal(err)
	}
	if err := client.OpenPlace(requestCtx, "models"); err != nil {
		t.Fatal(err)
	}
	if executor.applied != "DP-1:1920x1080" || executor.key != "text:@" || executor.place != "models" {
		t.Fatalf("executor = %+v", executor)
	}
}

func TestDesktopRequestsFailClosed(t *testing.T) {
	rejected := []Request{
		{Action: Action("command.run")},
		{Action: ActionDisplayQuery, Output: "DP-1"},
		{Action: ActionDisplayApply, Output: "../../bad", Width: 1920, Height: 1080},
		{Action: ActionDisplayApply, Output: "DP-1", Width: 0, Height: 1080},
		{Action: ActionInputKey, KeyAction: "text", Value: "too long"},
		{Action: ActionInputKey, KeyAction: "ctrl-alt-delete"},
		{Action: ActionPlaceOpen, PlaceID: "../../etc"},
		{Action: ActionPlaceOpen, PlaceID: "models", Value: "unrelated"},
	}
	for _, request := range rejected {
		if err := request.Validate(); err == nil {
			t.Fatalf("unsafe request was accepted: %+v", request)
		}
	}
}

func TestDesktopAgentRejectsUnauthorizedPeer(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "desktop.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Serve(ctx, listener, func(net.Conn) error { return errors.New("no") }, &fakeExecutor{})
	}()
	client := &Client{Socket: socket}
	requestCtx, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	if _, err := client.QueryDisplay(requestCtx); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unauthorized query error = %v", err)
	}
}
