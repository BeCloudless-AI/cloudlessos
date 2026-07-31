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

	"github.com/cloudless/orchestrator/internal/privileged"
)

func main() {
	socketPath := flag.String("socket", privileged.DefaultSocketPath, "Unix socket path")
	controlGroup := flag.String("group", "cloudless-control", "authorized caller group")
	flag.Parse()
	if os.Geteuid() != 0 {
		log.Fatal("cloudless-privileged must run as root")
	}
	if err := prepareSocketDirectory(*socketPath); err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(*socketPath)
	}()
	if err := protectSocket(*socketPath, *controlGroup); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log.Printf("privileged broker listening on %s", *socketPath)
	if err := privileged.Serve(ctx, listener, privileged.AuthorizePeer(*controlGroup), execute); err != nil {
		log.Fatal(err)
	}
}

func prepareSocketDirectory(socketPath string) error {
	if !filepath.IsAbs(socketPath) {
		return errors.New("socket path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", socketPath)
	}
	return os.Remove(socketPath)
}

func protectSocket(socketPath, groupName string) error {
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup control group: %w", err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("parse control group id: %w", err)
	}
	if err := os.Chown(socketPath, 0, gid); err != nil {
		return fmt.Errorf("set socket ownership: %w", err)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		return fmt.Errorf("set socket mode: %w", err)
	}
	return nil
}

func execute(ctx context.Context, action privileged.Action, value string) error {
	var args []string
	switch action {
	case privileged.ActionPowerOff:
		args = []string{"poweroff", "--no-wall"}
	case privileged.ActionReboot:
		args = []string{"reboot", "--no-wall"}
	case privileged.ActionSystemUpdateCheck:
		args = []string{"start", "--no-block", "cloudless-update-check.service"}
	case privileged.ActionSystemUpdateApply:
		args = []string{"start", "--no-block", "cloudless-update-apply.service"}
	case privileged.ActionNVIDIAUpdateCheck:
		args = []string{"start", "--no-block", "cloudless-nvidia-check.service"}
	case privileged.ActionNVIDIAUpdateApply:
		args = []string{"start", "--no-block", "cloudless-nvidia-apply.service"}
	case privileged.ActionTailscaleInstaller:
		args = []string{"start", "--no-block", "cloudless-tailscale-install.service"}
	case privileged.ActionTimezoneSet:
		args = []string{"set-timezone", value}
		return exec.CommandContext(ctx, "/usr/bin/timedatectl", args...).Run()
	case privileged.ActionClusterNetworkApply:
		return applyClusterNetwork(ctx, value)
	case privileged.ActionClusterNetworkRemove:
		return removeClusterNetwork(ctx)
	default:
		return fmt.Errorf("unsupported action %q", action)
	}
	return exec.CommandContext(ctx, "/usr/bin/systemctl", args...).Run()
}
