package hardware

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// LoadInfo is the host's live CPU + RAM utilization (Linux /proc).
type LoadInfo struct {
	CPUModel   string  `json:"cpuModel"`
	Cores      int     `json:"cores"`
	CPUPct     float64 `json:"cpuPct"` // 0..100 over a short sample window
	MemUsedMB  int     `json:"memUsedMB"`
	MemTotalMB int     `json:"memTotalMB"`
}

// Load samples CPU utilization over a brief window and reads RAM use. Best-effort.
func Load() LoadInfo {
	t0, b0 := cpuJiffies()
	time.Sleep(120 * time.Millisecond)
	t1, b1 := cpuJiffies()
	pct := 0.0
	if t1 > t0 {
		pct = float64(b1-b0) / float64(t1-t0) * 100
	}
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	model, cores := cpuInfo(runtime.NumCPU())
	total, avail := memKB()
	used := total - avail
	if used < 0 {
		used = 0
	}
	return LoadInfo{CPUModel: model, Cores: cores, CPUPct: pct, MemUsedMB: used / 1024, MemTotalMB: total / 1024}
}

// cpuJiffies returns total and busy (non-idle) jiffies from /proc/stat's "cpu" line.
func cpuJiffies() (total, busy uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		fields := strings.Fields(sc.Text()) // cpu user nice system idle iowait irq softirq steal ...
		if len(fields) < 5 || fields[0] != "cpu" {
			return 0, 0
		}
		var idle uint64
		for i := 1; i < len(fields); i++ {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			total += v
			if i == 4 || i == 5 { // idle + iowait
				idle += v
			}
		}
		busy = total - idle
	}
	return
}

// memKB returns MemTotal and MemAvailable from /proc/meminfo (in kB).
func memKB() (total, avail int) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			total = firstInt(v)
		} else if v, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			avail = firstInt(v)
		}
	}
	return
}

func firstInt(s string) int {
	if f := strings.Fields(s); len(f) > 0 {
		n, _ := strconv.Atoi(f[0])
		return n
	}
	return 0
}
