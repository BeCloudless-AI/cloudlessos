package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/modelstorage"
)

func TestReadConfigRejectsCommandInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, payload := range []string{
		`{"mode":"nfs","server":"nas;reboot","export":"/models","version":"4.2","markerId":"0123456789abcdef0123456789abcdef"}`,
		`{"mode":"nfs","server":"nas","export":"/models/../etc","version":"4.2","markerId":"0123456789abcdef0123456789abcdef"}`,
	} {
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readConfig(path); err == nil {
			t.Fatalf("unsafe storage config was accepted: %s", payload)
		}
	}
}

func TestMountOptionsPreserveRuntimeCachesAndRejectSoftSemantics(t *testing.T) {
	options := mountOptions(modelstorage.Config{Version: "4.2"})
	for _, expected := range []string{"rw", "hard", "nosuid", "nodev", "vers=4.2", "timeo=600"} {
		if !strings.Contains(options, expected) {
			t.Fatalf("mount options %q are missing %q", options, expected)
		}
	}
	if strings.Contains(options, "soft") || strings.Contains(options, "noexec") {
		t.Fatalf("mount options would corrupt interrupted I/O or break JIT caches: %q", options)
	}
}

func TestManagedExportIsRestrictedToPrivateFabric(t *testing.T) {
	contents := managedExportsContents()
	for _, want := range []string{modelstorage.ManagedExport, "10.100.0.0/24", "10.100.1.0/24", "no_root_squash"} {
		if !strings.Contains(contents, want) {
			t.Fatalf("managed export is missing %q: %s", want, contents)
		}
	}
	if strings.Contains(contents, "*(") || strings.Contains(contents, "0.0.0.0/0") {
		t.Fatalf("managed export is available outside the private fabric: %s", contents)
	}
}

func TestAtomicStorageProofHasExactContentsAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "model-storage.ready")
	if err := writeAtomicFile(path, []byte("exact identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "exact identity\n" {
		t.Fatalf("readiness proof = %q, %v", contents, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("readiness proof mode = %v, %v", info, err)
	}
}

func TestMountInspectionRequiresExactMountPoint(t *testing.T) {
	arguments := strings.Join(mountInspectionArgs(modelstorage.MountPoint), " ")
	if !strings.Contains(arguments, "--first-only") || !strings.Contains(arguments, "--mountpoint "+modelstorage.MountPoint) || strings.Contains(arguments, "--target") {
		t.Fatalf("mount inspection could accept the parent filesystem: %q", arguments)
	}
}

func TestWriteAndVerifyMarker(t *testing.T) {
	root := t.TempDir()
	config := modelstorage.Config{Mode: modelstorage.ModeNFS, Server: "nas", Export: "/models", Version: "4.2", MarkerID: "0123456789abcdef0123456789abcdef"}
	if err := writeAndVerifyMarkerAt(root, config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, modelstorage.MarkerName))
	if err != nil || string(data) != modelstorage.MarkerContents(config) {
		t.Fatalf("marker = %q, %v", data, err)
	}
	info, err := os.Stat(filepath.Join(root, modelstorage.MarkerName))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("marker mode = %v, %v", info, err)
	}
}

func TestMarkerLinkExposesSharedIdentityWithoutSharingRuntimeRoot(t *testing.T) {
	parent := t.TempDir()
	cacheRoot := filepath.Join(parent, "models-cache")
	sharedRoot := filepath.Join(parent, "shared-model-storage")
	if err := os.MkdirAll(cacheRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sharedRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedRoot, modelstorage.MarkerName), []byte("cloudless-nfs-v1 identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureMarkerLinkAt(cacheRoot, sharedRoot); err != nil {
		t.Fatal(err)
	}
	if !modelstorage.Shared(cacheRoot) {
		t.Fatal("cache root did not resolve the shared-storage marker")
	}
	if err := removeMarkerLinkAt(cacheRoot); err != nil {
		t.Fatal(err)
	}
	if modelstorage.Shared(cacheRoot) {
		t.Fatal("local cache remained marked shared after link removal")
	}
}
