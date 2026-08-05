package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/modelstorage"
)

const (
	configPath        = "/etc/cloudless/model-storage.json"
	managedExportPath = "/etc/exports.d/cloudless-model-storage.exports"
)

func main() {
	if os.Geteuid() != 0 {
		fatal(errors.New("cloudless-model-storage must run as root"))
	}
	if len(os.Args) != 2 {
		fatal(errors.New("usage: cloudless-model-storage mount|unmount"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var err error
	switch os.Args[1] {
	case "mount":
		err = mountStorage(ctx, configPath)
	case "unmount":
		err = unmountStorage(ctx, configPath)
	default:
		err = errors.New("unsupported model storage action")
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func readConfig(path string) (modelstorage.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return modelstorage.Config{}, err
	}
	var config modelstorage.Config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return modelstorage.Config{}, err
	}
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		return modelstorage.Config{}, err
	}
	if config.Mode != modelstorage.ModeNFS {
		return modelstorage.Config{}, errors.New("model storage configuration is not NFS")
	}
	return config, nil
}

func mountStorage(ctx context.Context, path string) error {
	config, err := readConfig(path)
	if err != nil {
		return err
	}
	if config.Managed {
		return serveManagedStorage(ctx, config, managedExportPath)
	}
	for _, directory := range []string{modelstorage.MountPoint, modelstorage.CacheRoot, modelstorage.CacheHub} {
		if err := os.MkdirAll(directory, 0o770); err != nil {
			return err
		}
	}
	// Upgrade the short-lived whole-cache prototype in place. Its regular
	// marker disappears with the old mount; local CUDA/JIT caches then remain
	// local under CacheRoot while only hub is rebound below.
	if legacySource, legacyType := mountedSourceAt(ctx, modelstorage.CacheRoot); legacyType == "nfs4" || legacyType == "nfs" {
		if legacySource != config.Source() {
			return fmt.Errorf("legacy model cache is mounted from unexpected source %s", legacySource)
		}
		if err := unmountExact(ctx, modelstorage.CacheRoot); err != nil {
			return err
		}
	}
	mountedHere := false
	if mounted, _ := mountedSourceAt(ctx, modelstorage.MountPoint); mounted != "" && mounted != config.Source() {
		return fmt.Errorf("shared model storage is already mounted from %s", mounted)
	} else if mounted == "" {
		options := mountOptions(config)
		if output, err := exec.CommandContext(ctx, "/usr/bin/mount", "-t", "nfs4", "-o", options, "--", config.Source(), modelstorage.MountPoint).CombinedOutput(); err != nil {
			return fmt.Errorf("mount NFS model storage: %s: %w", strings.TrimSpace(string(output)), err)
		}
		mountedHere = true
	}
	mounted, fsType := mountedSourceAt(ctx, modelstorage.MountPoint)
	if mounted != config.Source() || fsType != "nfs4" && fsType != "nfs" {
		if mountedHere {
			_, _ = exec.CommandContext(ctx, "/usr/bin/umount", "--", modelstorage.MountPoint).CombinedOutput()
		}
		return fmt.Errorf("mounted model storage is %q (%s), expected %q over NFSv4", mounted, fsType, config.Source())
	}
	if err := os.MkdirAll(filepath.Join(modelstorage.MountPoint, "hub"), 0o770); err != nil {
		if mountedHere {
			_, _ = exec.CommandContext(ctx, "/usr/bin/umount", "--", modelstorage.MountPoint).CombinedOutput()
		}
		return err
	}
	if hubSource, hubType := mountedSourceAt(ctx, modelstorage.CacheHub); hubType == "" {
		if output, err := exec.CommandContext(ctx, "/usr/bin/mount", "--bind", filepath.Join(modelstorage.MountPoint, "hub"), modelstorage.CacheHub).CombinedOutput(); err != nil {
			if mountedHere {
				_, _ = exec.CommandContext(ctx, "/usr/bin/umount", "--", modelstorage.MountPoint).CombinedOutput()
			}
			return fmt.Errorf("bind shared model weights: %s: %w", strings.TrimSpace(string(output)), err)
		}
	} else if hubType != "nfs4" && hubType != "nfs" || !strings.HasPrefix(hubSource, config.Source()) {
		return fmt.Errorf("model-weight hub is already mounted from an unexpected %s filesystem", hubType)
	}
	if err := writeAndVerifyMarker(config); err != nil {
		_ = unmountExact(ctx, modelstorage.CacheHub)
		if mountedHere {
			_ = unmountExact(ctx, modelstorage.MountPoint)
		}
		return err
	}
	if err := ensureMarkerLink(); err != nil {
		_ = unmountExact(ctx, modelstorage.CacheHub)
		if mountedHere {
			_ = unmountExact(ctx, modelstorage.MountPoint)
		}
		return err
	}
	return nil
}

func mountOptions(config modelstorage.Config) string {
	return "rw,hard,nosuid,nodev,noatime,_netdev,vers=" + config.Version + ",timeo=600,retrans=2"
}

func unmountStorage(ctx context.Context, path string) error {
	config, err := readConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && config.Managed {
		return unserveManagedStorage(ctx, config, managedExportPath)
	}
	if legacySource, legacyType := mountedSourceAt(ctx, modelstorage.CacheRoot); legacyType == "nfs4" || legacyType == "nfs" {
		if err == nil && legacySource != config.Source() {
			return fmt.Errorf("refusing to unmount unexpected legacy model storage %q", legacySource)
		}
		return unmountExact(ctx, modelstorage.CacheRoot)
	}
	mounted, fsType := mountedSourceAt(ctx, modelstorage.MountPoint)
	if mounted != "" && (err == nil && mounted != config.Source() || fsType != "nfs4" && fsType != "nfs") {
		return fmt.Errorf("refusing to unmount unexpected model storage %q (%s)", mounted, fsType)
	}
	if _, hubType := mountedSourceAt(ctx, modelstorage.CacheHub); hubType != "" {
		if hubType != "nfs4" && hubType != "nfs" {
			return fmt.Errorf("refusing to unmount unexpected model-weight hub filesystem %s", hubType)
		}
		if err := unmountExact(ctx, modelstorage.CacheHub); err != nil {
			return err
		}
	}
	if err := removeMarkerLink(); err != nil {
		return err
	}
	if mounted != "" {
		if err := unmountExact(ctx, modelstorage.MountPoint); err != nil {
			return err
		}
	}
	return nil
}

func managedExportsContents() string {
	options := "rw,sync,no_subtree_check,no_root_squash,secure"
	return fmt.Sprintf("%s 10.100.0.0/24(%s) 10.100.1.0/24(%s)\n", modelstorage.ManagedExport, options, options)
}

func serveManagedStorage(ctx context.Context, config modelstorage.Config, exportsPath string) error {
	if !config.Managed || config.Export != modelstorage.ManagedExport {
		return errors.New("invalid managed model-storage configuration")
	}
	if err := os.MkdirAll(modelstorage.CacheHub, 0o770); err != nil {
		return err
	}
	if err := writeAndVerifyMarkerAt(modelstorage.CacheHub, config); err != nil {
		return err
	}
	if err := ensureMarkerLinkAt(modelstorage.CacheRoot, modelstorage.CacheHub); err != nil {
		_ = os.Remove(filepath.Join(modelstorage.CacheHub, modelstorage.MarkerName))
		return err
	}
	if err := writeAtomicFile(exportsPath, []byte(managedExportsContents()), 0o644); err != nil {
		_ = removeMarkerLinkAt(modelstorage.CacheRoot)
		_ = os.Remove(filepath.Join(modelstorage.CacheHub, modelstorage.MarkerName))
		return fmt.Errorf("publish managed NFS export: %w", err)
	}
	if output, err := exec.CommandContext(ctx, "/usr/sbin/exportfs", "-ra").CombinedOutput(); err != nil {
		_ = os.Remove(exportsPath)
		_ = removeMarkerLinkAt(modelstorage.CacheRoot)
		_ = os.Remove(filepath.Join(modelstorage.CacheHub, modelstorage.MarkerName))
		return fmt.Errorf("activate managed NFS export: %s: %w", strings.TrimSpace(string(output)), err)
	}
	if err := writeAtomicFile(modelstorage.ReadyPath, []byte(modelstorage.MarkerContents(config)), 0o644); err != nil {
		_ = os.Remove(exportsPath)
		_, _ = exec.CommandContext(ctx, "/usr/sbin/exportfs", "-ra").CombinedOutput()
		_ = removeMarkerLinkAt(modelstorage.CacheRoot)
		_ = os.Remove(filepath.Join(modelstorage.CacheHub, modelstorage.MarkerName))
		return fmt.Errorf("publish managed NFS readiness: %w", err)
	}
	return nil
}

func unserveManagedStorage(ctx context.Context, config modelstorage.Config, exportsPath string) error {
	previous, err := os.ReadFile(exportsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(exportsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if output, err := exec.CommandContext(ctx, "/usr/sbin/exportfs", "-ra").CombinedOutput(); err != nil {
		if len(previous) != 0 {
			_ = writeAtomicFile(exportsPath, previous, 0o644)
		}
		return fmt.Errorf("disable managed NFS export: %s: %w", strings.TrimSpace(string(output)), err)
	}
	if err := removeMarkerLinkAt(modelstorage.CacheRoot); err != nil {
		return err
	}
	if err := os.Remove(modelstorage.ReadyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	marker := filepath.Join(modelstorage.CacheHub, modelstorage.MarkerName)
	if contents, readErr := os.ReadFile(marker); readErr == nil && string(contents) == modelstorage.MarkerContents(config) {
		if err := os.Remove(marker); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomicFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cloudless-model-storage-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func mountedSourceAt(ctx context.Context, mountPoint string) (string, string) {
	output, err := exec.CommandContext(ctx, "/usr/bin/findmnt", mountInspectionArgs(mountPoint)...).CombinedOutput()
	if err != nil {
		return "", ""
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) != 2 {
		return "", ""
	}
	return fields[0], fields[1]
}

func unmountExact(ctx context.Context, mountPoint string) error {
	if output, err := exec.CommandContext(ctx, "/usr/bin/umount", "--", mountPoint).CombinedOutput(); err != nil {
		return fmt.Errorf("unmount %s: %s: %w", mountPoint, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func ensureMarkerLink() error {
	return ensureMarkerLinkAt(modelstorage.CacheRoot, modelstorage.MountPoint)
}

func ensureMarkerLinkAt(cacheRoot, sharedRoot string) error {
	link := filepath.Join(cacheRoot, modelstorage.MarkerName)
	target, err := filepath.Rel(cacheRoot, filepath.Join(sharedRoot, modelstorage.MarkerName))
	if err != nil {
		return err
	}
	if current, err := os.Readlink(link); err == nil {
		if current == target {
			return nil
		}
		return errors.New("model-cache shared-storage marker points to an unexpected location")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("model-cache shared-storage marker is not a Cloudless symlink")
	}
	return os.Symlink(target, link)
}

func removeMarkerLink() error {
	return removeMarkerLinkAt(modelstorage.CacheRoot)
}

func removeMarkerLinkAt(cacheRoot string) error {
	link := filepath.Join(cacheRoot, modelstorage.MarkerName)
	if _, err := os.Lstat(link); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Readlink(link); err != nil {
		return errors.New("refusing to remove a non-symlink shared-storage marker")
	}
	return os.Remove(link)
}

func mountInspectionArgs(mountPoint string) []string {
	// --mountpoint is deliberately stricter than --target. The latter also
	// returns the parent filesystem for an ordinary directory and would make a
	// fresh local cache look like an unexpected existing mount.
	return []string{"-n", "--first-only", "-o", "SOURCE,FSTYPE", "--mountpoint", mountPoint}
}

func writeAndVerifyMarker(config modelstorage.Config) error {
	return writeAndVerifyMarkerAt(modelstorage.MountPoint, config)
}

func writeAndVerifyMarkerAt(root string, config modelstorage.Config) error {
	path := filepath.Join(root, modelstorage.MarkerName)
	temporary, err := os.CreateTemp(root, ".cloudless-shared-storage-*")
	if err != nil {
		return fmt.Errorf("create shared-storage marker: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.WriteString(modelstorage.MarkerContents(config)); err != nil {
		temporary.Close()
		return fmt.Errorf("write shared-storage marker: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("publish shared-storage marker: %w", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != modelstorage.MarkerContents(config) {
		return errors.New("shared-storage marker could not be verified")
	}
	return nil
}
