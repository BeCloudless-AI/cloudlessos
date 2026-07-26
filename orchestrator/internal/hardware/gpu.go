// Package hardware reports host hardware info. Currently it surfaces NVIDIA GPUs
// via nvidia-smi; AMD/ROCm can be added behind the same shapes later.
package hardware

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	hostplatform "github.com/cloudless/orchestrator/internal/platform"
)

// GPU is a single GPU's live stats.
type GPU struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	MemUsedMB   int     `json:"memUsedMB"`
	MemTotalMB  int     `json:"memTotalMB"`
	UtilPct     int     `json:"utilPct"`
	TempC       int     `json:"tempC"`
	PowerW      float64 `json:"powerW"`
	PowerLimitW float64 `json:"powerLimitW"`
	Driver      string  `json:"driver"`
	MemoryType  string  `json:"memoryType"` // dedicated | unified
	Node        string  `json:"node,omitempty"`
	Remote      bool    `json:"remote,omitempty"`
}

// gpuTTL is how long a nvidia-smi snapshot is reused. GPU stats are read-only
// telemetry, so sub-second staleness is harmless — and several pollers (the home
// panel, the inference hardware band, /api/system) hit this, so caching bounds
// nvidia-smi to ~one fork per TTL no matter how many callers overlap.
const gpuTTL = 700 * time.Millisecond

var (
	gpuMu    sync.Mutex
	gpuCache []GPU
	gpuErr   error
	gpuAt    time.Time
)

// GPUs returns the latest GPU snapshot, querying nvidia-smi at most once per gpuTTL.
// The lock is held across the query, so a burst of concurrent callers (e.g. on page
// load) collapses to a single fork rather than each spawning its own nvidia-smi.
func GPUs(ctx context.Context) ([]GPU, error) {
	gpuMu.Lock()
	defer gpuMu.Unlock()
	if gpuCache != nil && time.Since(gpuAt) < gpuTTL {
		return gpuCache, gpuErr
	}
	gpuCache, gpuErr = queryGPUs(ctx)
	gpuAt = time.Now()
	return gpuCache, gpuErr
}

// queryGPUs runs nvidia-smi for all NVIDIA GPUs. Returns an empty slice (and the
// error) when nvidia-smi is absent or reports nothing.
func queryGPUs(ctx context.Context) ([]GPU, error) {
	cmd := exec.CommandContext(ctx, nvidiaSMIPath(),
		"--query-gpu=index,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw,power.limit,driver_version",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return []GPU{}, err
	}

	gpus := ParseGPUsCSV(string(out))
	if hostplatform.IsDGXSpark() {
		total, available := memInfoMB()
		ApplyUnifiedMemory(gpus, total, available)
	}
	return gpus, nil
}

// ParseGPUsCSV parses the stable nounits nvidia-smi query format used locally
// and over the private Spark management connection.
func ParseGPUsCSV(out string) []GPU {
	gpus := []GPU{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := splitCSV(sc.Text())
		if len(f) < 9 {
			continue
		}
		gpus = append(gpus, GPU{
			Index:       atoi(f[0]),
			Name:        f[1],
			MemUsedMB:   atoi(f[2]),
			MemTotalMB:  atoi(f[3]),
			UtilPct:     atoi(f[4]),
			TempC:       atoi(f[5]),
			PowerW:      atof(f[6]),
			PowerLimitW: atof(f[7]),
			Driver:      f[8],
			MemoryType:  "dedicated",
		})
	}
	return gpus
}

// ApplyUnifiedMemory maps host unified-memory capacity onto each GB10 GPU.
func ApplyUnifiedMemory(gpus []GPU, totalMB, availableMB int) {
	if totalMB <= 0 {
		return
	}
	used := totalMB - availableMB
	if used < 0 {
		used = 0
	}
	for i := range gpus {
		gpus[i].MemTotalMB = totalMB
		gpus[i].MemUsedMB = used
		gpus[i].MemoryType = "unified"
	}
}

// AcceleratorMemoryGB returns the capacity used for model-fit decisions and
// whether that capacity is dedicated VRAM or CPU/GPU unified memory.
func AcceleratorMemoryGB(ctx context.Context) (int, string) {
	gpus, _ := GPUs(ctx)
	if len(gpus) == 0 {
		return 0, "unknown"
	}
	if gpus[0].MemoryType == "unified" {
		return gpus[0].MemTotalMB / 1024, "unified"
	}
	total := 0
	for _, gpu := range gpus {
		if gpu.MemTotalMB > 0 {
			total += gpu.MemTotalMB
		}
	}
	return total / 1024, "dedicated"
}

// nvidiaSMIPath supports both a normal Linux installation and NVIDIA's WSL
// driver bridge, which exposes nvidia-smi outside the default distro PATH.
func nvidiaSMIPath() string {
	if path, err := exec.LookPath("nvidia-smi"); err == nil {
		return path
	}
	const wslPath = "/usr/lib/wsl/lib/nvidia-smi"
	if info, err := os.Stat(wslPath); err == nil && !info.IsDir() {
		return wslPath
	}
	return "nvidia-smi"
}

func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func atof(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return -1
	}
	return f
}
