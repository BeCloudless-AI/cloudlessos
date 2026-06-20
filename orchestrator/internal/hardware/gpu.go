// Package hardware reports host hardware info. Currently it surfaces NVIDIA GPUs
// via nvidia-smi; AMD/ROCm can be added behind the same shapes later.
package hardware

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
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
}

// GPUs queries nvidia-smi for all NVIDIA GPUs. Returns an empty slice (and the
// error) when nvidia-smi is absent or reports nothing.
func GPUs(ctx context.Context) ([]GPU, error) {
	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=index,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw,power.limit,driver_version",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return []GPU{}, err
	}

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
		})
	}
	return gpus, nil
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
