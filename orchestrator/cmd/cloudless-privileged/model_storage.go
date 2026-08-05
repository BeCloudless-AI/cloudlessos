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

	"github.com/cloudless/orchestrator/internal/privileged"
)

const modelStorageService = "cloudless-model-storage.service"

var modelStorageConfigPath = "/etc/cloudless/model-storage.json"
var modelStorageRun = func(ctx context.Context, program string, args ...string) error {
	command := exec.CommandContext(ctx, program, args...)
	if output, err := command.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%s: %w", message, err)
		}
		return err
	}
	return nil
}

func applyNFSModelStorage(ctx context.Context, value string) error {
	config, err := privileged.ParseModelStorageValue(value)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(config)
	if err != nil {
		return err
	}
	previous, previousErr := os.ReadFile(modelStorageConfigPath)
	hadPrevious := previousErr == nil
	if previousErr != nil && !errors.Is(previousErr, os.ErrNotExist) {
		return fmt.Errorf("read previous model storage configuration: %w", previousErr)
	}
	if hadPrevious {
		if err := modelStorageRun(ctx, "/usr/bin/systemctl", "disable", "--now", modelStorageService); err != nil {
			return fmt.Errorf("stop previous shared model storage: %w", err)
		}
	}
	if err := writeModelStorageConfig(modelStorageConfigPath, payload); err != nil {
		return err
	}
	startErr := modelStorageRun(ctx, "/usr/bin/systemctl", "enable", "--now", modelStorageService)
	if startErr == nil {
		return nil
	}
	_ = modelStorageRun(ctx, "/usr/bin/systemctl", "disable", "--now", modelStorageService)
	if hadPrevious {
		_ = writeModelStorageConfig(modelStorageConfigPath, previous)
		_ = modelStorageRun(ctx, "/usr/bin/systemctl", "enable", "--now", modelStorageService)
	} else {
		_ = os.Remove(modelStorageConfigPath)
	}
	return fmt.Errorf("activate shared model storage: %w", startErr)
}

func useLocalModelStorage(ctx context.Context) error {
	if _, err := os.Stat(modelStorageConfigPath); errors.Is(err, os.ErrNotExist) {
		if err := modelStorageRun(ctx, "/usr/bin/systemctl", "reset-failed", modelStorageService); err != nil {
			return fmt.Errorf("clear stale shared model storage failure: %w", err)
		}
		return nil
	} else if err != nil {
		return err
	}
	if err := modelStorageRun(ctx, "/usr/bin/systemctl", "disable", "--now", modelStorageService); err != nil {
		return fmt.Errorf("unmount shared model storage: %w", err)
	}
	if err := os.Remove(modelStorageConfigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := modelStorageRun(ctx, "/usr/bin/systemctl", "reset-failed", modelStorageService); err != nil {
		return fmt.Errorf("clear shared model storage failure state: %w", err)
	}
	return nil
}

func writeModelStorageConfig(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
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
	if err := temporary.Chmod(0o600); err != nil {
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
	return os.Rename(name, path)
}
