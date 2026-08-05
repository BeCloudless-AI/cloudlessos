package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteModelStorageConfigIsPrivateAndAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloudless", "model-storage.json")
	if err := writeModelStorageConfig(path, []byte(`{"mode":"nfs"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("model storage config mode = %o", info.Mode().Perm())
	}
}

func TestUseLocalModelStorageClearsStaleServiceFailure(t *testing.T) {
	originalPath, originalRun := modelStorageConfigPath, modelStorageRun
	t.Cleanup(func() {
		modelStorageConfigPath, modelStorageRun = originalPath, originalRun
	})
	modelStorageConfigPath = filepath.Join(t.TempDir(), "model-storage.json")
	var calls []string
	modelStorageRun = func(_ context.Context, program string, args ...string) error {
		calls = append(calls, program+" "+strings.Join(args, " "))
		return nil
	}
	if err := useLocalModelStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "/usr/bin/systemctl reset-failed cloudless-model-storage.service" {
		t.Fatalf("idempotent local cleanup calls = %#v", calls)
	}

	if err := os.WriteFile(modelStorageConfigPath, []byte(`{"mode":"nfs"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := useLocalModelStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(modelStorageConfigPath); !os.IsNotExist(err) {
		t.Fatalf("model storage config still exists: %v", err)
	}
	want := []string{
		"/usr/bin/systemctl disable --now cloudless-model-storage.service",
		"/usr/bin/systemctl reset-failed cloudless-model-storage.service",
	}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("configured local cleanup calls = %#v", calls)
	}
}
