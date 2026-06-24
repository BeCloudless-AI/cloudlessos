package hardware

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
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
}

// Sys gathers host facts from /etc/os-release, /proc, and the Go runtime.
func Sys() System {
	s := System{Arch: runtime.GOARCH, Cores: runtime.NumCPU()}
	if h, err := os.Hostname(); err == nil {
		s.Hostname = h
	}
	s.OS = osReleaseField("PRETTY_NAME")
	s.Kernel = firstLine("/proc/sys/kernel/osrelease")
	s.CPU, s.Cores = cpuInfo(s.Cores)
	s.MemTotalMB = memTotalMB()
	s.UptimeSec = uptimeSec()
	return s
}

// osReleaseField reads KEY="value" (or KEY=value) from /etc/os-release.
func osReleaseField(key string) string {
	b, err := os.ReadFile("/etc/os-release")
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
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
			fields := strings.Fields(v) // "<kB> kB"
			if len(fields) >= 1 {
				if kb, err := strconv.Atoi(fields[0]); err == nil {
					return kb / 1024
				}
			}
		}
	}
	return 0
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
