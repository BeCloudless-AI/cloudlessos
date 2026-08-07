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
	"strings"
	"syscall"

	"github.com/cloudless/orchestrator/internal/osupdate"
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
	case privileged.ActionSystemUpdateChannel:
		previous := osupdate.Channel()
		if err := osupdate.SetChannel(value); err != nil {
			return err
		}
		if err := exec.CommandContext(ctx, "/usr/bin/systemctl", "start", "--no-block", "cloudless-update-check.service").Run(); err != nil {
			_ = osupdate.SetChannel(previous)
			return err
		}
		return nil
	case privileged.ActionNVIDIAUpdateCheck:
		args = []string{"start", "--no-block", "cloudless-nvidia-check.service"}
	case privileged.ActionNVIDIAUpdateApply:
		args = []string{"start", "--no-block", "cloudless-nvidia-apply.service"}
	case privileged.ActionTailscaleInstaller:
		// A previous network or package failure may have exhausted systemd's
		// start limit. An explicit user retry must always get a fresh attempt.
		if err := exec.CommandContext(ctx, "/usr/bin/systemctl", "reset-failed", "cloudless-tailscale-install.service").Run(); err != nil {
			return err
		}
		args = []string{"start", "--no-block", "cloudless-tailscale-install.service"}
	case privileged.ActionTimezoneSet:
		args = []string{"set-timezone", value}
		return exec.CommandContext(ctx, "/usr/bin/timedatectl", args...).Run()
	case privileged.ActionClusterNetworkApply:
		return applyClusterNetwork(ctx, value)
	case privileged.ActionClusterNetworkRemove:
		return removeClusterNetwork(ctx)
	case privileged.ActionRecipeRuntimeRepair:
		return repairRecipeRuntimeOwnership()
	case privileged.ActionRecipeContainerRemove:
		return exec.CommandContext(ctx, "/usr/bin/docker", "rm", "-f", "--", value).Run()
	case privileged.ActionModelUninstall:
		return uninstallManagedModel(value)
	case privileged.ActionModelViewsReconcile:
		if err := reconcileManagedModelViews(); err != nil {
			log.Printf("model view reconciliation failed: %v", err)
			return err
		}
		return nil
	case privileged.ActionModelStorageNFSApply:
		return applyNFSModelStorage(ctx, value)
	case privileged.ActionModelStorageLocal:
		return useLocalModelStorage(ctx)
	case privileged.ActionModelStorageRefresh:
		// The long-running services use hardened mount namespaces. Schedule a
		// delayed restart so their next namespace includes the newly mounted or
		// restored cache, while still allowing this broker request to reply.
		return exec.CommandContext(ctx, "/usr/bin/systemd-run", "--unit=cloudless-model-storage-refresh", "--collect", "--on-active=2s", "/usr/bin/systemctl", "restart", "cloudless-engine.service", "cloudless-privileged.service", "cloudlessd.service").Run()
	default:
		return fmt.Errorf("unsupported action %q", action)
	}
	return exec.CommandContext(ctx, "/usr/bin/systemctl", args...).Run()
}

func uninstallManagedModel(modelID string) error {
	return uninstallManagedModelAt(modelID, "/var/lib/cloudless/models-cache", "/home/cloudless/Cloudless/Models")
}

func uninstallManagedModelAt(modelID, cacheRoot, modelsPath string) error {
	if !privileged.ValidModelID(modelID) {
		return errors.New("invalid model id")
	}
	name := strings.ReplaceAll(modelID, "/", "--")
	cachePath := filepath.Join(cacheRoot, "hub", "models--"+name)
	var failures []error
	for _, candidate := range []string{
		filepath.Join(modelsPath, name),
		filepath.Join(modelsPath, name+"-Cloudless"),
	} {
		marker, err := os.Lstat(filepath.Join(candidate, ".cloudless-revision"))
		if err == nil && marker.Mode().IsRegular() {
			if err := os.RemoveAll(candidate); err != nil {
				failures = append(failures, err)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(modelsPath, ".materializing-"+name)); err != nil {
		failures = append(failures, err)
	}
	if err := os.RemoveAll(cachePath); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func repairRecipeRuntimeOwnership() error {
	const root = "/var/lib/cloudless/recipes-runtime"
	serviceUser, err := user.Lookup("cloudlessd")
	if err != nil {
		return fmt.Errorf("lookup cloudless service account: %w", err)
	}
	controlGroup, err := user.LookupGroup("cloudless-control")
	if err != nil {
		return fmt.Errorf("lookup cloudless control group: %w", err)
	}
	uid, err := strconv.Atoi(serviceUser.Uid)
	if err != nil {
		return fmt.Errorf("parse cloudless service uid: %w", err)
	}
	gid, err := strconv.Atoi(controlGroup.Gid)
	if err != nil {
		return fmt.Errorf("parse cloudless control gid: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create recipe runtime root: %w", err)
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return os.Lchown(path, uid, gid)
		}
		return os.Chown(path, uid, gid)
	})
}
