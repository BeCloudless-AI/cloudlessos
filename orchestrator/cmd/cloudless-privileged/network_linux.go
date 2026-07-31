//go:build linux

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloudless/orchestrator/internal/privileged"
)

const clusterNetplanPath = "/etc/netplan/99-cloudless-spark-cluster.yaml"

func applyClusterNetwork(ctx context.Context, value string) error {
	content, err := clusterNetworkContent(value)
	if err != nil {
		return err
	}
	if err := secureReplace(clusterNetplanPath, []byte(content), 0o600); err != nil {
		return err
	}
	if err := runNetplan(ctx); err != nil {
		_ = removeManagedFile(clusterNetplanPath)
		_ = runNetplan(ctx)
		return err
	}
	return nil
}

func clusterNetworkContent(value string) (string, error) {
	index, first, second, err := privileged.ParseClusterNetworkValue(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"network:\n  version: 2\n  ethernets:\n    %s:\n      addresses: [10.100.0.%d/24]\n      dhcp4: false\n      optional: true\n    %s:\n      addresses: [10.100.1.%d/24]\n      dhcp4: false\n      optional: true\n",
		first, index, second, index,
	), nil
}

func removeClusterNetwork(ctx context.Context) error {
	if err := removeManagedFile(clusterNetplanPath); err != nil {
		return err
	}
	if err := runNetplan(ctx); err != nil {
		return err
	}
	return removeClusterAddresses(ctx)
}

func runNetplan(ctx context.Context) error {
	for _, action := range []string{"generate", "apply"} {
		output, err := exec.CommandContext(ctx, "/usr/sbin/netplan", action).CombinedOutput()
		if err != nil {
			return fmt.Errorf("netplan %s failed: %w: %s", action, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func secureReplace(path string, content []byte, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to replace non-regular managed file %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cloudless-netplan-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func removeManagedFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove non-regular managed file %s", path)
	}
	return os.Remove(path)
}

func removeClusterAddresses(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, "/usr/sbin/ip", "-o", "-4", "addr", "show").Output()
	if err != nil {
		return err
	}
	var result error
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[2] != "inet" {
			continue
		}
		iface := strings.TrimSuffix(fields[1], ":")
		cidr := fields[3]
		if _, _, _, parseErr := privileged.ParseClusterNetworkValue("1|" + iface + "|placeholder"); parseErr != nil {
			continue
		}
		if !isReservedClusterAddress(cidr) {
			continue
		}
		if commandOutput, commandErr := exec.CommandContext(ctx, "/usr/sbin/ip", "address", "del", cidr, "dev", iface).CombinedOutput(); commandErr != nil {
			result = errors.Join(result, fmt.Errorf("remove %s from %s: %w: %s", cidr, iface, commandErr, strings.TrimSpace(string(commandOutput))))
		}
	}
	return errors.Join(result, scanner.Err())
}

func isReservedClusterAddress(cidr string) bool {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return false
	}
	ones, bits := network.Mask.Size()
	v4 := ip.To4()
	return bits == 32 && ones == 24 && v4[0] == 10 && v4[1] == 100 && (v4[2] == 0 || v4[2] == 1)
}
