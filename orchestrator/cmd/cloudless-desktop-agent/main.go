//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/cloudless/orchestrator/internal/desktop"
	"github.com/cloudless/orchestrator/internal/displaylayout"
	"github.com/cloudless/orchestrator/internal/privileged"
)

type sessionExecutor struct{}

func (sessionExecutor) QueryDisplay(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "/usr/bin/xrandr", "--query").Output()
	if err != nil {
		return "", fmt.Errorf("query display: %w", err)
	}
	return string(output), nil
}

func (sessionExecutor) ApplyDisplay(ctx context.Context, layout, output string, width, height int) error {
	before, err := exec.CommandContext(ctx, "/usr/bin/xrandr", "--query").Output()
	if err != nil {
		return fmt.Errorf("query display before layout: %w", err)
	}
	snapshot, err := displaylayout.Parse(string(before))
	if err != nil {
		return fmt.Errorf("parse display layout: %w", err)
	}
	plan, err := displaylayout.BuildPlan(snapshot, displaylayout.Preference{Layout: layout, Output: output, Width: width, Height: height})
	if err != nil {
		return fmt.Errorf("plan display layout: %w", err)
	}
	command := exec.CommandContext(ctx, "/usr/bin/xrandr", plan.Args...)
	if payload, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("apply display layout: %w: %s", err, payload)
	}
	after, err := exec.CommandContext(ctx, "/usr/bin/xrandr", "--query").Output()
	if err != nil {
		return fmt.Errorf("verify display layout: %w", err)
	}
	verified, err := displaylayout.Parse(string(after))
	if err != nil || !displaylayout.Matches(verified, plan.Effective) {
		return errors.New("display server did not apply the requested safe layout")
	}
	return nil
}

func (sessionExecutor) EmitKey(ctx context.Context, action, value string) error {
	args := []string{"key", "--clearmodifiers"}
	if action == "text" {
		args = []string{"type", "--clearmodifiers", "--delay", "0", value}
	} else {
		key := map[string]string{
			"backspace": "BackSpace", "left": "Left", "right": "Right",
			"enter": "Return", "shift-enter": "shift+Return",
		}[action]
		args = append(args, key)
	}
	if err := exec.CommandContext(ctx, "/usr/bin/xdotool", args...).Run(); err != nil {
		return fmt.Errorf("emit virtual key: %w", err)
	}
	return nil
}

func (sessionExecutor) OpenPlace(ctx context.Context, id string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve desktop home: %w", err)
	}
	path, err := desktop.PlacePath(home, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create desktop place: %w", err)
	}
	fileManager, err := exec.LookPath("pcmanfm")
	if err != nil {
		return errors.New("file manager unavailable (pcmanfm not found)")
	}
	if err := exec.CommandContext(ctx, fileManager, "--no-desktop", "--new-win", path).Start(); err != nil {
		return fmt.Errorf("open desktop place: %w", err)
	}
	return nil
}

func main() {
	socketPath := flag.String("socket", desktop.DefaultSocket, "desktop agent Unix socket")
	authorizedUser := flag.String("authorized-user", "cloudlessd", "sole authorized client user")
	socketGroup := flag.String("socket-group", "cloudless", "socket access group")
	flag.Parse()
	if os.Geteuid() == 0 {
		log.Fatal("cloudless-desktop-agent must run inside the unprivileged desktop session")
	}
	if err := prepareSocket(*socketPath); err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(*socketPath)
	}()
	if err := protectSocket(*socketPath, *socketGroup); err != nil {
		log.Fatal(err)
	}
	account, err := user.Lookup(*authorizedUser)
	if err != nil {
		log.Fatal(err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	authorize := desktop.Authorizer(privileged.AuthorizePeerUID(uint32(uid)))
	if err := desktop.Serve(ctx, listener, authorize, sessionExecutor{}); err != nil {
		log.Fatal(err)
	}
}

func prepareSocket(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("desktop socket path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o770); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to replace non-socket desktop path")
	}
	return os.Remove(path)
}

func protectSocket(path, groupName string) error {
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	if err := os.Chown(path, os.Geteuid(), gid); err != nil {
		return err
	}
	return os.Chmod(path, 0o660)
}
