package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/privileged"
)

func TestRemoveModelCacheUsesTypedPrivilegedRemoval(t *testing.T) {
	root := filepath.Join(t.TempDir(), "models-cache")
	t.Setenv("CLOUDLESS_MODEL_CACHE", root)
	if err := os.MkdirAll(filepath.Join(root, "hub"), 0o750); err != nil {
		t.Fatal(err)
	}
	called := false
	server := &Server{privilegedValue: func(_ context.Context, action privileged.Action, value string) error {
		called = true
		if action != privileged.ActionModelUninstall || value != "Qwen/Qwen3.6-35B-A3B" {
			t.Fatalf("action=%q value=%q", action, value)
		}
		return nil
	}}
	if err := server.removeModelCache(context.Background(), "Qwen/Qwen3.6-35B-A3B"); err != nil {
		t.Fatal(err)
	}
	if !called || modelcache.Root() != root {
		t.Fatalf("called=%v root=%q", called, modelcache.Root())
	}
}
