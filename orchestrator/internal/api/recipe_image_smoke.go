package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func runRecipeImageSmokeTest(ctx context.Context, runtime engine.Engine, image string, smoke *localrecipes.ContainerSmokeTest) error {
	if smoke == nil {
		return nil
	}
	program := strings.TrimSpace(smoke.Program)
	if program == "" {
		return fmt.Errorf("container smoke test has no program")
	}
	timeout := time.Duration(smoke.TimeoutSeconds) * time.Second
	if timeout < time.Second || timeout > 5*time.Minute {
		return fmt.Errorf("container smoke test timeout is outside the 1-300 second boundary")
	}
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, err := recipeImageSmokeContainerName()
	if err != nil {
		return fmt.Errorf("create container smoke test identity: %w", err)
	}
	_, err = runtime.RunTransient(testCtx, engine.RunSpec{
		Name:         name,
		Image:        image,
		Network:      "none",
		EntryPoint:   program,
		Args:         append([]string(nil), smoke.Args...),
		Env:          map[string]string{"HOME": "/tmp", "PYTHONDONTWRITEBYTECODE": "1"},
		ReadOnly:     true,
		CapDrop:      []string{"ALL"},
		SecurityOpts: []string{"no-new-privileges:true"},
		Tmpfs:        []string{"/tmp:rw,nosuid,nodev,noexec,size=64m"},
		PidsLimit:    256,
		Memory:       "4g",
		MemorySwap:   "4g",
	})
	if err != nil {
		// Killing an attached Docker client does not guarantee that the daemon
		// stopped the container. Clean up by the name we supplied, using a fresh
		// context because testCtx may already have expired.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = runtime.Stop(cleanupCtx, name)
		_ = runtime.Remove(cleanupCtx, name)
		cleanupCancel()
		if testCtx.Err() != nil {
			return fmt.Errorf("container smoke test exceeded %d seconds: %w", smoke.TimeoutSeconds, testCtx.Err())
		}
		return fmt.Errorf("container smoke test failed: %w", err)
	}
	return nil
}

func recipeImageSmokeContainerName() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return "cloudless-recipe-smoke-" + hex.EncodeToString(suffix[:]), nil
}
