package hardware

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"

	hostplatform "github.com/cloudless/orchestrator/internal/platform"
)

// System is a best-effort snapshot of the host machine (Linux). Fields that can't
// be read are left zero/empty rather than failing — the UI degrades gracefully.
type System struct {
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`         // distro pretty name, e.g. "Ubuntu 24.04.1 LTS"
	Kernel     string `json:"kernel"`     // e.g. "6.6.87.2-microsoft-standard-WSL2"
	Arch       string `json:"arch"`       // GOARCH, e.g. "amd64"
	CPU        string `json:"cpu"`        // model name, e.g. "AMD Ryzen 9 7950X"
	Cores      int    `json:"cores"`      // logical CPUs
	MemTotalMB int    `json:"memTotalMB"` // total RAM
	UptimeSec  int64  `json:"uptimeSec"`  // host uptime
	Platform   string `json:"platform"`   // generic | dgx-spark
	Product    string `json:"product,omitempty"`
	DGXVersion string `json:"dgxVersion,omitempty"`
	UnifiedGPU bool   `json:"unifiedGpuMemory"`
}

// Sys gathers host facts from /etc/os-release, /proc, and the Go runtime.
func Sys() System {
	platformName := hostplatform.Detect()
	s := System{
		Arch: runtime.GOARCH, Cores: runtime.NumCPU(), Platform: platformName,
		UnifiedGPU: platformName == hostplatform.DGXSpark,
	}
	if h, err := os.Hostname(); err == nil {
		s.Hostname = h
	}
	s.OS = osReleaseField("PRETTY_NAME")
	s.Kernel = firstLine("/proc/sys/kernel/osrelease")
	s.CPU, s.Cores = cpuInfo(s.Cores)
	s.MemTotalMB = memTotalMB()
	s.UptimeSec = uptimeSec()
	s.Product = firstLine("/sys/devices/virtual/dmi/id/product_name")
	s.DGXVersion = keyValueFileField("/etc/dgx-release", "DGX_OTA_VERSION")
	if s.DGXVersion == "" {
		s.DGXVersion = keyValueFileField("/etc/dgx-release", "DGX_SWBUILD_VERSION")
	}
	return s
}

// osReleaseField reads KEY="value" (or KEY=value) from /etc/os-release.
func osReleaseField(key string) string {
	return keyValueFileField("/etc/os-release", key)
}

func keyValueFileField(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func firstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
}

// cpuInfo returns the CPU model and a physical/logical core count from /proc/cpuinfo.
func cpuInfo(fallbackCores int) (model string, cores int) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", fallbackCores
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "processor") {
			n++
		}
		if model == "" {
			if v, ok := strings.CutPrefix(line, "model name"); ok {
				if i := strings.Index(v, ":"); i >= 0 {
					model = strings.TrimSpace(v[i+1:])
				}
			}
		}
	}
	if n == 0 {
		n = fallbackCores
	}
	return model, n
}

func memTotalMB() int {
	total, _ := memInfoMB()
	return total
}

func memInfoMB() (total, available int) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		for _, field := range []struct {
			prefix string
			target *int
		}{
			{"MemTotal:", &total},
			{"MemAvailable:", &available},
		} {
			if v, ok := strings.CutPrefix(line, field.prefix); ok {
				parts := strings.Fields(v)
				if len(parts) > 0 {
					if kb, err := strconv.Atoi(parts[0]); err == nil {
						*field.target = kb / 1024
					}
				}
			}
		}
	}
	return total, available
}

func uptimeSec() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	if f, err := strconv.ParseFloat(fields[0], 64); err == nil {
		return int64(f)
	}
	return 0
}
