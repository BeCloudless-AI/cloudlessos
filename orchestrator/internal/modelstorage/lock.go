package modelstorage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Shared reports whether the cache itself carries a Cloudless shared-storage
// marker. This intentionally works on peer Sparks whose local daemon state was
// not used to configure the coordinator-owned cluster.
func Shared(root string) bool {
	_, ok := SharedRoot(root)
	return ok
}

// SharedRoot returns the NFS root carrying the marker. New installations use
// a marker symlink from the local cache root, while the regular-file fallback
// keeps upgrades from the initial whole-cache prototype safe.
func SharedRoot(root string) (string, bool) {
	// External NFS uses the cache-root marker link. A coordinator-hosted export
	// keeps the same marker directly in the exported local hub.
	for _, marker := range []string{filepath.Join(root, MarkerName), filepath.Join(root, "hub", MarkerName)} {
		data, err := os.ReadFile(marker)
		if err != nil || !strings.HasPrefix(string(data), "cloudless-nfs-v1 ") {
			continue
		}
		resolved, err := filepath.EvalSymlinks(marker)
		if err == nil {
			return filepath.Dir(resolved), true
		}
	}
	return "", false
}

// AcquireMutationLock serializes cache mutations across every Spark mounting
// the same NFS export. Local NVMe keeps the existing process-local behavior.
func AcquireMutationLock(ctx context.Context, root string) (func(), error) {
	sharedRoot, shared := SharedRoot(root)
	if !shared {
		return func() {}, nil
	}
	directory := filepath.Join(sharedRoot, ".cloudless-locks")
	if err := os.MkdirAll(directory, 0o770); err != nil {
		return nil, fmt.Errorf("create shared model-cache lock directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, "mutation.lock"), os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return nil, fmt.Errorf("open shared model-cache lock: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock shared model cache: %w", err)
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, fmt.Errorf("wait for another Spark to finish changing the shared model cache: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
