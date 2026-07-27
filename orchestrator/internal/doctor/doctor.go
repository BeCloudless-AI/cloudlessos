// Package doctor gathers a bounded, read-only health report and creates a
// credential-redacted support bundle. It never mutates services or uploads data.
package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/state"
)

type Check struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"` // pass | warning | fail
	Summary string `json:"summary"`
	Action  string `json:"action,omitempty"`
}

type Report struct {
	Generated string               `json:"generated"`
	Overall   string               `json:"overall"`
	Platform  string               `json:"platform"`
	Arch      string               `json:"arch"`
	OS        string               `json:"os"`
	Kernel    string               `json:"kernel"`
	MemoryMB  int                  `json:"memoryMB"`
	DiskFree  uint64               `json:"diskFreeBytes"`
	GPUs      []hardware.GPU       `json:"gpus"`
	Checks    []Check              `json:"checks"`
	Promotion state.ModelPromotion `json:"modelPromotion,omitempty"`
}

// Run performs only local, read-only checks and bounds external probes by ctx.
func Run(ctx context.Context, eng engine.Engine, st *state.Store) Report {
	system := hardware.Sys()
	report := Report{
		Generated: time.Now().UTC().Format(time.RFC3339), Platform: system.Platform,
		Arch: runtime.GOARCH, OS: system.OS, Kernel: system.Kernel,
		MemoryMB: system.MemTotalMB, DiskFree: system.StorageAvailableBytes,
		Promotion: st.Get().ModelPromotion,
	}
	report.GPUs, _ = hardware.GPUs(ctx)
	for i := range report.GPUs {
		report.GPUs[i].Node = "local"
	}

	if err := eng.Available(ctx); err != nil {
		report.Checks = append(report.Checks, Check{"container-runtime", "Container runtime", "fail", "Docker is not reachable: " + err.Error(), "Restart Docker, then run Cloudless Doctor again."})
	} else {
		report.Checks = append(report.Checks, Check{"container-runtime", "Container runtime", "pass", "Docker is reachable.", ""})
	}
	if len(report.GPUs) == 0 {
		report.Checks = append(report.Checks, Check{"accelerator", "NVIDIA accelerator", "warning", "No usable NVIDIA accelerator was detected.", "Check the NVIDIA driver and container toolkit."})
	} else {
		report.Checks = append(report.Checks, Check{"accelerator", "NVIDIA accelerator", "pass", fmt.Sprintf("Detected %d NVIDIA accelerator(s).", len(report.GPUs)), ""})
	}
	diskGB := float64(system.StorageAvailableBytes) / (1 << 30)
	switch {
	case diskGB < 10:
		report.Checks = append(report.Checks, Check{"storage", "Storage", "fail", fmt.Sprintf("Only %.1f GB is free.", diskGB), "Free at least 25 GB before downloading models or updates."})
	case diskGB < 25:
		report.Checks = append(report.Checks, Check{"storage", "Storage", "warning", fmt.Sprintf("%.1f GB is free.", diskGB), "Large models may not fit; free space before downloading."})
	default:
		report.Checks = append(report.Checks, Check{"storage", "Storage", "pass", fmt.Sprintf("%.1f GB is free.", diskGB), ""})
	}
	if system.MemTotalMB > 0 && system.MemTotalMB < 8*1024 {
		report.Checks = append(report.Checks, Check{"memory", "System memory", "warning", fmt.Sprintf("Only %.1f GB RAM is available to the system.", float64(system.MemTotalMB)/1024), "Close other workloads or use a smaller model."})
	} else {
		report.Checks = append(report.Checks, Check{"memory", "System memory", "pass", fmt.Sprintf("%.1f GB system memory detected.", float64(system.MemTotalMB)/1024), ""})
	}

	containers, listErr := eng.List(ctx)
	if listErr == nil {
		running := map[string]engine.Container{}
		for _, container := range containers {
			if container.State == "running" {
				running[container.Name] = container
			}
		}
		currentState := st.Get()
		selectedEngine := currentState.Engine
		if selectedEngine == "" {
			selectedEngine = catalog.DefaultEngine()
		}
		required := make([]catalog.App, 0)
		for _, app := range catalog.Bundled() {
			if app.Preinstall && !app.Engine {
				required = append(required, app)
			}
		}
		if !currentState.EngineUnloaded {
			if selected, ok := catalog.Get(selectedEngine); ok {
				required = append(required, selected)
			}
		} else {
			report.Checks = append(report.Checks, Check{"inference-engine", "Inference engine", "pass", "The model is intentionally unloaded.", ""})
		}
		for _, app := range required {
			if _, ok := running[app.ContainerName()]; !ok {
				report.Checks = append(report.Checks, Check{"app-" + app.ID, app.Name, "fail", "Required service is not running.", "Restart CloudlessOS services or reinstall this component."})
				continue
			}
			if app.Health.Kind == "http" || app.Health.Kind == "openai" {
				if err := probeApp(ctx, app); err != nil {
					report.Checks = append(report.Checks, Check{"app-" + app.ID, app.Name, "warning", "Service is running but its health contract failed: " + err.Error(), "Wait for startup to finish, then retry Doctor."})
					continue
				}
			}
			report.Checks = append(report.Checks, Check{"app-" + app.ID, app.Name, "pass", "Service is running and reachable.", ""})
		}
	}
	if report.Promotion.Phase == "error" {
		report.Checks = append(report.Checks, Check{"model-promotion", "Full model promotion", "warning", "The full model failed verification; the bootstrap model was kept active.", "Retry the full model from Model Manager. Details are included in the support bundle."})
	} else if report.Promotion.Phase != "" && report.Promotion.Phase != "idle" {
		report.Checks = append(report.Checks, Check{"model-promotion", "Full model promotion", "pass", report.Promotion.Message, ""})
	}
	report.Overall = overall(report.Checks)
	return report
}

func probeApp(parent context.Context, app catalog.App) error {
	hostPort := app.Health.Port
	if hostPort == 0 {
		for host, container := range app.Ports {
			if container == app.Health.Port || app.Health.Port == 0 {
				hostPort = host
				break
			}
		}
	}
	if hostPort == 0 {
		return fmt.Errorf("health port is not mapped")
	}
	path := app.Health.Path
	if path == "" {
		path = "/"
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, path), nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil
}

func overall(checks []Check) string {
	result := "healthy"
	for _, check := range checks {
		if check.Status == "fail" {
			return "needs-attention"
		}
		if check.Status == "warning" {
			result = "warning"
		}
	}
	return result
}

var secretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?)[^\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|token|password|secret)\s*[:=]\s*)[^\s,"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`\b(?:hf_|sk-cloudless-)[A-Za-z0-9._-]+`), `[REDACTED]`},
	{regexp.MustCompile(`(?i)("(?:hash|prefix)"\s*:\s*)"[^"]*"`), `${1}"[REDACTED]"`},
}

// Redact removes common credentials and local identity/path fragments. It is
// applied after serialization and independently to every collected log.
func Redact(input string) string {
	output := input
	for _, rule := range secretPatterns {
		output = rule.pattern.ReplaceAllString(output, rule.replacement)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		output = strings.ReplaceAll(output, home, "[HOME]")
		output = strings.ReplaceAll(output, filepathSlash(home), "[HOME]")
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		output = strings.ReplaceAll(output, hostname, "[HOST]")
	}
	return output
}

func filepathSlash(path string) string { return strings.ReplaceAll(path, `\`, "/") }

// BuildBundle creates a ZIP in memory. Nothing is written or uploaded unless
// the API caller explicitly saves the returned download.
func BuildBundle(ctx context.Context, eng engine.Engine, st *state.Store, report Report) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	add := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = entry.Write([]byte(Redact(string(data))))
		return err
	}
	if err := add("doctor-report.json", report); err != nil {
		return nil, err
	}
	current := st.Get()
	safeState := map[string]any{
		"firstSeen": current.FirstSeen, "onboarded": current.Onboarded,
		"engine": current.Engine, "model": current.Model, "engineUnloaded": current.EngineUnloaded,
		"executionMode": current.ExecutionMode, "localNet": current.LocalNet,
		"display": current.Display, "modelPromotion": current.ModelPromotion,
		"installedPacks": current.InstalledPacks,
		"apiKeyCount": len(current.APIKeys), "customModelCount": len(current.CustomModels),
	}
	if err := add("state-summary.json", safeState); err != nil {
		return nil, err
	}
	containers, _ := eng.List(ctx)
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	if err := add("containers.json", containers); err != nil {
		return nil, err
	}
	for _, container := range containers {
		if !strings.HasPrefix(container.Name, "cloudless-") {
			continue
		}
		logs, err := eng.Logs(ctx, container.Name)
		if err != nil {
			continue
		}
		if len(logs) > 256*1024 {
			logs = logs[len(logs)-256*1024:]
		}
		entry, err := archive.Create("logs/" + strings.TrimPrefix(container.Name, "cloudless-") + ".log")
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write([]byte(Redact(logs))); err != nil {
			return nil, err
		}
	}
	identity := sha256.Sum256([]byte(report.Platform + ":" + report.Arch + ":" + report.Kernel))
	if err := add("bundle.json", map[string]string{"format": "cloudless-support-v1", "anonymousMachine": hex.EncodeToString(identity[:6])}); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
