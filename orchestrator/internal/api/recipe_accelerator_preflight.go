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
	"time"

	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	hostplatform "github.com/cloudless/orchestrator/internal/platform"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func recipeMinimumAcceleratorMemoryMB(modelBytes int64, nodes int) int64 {
	if modelBytes <= 0 {
		return 0
	}
	if nodes < 1 {
		nodes = 1
	}
	// This is deliberately a lower bound, not a promise about maximum context:
	// weights are distributed over the requested nodes and need headroom for
	// runtime metadata and activation buffers. The recipe-specific runtime owns
	// KV-cache sizing and is validated separately by its health/contract probe.
	perNode := float64(modelBytes) / float64(nodes)
	requiredBytes := int64(math.Ceil(perNode * 1.20))
	const mib = int64(1024 * 1024)
	return (requiredBytes + mib - 1) / mib
}

func parseRecipeComputeCapabilities(output string) ([]float64, error) {
	values := []float64{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, ",", 2)[0])
		if line == "" {
			continue
		}
		value, err := strconv.ParseFloat(line, 64)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("invalid NVIDIA compute capability %q", line)
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, errors.New("NVIDIA did not report a compute capability")
	}
	return values, nil
}

func localRecipeComputeCapabilities(ctx context.Context) ([]float64, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	output, err := recipeCommandOutput(recipeLocalCommand(probeCtx, "/", nil, "nvidia-smi",
		"--query-gpu=compute_cap", "--format=csv,noheader,nounits"))
	if err != nil {
		return nil, err
	}
	return parseRecipeComputeCapabilities(output)
}

func peerRecipeComputeCapabilities(ctx context.Context, checkout string, env map[string]string, peer recipePeer) ([]float64, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	output, err := recipeCommandOutput(recipeSSHCommand(probeCtx, checkout, env, peer, "nvidia-smi",
		"--query-gpu=compute_cap", "--format=csv,noheader,nounits"))
	if err != nil {
		return nil, err
	}
	return parseRecipeComputeCapabilities(output)
}

func recipeGPUMemoryTotalMB(gpus []hardware.GPU) int64 {
	var total int64
	for _, gpu := range gpus {
		if gpu.MemTotalMB > 0 {
			total += int64(gpu.MemTotalMB)
		}
	}
	return total
}

func minimumRecipeComputeCapability(recipe localrecipes.Recipe) float64 {
	if recipe.Platform == hostplatform.DGXSpark {
		return 12.0
	}
	return 0
}

func hasNVIDIACDISpecAt(root string) bool {
	for _, directory := range []string{"etc/cdi", "var/run/cdi"} {
		matches, _ := filepath.Glob(filepath.Join(root, directory, "*nvidia*.yaml"))
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				return true
			}
		}
	}
	return false
}

func (s *Server) verifyLocalRecipeGPUContainerRuntime(ctx context.Context) (string, error) {
	mode := hostplatform.GPUContainerMode()
	if mode == hostplatform.GPUCDI {
		if !hasNVIDIACDISpecAt("/") {
			return "", errors.New("NVIDIA CDI device specification is unavailable")
		}
		return mode, nil
	}
	if s.eng == nil {
		return "", errors.New("container engine is unavailable")
	}
	available, err := s.eng.HasNVIDIARuntime(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect Docker GPU runtimes: %w", err)
	}
	if !available {
		return "", errors.New("Docker does not report the NVIDIA runtime")
	}
	return mode, nil
}

func verifyPeerRecipeGPUContainerRuntime(ctx context.Context, checkout string, env map[string]string, peer recipePeer, mode string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var probe string
	if mode == hostplatform.GPUCDI {
		probe = `find /etc/cdi /var/run/cdi -maxdepth 1 -type f -name '*nvidia*.yaml' -size +0c -print -quit 2>/dev/null | grep -q .`
	} else {
		probe = `docker info --format '{{json .Runtimes}}' 2>/dev/null | grep -qi '"nvidia"'`
	}
	if _, err := recipeCommandOutput(recipeSSHCommand(probeCtx, checkout, env, peer, "/bin/sh", "-c", probe)); err != nil {
		return fmt.Errorf("%s does not expose the required NVIDIA container runtime: %w", peer.Name, err)
	}
	return nil
}

func verifyRecipeGPUNode(name string, gpus []hardware.GPU, capabilities []float64, minimumMemoryMB int64, minimumCapability float64, expectedDriver string) (string, error) {
	if len(gpus) == 0 {
		return "", fmt.Errorf("%s has no usable NVIDIA accelerator", name)
	}
	if len(capabilities) != len(gpus) {
		return "", fmt.Errorf("%s returned inconsistent accelerator metadata", name)
	}
	for _, capability := range capabilities {
		if capability < minimumCapability {
			return "", fmt.Errorf("%s compute capability %.1f is below required %.1f", name, capability, minimumCapability)
		}
	}
	memoryMB := recipeGPUMemoryTotalMB(gpus)
	if minimumMemoryMB > 0 && memoryMB < minimumMemoryMB {
		return "", fmt.Errorf("%s exposes %d MiB accelerator memory; at least %d MiB is required", name, memoryMB, minimumMemoryMB)
	}
	driver := strings.TrimSpace(gpus[0].Driver)
	if driver == "" {
		return "", fmt.Errorf("%s did not report an NVIDIA driver version", name)
	}
	if expectedDriver != "" && driver != expectedDriver {
		return "", fmt.Errorf("%s uses NVIDIA driver %s while the coordinator uses %s", name, driver, expectedDriver)
	}
	return driver, nil
}

func (s *Server) preflightRecipeAccelerators(ctx context.Context, recipe localrecipes.Recipe, cluster sparkcluster.State, checkout string, env map[string]string, modelBytes int64) (map[string]string, error) {
	platformName := hostplatform.Detect()
	if recipe.Platform == hostplatform.DGXSpark && platformName != hostplatform.DGXSpark {
		return nil, fmt.Errorf("recipe requires DGX Spark but this machine reports %s", platformName)
	}
	if recipe.Platform == hostplatform.DGXSpark && hostplatform.Architecture() != "arm64" {
		return nil, fmt.Errorf("DGX Spark recipe requires arm64 but this machine reports %s", hostplatform.Architecture())
	}
	localGPUs, err := hardware.GPUs(ctx)
	if err != nil {
		return nil, fmt.Errorf("query local NVIDIA accelerator: %w", err)
	}
	localCapabilities, err := localRecipeComputeCapabilities(ctx)
	if err != nil {
		return nil, fmt.Errorf("query local compute capability: %w", err)
	}
	minimumMemoryMB := recipeMinimumAcceleratorMemoryMB(modelBytes, recipe.Distributed.Nodes)
	minimumCapability := minimumRecipeComputeCapability(recipe)
	localNode := localRecipeNodeName()
	driver, err := verifyRecipeGPUNode(localNode, localGPUs, localCapabilities, minimumMemoryMB, minimumCapability, "")
	if err != nil {
		return nil, err
	}
	containerMode, err := s.verifyLocalRecipeGPUContainerRuntime(ctx)
	if err != nil {
		return nil, err
	}
	values := map[string]string{
		"platform": platformName, "architecture": hostplatform.Architecture(),
		"minimumMemoryMBPerNode":   strconv.FormatInt(minimumMemoryMB, 10),
		"minimumComputeCapability": strconv.FormatFloat(minimumCapability, 'f', 1, 64),
		"local.name":               localGPUs[0].Name, "local.driver": driver,
		"local.memoryMB":          strconv.FormatInt(recipeGPUMemoryTotalMB(localGPUs), 10),
		"local.computeCapability": strconv.FormatFloat(localCapabilities[0], 'f', 1, 64),
		"containerGpuMode":        containerMode,
	}
	if recipe.Platform == hostplatform.DGXSpark && localGPUs[0].MemoryType != "unified" {
		return nil, errors.New("DGX Spark recipe requires unified accelerator memory")
	}
	if recipe.Distributed.Nodes > 1 {
		peers, err := recipeDistributionPeers(recipe, cluster, env)
		if err != nil {
			return nil, err
		}
		telemetry, err := sparkcluster.ClusterGPUs(ctx)
		if err != nil || len(telemetry) != len(peers) {
			return nil, errors.New("cluster accelerator telemetry is incomplete")
		}
		for index, peer := range peers {
			if !telemetry[index].Reachable || telemetry[index].Error != "" {
				return nil, fmt.Errorf("%s accelerator telemetry is unavailable: %s", peer.Name, telemetry[index].Error)
			}
			capabilities, capabilityErr := peerRecipeComputeCapabilities(ctx, checkout, env, peer)
			if capabilityErr != nil {
				return nil, fmt.Errorf("%s compute capability: %w", peer.Name, capabilityErr)
			}
			if runtimeErr := verifyPeerRecipeGPUContainerRuntime(ctx, checkout, env, peer, containerMode); runtimeErr != nil {
				return nil, runtimeErr
			}
			peerDriver, verifyErr := verifyRecipeGPUNode(peer.Name, telemetry[index].GPUs, capabilities, minimumMemoryMB, minimumCapability, driver)
			if verifyErr != nil {
				return nil, verifyErr
			}
			if recipe.Platform == hostplatform.DGXSpark && telemetry[index].GPUs[0].MemoryType != "unified" {
				return nil, fmt.Errorf("%s does not report unified accelerator memory", peer.Name)
			}
			prefix := "peer." + peer.Name + "."
			values[prefix+"name"] = telemetry[index].GPUs[0].Name
			values[prefix+"driver"] = peerDriver
			values[prefix+"memoryMB"] = strconv.FormatInt(recipeGPUMemoryTotalMB(telemetry[index].GPUs), 10)
			values[prefix+"computeCapability"] = strconv.FormatFloat(capabilities[0], 'f', 1, 64)
		}
	}
	return values, nil
}
