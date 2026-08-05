package modelstorage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedMutationLockSerializesWriters(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, MarkerName), []byte("cloudless-nfs-v1 test identity\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireMutationLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := AcquireMutationLock(ctx, root); err == nil {
		t.Fatal("second shared-cache writer acquired the lock concurrently")
	}
	release()
	releaseAgain, err := AcquireMutationLock(context.Background(), root)
	if err != nil {
		t.Fatalf("released lock was not reusable: %v", err)
	}
	releaseAgain()
}

func TestLocalStorageDoesNotCreateCrossMachineLock(t *testing.T) {
	root := t.TempDir()
	release, err := AcquireMutationLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(filepath.Join(root, ".cloudless-locks")); !os.IsNotExist(err) {
		t.Fatalf("local storage created an NFS lock directory: %v", err)
	}
}

func TestMarkerSymlinkPlacesLockOnSharedExport(t *testing.T) {
	parent := t.TempDir()
	cacheRoot := filepath.Join(parent, "cache")
	sharedRoot := filepath.Join(parent, "shared")
	if err := os.MkdirAll(cacheRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sharedRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedRoot, MarkerName), []byte("cloudless-nfs-v1 identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "shared", MarkerName), filepath.Join(cacheRoot, MarkerName)); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireMutationLock(context.Background(), cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(filepath.Join(sharedRoot, ".cloudless-locks", "mutation.lock")); err != nil {
		t.Fatalf("lock was not stored on shared export: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".cloudless-locks")); !os.IsNotExist(err) {
		t.Fatalf("shared lock leaked onto local cache root: %v", err)
	}
}
