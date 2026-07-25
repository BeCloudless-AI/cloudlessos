package hardware

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	hostplatform "github.com/cloudless/orchestrator/internal/platform"
)

// LoadInfo is the host's live utilization plus filesystem capacity.
type LoadInfo struct {
	CPUModel              string  `json:"cpuModel"`
	Cores                 int     `json:"cores"`
	CPUPct                float64 `json:"cpuPct"` // 0..100 since the previous Load() call
	MemUsedMB             int     `json:"memUsedMB"`
	MemTotalMB            int     `json:"memTotalMB"`
	Platform              string  `json:"platform"`
	StorageTotalBytes     uint64  `json:"storageTotalBytes"`
	StorageAvailableBytes uint64  `json:"storageAvailableBytes"`
}

var (
	loadMu              sync.Mutex
	lastTotal, lastBusy uint64
	lastPct             float64
	haveLast            bool
)

// Load reports CPU utilization (over the interval since the previous call) and RAM
// use. It does NOT sleep — the dashboard polls it on a ticker, so the gap between
// calls IS the sample window. This keeps /api/sysload instant instead of blocking
// ~120ms per request (which made the system panel appear in a delayed stage).
func Load() LoadInfo {
	total, busy := cpuJiffies()
	loadMu.Lock()
	pct := lastPct
	if haveLast && total > lastTotal {
		pct = float64(busy-lastBusy) / float64(total-lastTotal) * 100
	}
	lastTotal, lastBusy, haveLast = total, busy, true
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	lastPct = pct
	loadMu.Unlock()

	model, cores := cpuInfo(runtime.NumCPU())
	total2, avail := memKB()
	used := total2 - avail
	if used < 0 {
		used = 0
	}
	storageTotal, storageAvailable := storageBytes(storagePath())
	return LoadInfo{
		CPUModel: model, Cores: cores, CPUPct: pct,
		MemUsedMB: used / 1024, MemTotalMB: total2 / 1024,
		Platform:          hostplatform.Detect(),
		StorageTotalBytes: storageTotal, StorageAvailableBytes: storageAvailable,
	}
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
