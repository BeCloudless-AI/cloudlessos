//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallManagedModelAtRemovesOnlyExactManagedPaths(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	models := filepath.Join(root, "Models")
	modelID := "Qwen/Qwen3.6-35B-A3B"
	name := "Qwen--Qwen3.6-35B-A3B"
	for _, path := range []string{
		filepath.Join(cache, "hub", "models--"+name, "blobs"),
		filepath.Join(models, name, "nested"),
		filepath.Join(models, name+"-Cloudless"),
		filepath.Join(models, ".materializing-"+name),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, managed := range []string{filepath.Join(models, name), filepath.Join(models, name+"-Cloudless")} {
		if err := os.WriteFile(filepath.Join(managed, ".cloudless-revision"), []byte("0123456789abcdef\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unmanaged := filepath.Join(models, "unmanaged")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := uninstallManagedModelAt(modelID, cache, models); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{filepath.Join(cache, "hub", "models--"+name), filepath.Join(models, name), filepath.Join(models, name+"-Cloudless"), filepath.Join(models, ".materializing-"+name)} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("managed path remains: %s", removed)
		}
	}
	if _, err := os.Stat(unmanaged); err != nil {
		t.Fatalf("unmanaged folder was changed: %v", err)
	}
}

func TestUninstallManagedModelAtRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := uninstallManagedModelAt("../outside", filepath.Join(root, "cache"), filepath.Join(root, "Models")); err == nil {
		t.Fatal("traversal model id was accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside path was changed: %v", err)
	}
}
