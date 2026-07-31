package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

const (
	recipeCapacityMinimumReserve  int64 = 5 * 1024 * 1024 * 1024
	recipeRegistryExpansionFactor int64 = 2
)

type recipeCapacityRequirement struct {
	ModelBytes     int64
	RuntimeBytes   int64
	WorkspaceBytes int64
	ReserveBytes   int64
	RequiredBytes  int64
	AvailableBytes int64
}

func calculateRecipeCapacity(modelBytes, runtimeBytes, workspaceBytes, availableBytes int64, modelReady, runtimeReady bool) (recipeCapacityRequirement, error) {
	if modelBytes < 0 || runtimeBytes < 0 || workspaceBytes < 0 || availableBytes < 0 {
		return recipeCapacityRequirement{}, errors.New("recipe capacity values cannot be negative")
	}
	missingModel, missingRuntime := modelBytes, runtimeBytes
	if modelReady {
		missingModel = 0
	}
	if runtimeReady {
		missingRuntime = 0
	}
	if missingModel > math.MaxInt64-missingRuntime {
		return recipeCapacityRequirement{}, errors.New("recipe capacity calculation overflowed")
	}
	base := missingModel + missingRuntime
	if base > math.MaxInt64-workspaceBytes {
		return recipeCapacityRequirement{}, errors.New("recipe capacity calculation overflowed")
	}
	base += workspaceBytes
	reserve := base / 10
	if reserve < recipeCapacityMinimumReserve {
		reserve = recipeCapacityMinimumReserve
	}
	if base > math.MaxInt64-reserve {
		return recipeCapacityRequirement{}, errors.New("recipe capacity calculation overflowed")
	}
	return recipeCapacityRequirement{
		ModelBytes: missingModel, RuntimeBytes: missingRuntime, WorkspaceBytes: workspaceBytes,
		ReserveBytes: reserve, RequiredBytes: base + reserve, AvailableBytes: availableBytes,
	}, nil
}

func filesystemAvailableBytes(path string) (int64, string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		path = "/"
	}
	for {
		if _, err := os.Stat(path); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return 0, path, err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, path, errors.New("no existing parent filesystem was found")
		}
		path = parent
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, path, err
	}
	if stat.Bsize <= 0 || uint64(stat.Bavail) > math.MaxInt64/uint64(stat.Bsize) {
		return 0, path, errors.New("filesystem capacity is invalid")
	}
	return int64(uint64(stat.Bavail) * uint64(stat.Bsize)), path, nil
}

func recipeModelPreflightBytes(ctx context.Context, recipe localrecipes.Recipe, token string) (int64, error) {
	if filepath.IsAbs(recipe.Model.ID) {
		info, err := os.Stat(recipe.Model.ID)
		if err != nil {
			return 0, fmt.Errorf("inspect local model path: %w", err)
		}
		if !info.IsDir() {
			return info.Size(), nil
		}
		size := directoryBytes(recipe.Model.ID)
		if size <= 0 {
			return 0, errors.New("local model path contains no readable files")
		}
		return size, nil
	}
	return huggingFaceModelRevisionSize(ctx, recipe.Model.ID, recipe.Model.Revision, token)
}

func peerRecipeAvailableBytes(ctx context.Context, checkout string, env map[string]string, peer recipePeer) (int64, error) {
	marker := string(os.PathSeparator) + ".local" + string(os.PathSeparator)
	markerIndex := strings.Index(peer.Checkout, marker)
	if markerIndex < len("/home/x") {
		return 0, errors.New("peer checkout does not identify a safe home directory")
	}
	home := peer.Checkout[:markerIndex]
	if !strings.HasPrefix(home, "/home/") || strings.Contains(strings.TrimPrefix(home, "/home/"), "/") {
		return 0, errors.New("peer checkout does not identify a safe home directory")
	}
	const probe = `df -PB1 --output=avail "$1" 2>/dev/null | tail -n 1`
	output, err := recipeCommandOutput(recipeSSHCommand(ctx, checkout, env, peer,
		"/bin/sh", "-c", probe, "cloudless-capacity", home))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return 0, errors.New("peer returned no filesystem capacity")
	}
	available, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err != nil || available < 0 {
		return 0, errors.New("peer returned invalid filesystem capacity")
	}
	return available, nil
}

func (capacity recipeCapacityRequirement) values(prefix string) map[string]string {
	return map[string]string{
		prefix + "modelBytes":     strconv.FormatInt(capacity.ModelBytes, 10),
		prefix + "runtimeBytes":   strconv.FormatInt(capacity.RuntimeBytes, 10),
		prefix + "workspaceBytes": strconv.FormatInt(capacity.WorkspaceBytes, 10),
		prefix + "reserveBytes":   strconv.FormatInt(capacity.ReserveBytes, 10),
		prefix + "requiredBytes":  strconv.FormatInt(capacity.RequiredBytes, 10),
		prefix + "availableBytes": strconv.FormatInt(capacity.AvailableBytes, 10),
	}
}
