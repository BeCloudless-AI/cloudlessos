//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClusterNetworkContentIsGeneratedFromTypedFields(t *testing.T) {
	content, err := clusterNetworkContent("2|enp1s0f0|enp1s0f1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"enp1s0f0:\n      addresses: [10.100.0.2/24]",
		"enp1s0f1:\n      addresses: [10.100.1.2/24]",
		"dhcp4: false",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
}

func TestSecureReplaceRejectsNonRegularDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.Symlink("/etc/passwd", path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := secureReplace(path, []byte("safe"), 0o600); err == nil {
		t.Fatal("symlink destination was accepted")
	}
}

func TestSecureReplaceWritesPrivateRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := secureReplace(path, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%#o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "safe" {
		t.Fatalf("data=%q", data)
	}
}

func TestReservedClusterAddressScope(t *testing.T) {
	for _, address := range []string{"10.100.0.1/24", "10.100.1.8/24"} {
		if !isReservedClusterAddress(address) {
			t.Fatalf("%q should be reserved", address)
		}
	}
	for _, address := range []string{"10.100.2.1/24", "10.100.0.1/16", "10.100.0.999/24", "192.168.1.1/24"} {
		if isReservedClusterAddress(address) {
			t.Fatalf("%q should not be reserved", address)
		}
	}
}
