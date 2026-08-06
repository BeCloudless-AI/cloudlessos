package api

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
)

var (
	containerANSISequencePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	pipCollectPattern            = regexp.MustCompile(`(?i)Collecting\s+([A-Za-z0-9_.-]+)(?:==[^\s]+)?`)
	pipDownloadPattern           = regexp.MustCompile(`(?i)Downloading\s+([^\s/]+\.whl)(?:\.metadata)?\s+\(([0-9.]+)\s*([KMGT]?B)\)`)
	pipSharedByteProgressPattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*/\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGT]?i?B)`)
	modelLoadPattern             = regexp.MustCompile(`(?i)Starting to load model\s+(.+?)(?:\.\.\.|$)`)
	checkpointSizePattern        = regexp.MustCompile(`(?i)Checkpoint size:\s*([0-9.]+\s*[KMGT]i?B)`)
	availableRAMPattern          = regexp.MustCompile(`(?i)Available RAM:\s*([0-9.]+\s*[KMGT]i?B)`)
	checkpointShardPattern       = regexp.MustCompile(`(?i)Loading safetensors checkpoint shards:\s*([0-9]+)%\s+Completed\s*\|\s*([0-9]+)\s*/\s*([0-9]+)(?:\s*\[([^<\]]*)<([^,\]]*))?`)
	deepGEMMWarmupPattern        = regexp.MustCompile(`(?i)DeepGEMM warmup:\s*([0-9]+)%.*\|\s*([0-9]+)\s*/\s*([0-9]+)`)
)

type recipeContainerProgress struct {
	Phase          string
	Message        string
	CurrentItem    string
	OverallPercent int
	ItemsDone      int
	ItemsTotal     int
	BytesDone      int64
	BytesTotal     int64
	ETASeconds     int64
	Components     []jobs.ComponentProgress
}

func parseClockDuration(value string) int64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 1 || len(parts) > 3 {
		return 0
	}
	var seconds int64
	for _, part := range parts {
		value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || value < 0 {
			return 0
		}
		seconds = seconds*60 + value
	}
	return seconds
}

func dependencyNameFromWheel(filename string) string {
	name := strings.TrimSuffix(strings.TrimSpace(filename), ".metadata")
	name = strings.TrimSuffix(name, ".whl")
	if index := strings.Index(name, "-"); index > 0 {
		name = name[:index]
	}
	return strings.ReplaceAll(name, "_", "-")
}

func recipeContainerEngineName(engineType string) string {
	switch strings.ToLower(strings.TrimSpace(engineType)) {
	case "vllm":
		return "vLLM"
	case "sglang":
		return "SGLang"
	default:
		if name := strings.TrimSpace(engineType); name != "" {
			return name
		}
		return "model server"
	}
}

func parseRecipeContainerProgress(logs string) (recipeContainerProgress, bool) {
	return parseRecipeContainerProgressForEngine(logs, "vllm")
}

func parseRecipeContainerProgressForEngine(logs, engineType string) (recipeContainerProgress, bool) {
	clean := containerANSISequencePattern.ReplaceAllString(logs, "")
	clean = strings.ReplaceAll(clean, "\r", "\n")
	engineName := recipeContainerEngineName(engineType)
	currentModel, checkpointSize, availableRAM, currentDependency := "", "", "", ""
	progress := recipeContainerProgress{}
	prefetchDisabled := false
	found := false
	for _, raw := range strings.Split(clean, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if match := pipCollectPattern.FindStringSubmatch(line); len(match) == 2 {
			dependency := strings.ReplaceAll(match[1], "_", "-")
			currentDependency = dependency
			progress = recipeContainerProgress{
				Phase: "installing-runtime", Message: "Preparing startup dependency " + dependency + "…",
				CurrentItem: dependency, OverallPercent: 51,
			}
			found = true
		}
		if match := pipDownloadPattern.FindStringSubmatch(line); len(match) == 4 {
			dependency := dependencyNameFromWheel(match[1])
			currentDependency = dependency
			progress = recipeContainerProgress{
				Phase: "installing-runtime", Message: fmt.Sprintf("Downloading startup dependency %s (%s %s)…", dependency, match[2], strings.ToUpper(match[3])),
				CurrentItem: dependency, OverallPercent: 52,
			}
			found = true
		}
		if currentDependency != "" {
			var done, total int64
			doneOK, totalOK := false, false
			if match := recipeByteProgressPattern.FindStringSubmatch(line); len(match) == 5 {
				done, doneOK = recipeByteValue(match[1], match[2])
				total, totalOK = recipeByteValue(match[3], match[4])
			} else if match := pipSharedByteProgressPattern.FindStringSubmatch(line); len(match) == 4 {
				done, doneOK = recipeByteValue(match[1], match[3])
				total, totalOK = recipeByteValue(match[2], match[3])
			}
			if doneOK && totalOK && total > 0 {
				percent := int(done * 100 / total)
				progress = recipeContainerProgress{
					Phase: "installing-runtime", Message: fmt.Sprintf("Downloading startup dependency %s — %s (%d%%)", currentDependency, formatDownloadProgress(done, total), percent),
					CurrentItem: currentDependency, OverallPercent: 52, BytesDone: done, BytesTotal: total,
				}
				found = true
			}
		}
		if strings.Contains(line, "Installing collected packages:") {
			message, currentItem := "Installing startup runtime dependencies…", engineName
			if strings.EqualFold(strings.TrimSpace(engineType), "vllm") {
				message, currentItem = "Installing FlashInfer and DFlash startup dependencies…", "FlashInfer / DFlash"
			}
			progress = recipeContainerProgress{Phase: "installing-runtime", Message: message, CurrentItem: currentItem, OverallPercent: 53}
			found = true
		}
		if strings.Contains(line, "Successfully installed") {
			progress = recipeContainerProgress{Phase: "initializing-engine", Message: "Startup dependencies are installed. Initializing " + engineName + "…", CurrentItem: engineName, OverallPercent: 54}
			found = true
		}
		if strings.Contains(line, "Initializing a V1 LLM engine") || strings.Contains(line, "Resolved architecture:") {
			progress = recipeContainerProgress{Phase: "initializing-engine", Message: engineName + " is resolving the model architecture and execution plan…", CurrentItem: engineName, OverallPercent: 55}
			found = true
		}
		if match := modelLoadPattern.FindStringSubmatch(line); len(match) == 2 {
			currentModel = strings.TrimSpace(match[1])
			checkpointSize = ""
			phase, percent, label := "loading-model", 56, "main model"
			if strings.Contains(strings.ToLower(currentModel), "dflash") || strings.Contains(strings.ToLower(currentModel), "draft") {
				phase, percent, label = "loading-draft", 72, "DFlash draft model"
			}
			progress = recipeContainerProgress{Phase: phase, Message: "Loading " + label + " " + currentModel + "…", CurrentItem: currentModel, OverallPercent: percent}
			found = true
		}
		if match := checkpointSizePattern.FindStringSubmatch(line); len(match) == 2 {
			checkpointSize = strings.TrimSpace(match[1])
			if ram := availableRAMPattern.FindStringSubmatch(line); len(ram) == 2 {
				availableRAM = strings.TrimSpace(ram[1])
			}
			if currentModel == "" {
				currentModel = "model weights"
			}
			phase, percent, label := "loading-model", 56, "main checkpoint"
			if strings.Contains(strings.ToLower(currentModel), "dflash") || strings.Contains(strings.ToLower(currentModel), "draft") {
				phase, percent, label = "loading-draft", 72, "DFlash checkpoint"
			}
			progress = recipeContainerProgress{Phase: phase, Message: fmt.Sprintf("Loading the %s (%s) from local storage…", label, checkpointSize), CurrentItem: currentModel, OverallPercent: percent}
			found = true
		}
		if strings.Contains(strings.ToLower(line), "auto-prefetch is disabled") {
			prefetchDisabled = true
			message := "Loading the checkpoint from local storage without prefetch"
			if availableRAM != "" {
				message += " because only " + availableRAM + " of RAM was available"
			}
			message += "…"
			progress = recipeContainerProgress{Phase: "loading-model", Message: message, CurrentItem: currentModel, OverallPercent: 56}
			found = true
		}
		if match := checkpointShardPattern.FindStringSubmatch(line); len(match) >= 4 {
			phase, base, width, label := "loading-model", 56, 15, "main model"
			if strings.Contains(strings.ToLower(currentModel), "dflash") || strings.Contains(strings.ToLower(currentModel), "draft") {
				phase, base, width, label = "loading-draft", 72, 5, "DFlash draft model"
			}
			shardPercent, _ := strconv.Atoi(match[1])
			done, _ := strconv.Atoi(match[2])
			total, _ := strconv.Atoi(match[3])
			message := fmt.Sprintf("Loading %s — checkpoint shard %d of %d (%d%%)", label, done, total, shardPercent)
			if checkpointSize != "" {
				message += " from " + checkpointSize
			}
			if prefetchDisabled {
				message += "; prefetch is off"
				if availableRAM != "" {
					message += " because " + availableRAM + " RAM was available"
				}
			}
			eta := int64(0)
			if len(match) >= 6 {
				eta = parseClockDuration(match[5])
			}
			progress = recipeContainerProgress{
				Phase: phase, Message: message, CurrentItem: currentModel,
				OverallPercent: base + shardPercent*width/100,
				ItemsDone:      done, ItemsTotal: total, ETASeconds: eta,
			}
			found = true
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "load weight begin") {
			currentModel = "model weights"
			progress = recipeContainerProgress{Phase: "loading-model", Message: "Loading model weights into " + engineName + "…", CurrentItem: currentModel, OverallPercent: 56}
			found = true
		}
		if strings.Contains(lower, "load weight end") {
			progress = recipeContainerProgress{Phase: "loading-model", Message: "Model weights are loaded. Preparing the " + engineName + " memory pool…", CurrentItem: engineName, OverallPercent: 72}
			found = true
		}
		if strings.Contains(lower, "memory pool end") {
			progress = recipeContainerProgress{Phase: "warming-engine", Message: engineName + " has allocated its model and KV-cache memory. Preparing optimized execution…", CurrentItem: engineName, OverallPercent: 77}
			found = true
		}
		if strings.Contains(lower, "model loading took") || strings.Contains(lower, "loading weights took") {
			progress = recipeContainerProgress{Phase: "warming-engine", Message: "Model weights are loaded. Finalizing distributed memory and execution state…", CurrentItem: engineName, OverallPercent: 76}
			found = true
		}
		if strings.Contains(lower, "no available shared memory broadcast block") {
			progress = recipeContainerProgress{Phase: "compiling-kernels", Message: "The Sparks are finishing compilation or KV-cache quantization…", CurrentItem: "GPU kernels / KV cache", OverallPercent: 78}
			found = true
		}
		if strings.Contains(lower, "torch.compile") || strings.Contains(lower, "compiling model") {
			progress = recipeContainerProgress{Phase: "compiling-kernels", Message: "Compiling optimized GPU kernels for this model and Spark…", CurrentItem: "GPU kernels", OverallPercent: 78}
			found = true
		}
		if strings.Contains(lower, "autotuning process starts") || strings.Contains(lower, "autotuning flashinfer") {
			progress = recipeContainerProgress{Phase: "compiling-kernels", Message: "Autotuning FlashInfer kernels for this model and Spark…", CurrentItem: "FlashInfer autotuning", OverallPercent: 79}
			found = true
		}
		if match := deepGEMMWarmupPattern.FindStringSubmatch(line); len(match) == 4 {
			percent, _ := strconv.Atoi(match[1])
			done, _ := strconv.Atoi(match[2])
			total, _ := strconv.Atoi(match[3])
			progress = recipeContainerProgress{Phase: "warming-engine", Message: fmt.Sprintf("Warming DeepGEMM kernels — %d of %d (%d%%)", done, total, percent), CurrentItem: "DeepGEMM kernels", OverallPercent: 80, ItemsDone: done, ItemsTotal: total}
			found = true
		}
		if strings.Contains(lower, "warmup finished") || strings.Contains(lower, "warming up deepseek") {
			progress = recipeContainerProgress{Phase: "warming-engine", Message: "Warming specialized model kernels and attention paths…", CurrentItem: "Model warmup", OverallPercent: 80}
			found = true
		}
		if strings.Contains(lower, "capturing cuda graph") || strings.Contains(lower, "graph capturing finished") || strings.Contains(lower, "warming up model") {
			progress = recipeContainerProgress{Phase: "warming-engine", Message: "Warming the engine and capturing reusable CUDA graphs…", CurrentItem: "CUDA graphs", OverallPercent: 80}
			found = true
		}
		if strings.Contains(lower, "application startup complete") || strings.Contains(lower, "uvicorn running on") ||
			strings.Contains(lower, "starting vllm api server") || strings.Contains(lower, "server is fired up and ready to roll") {
			progress = recipeContainerProgress{Phase: "health", Message: "The model server has started. Verifying its health and Cloudless model identity…", CurrentItem: "Health contract", OverallPercent: 82}
			found = true
		}
	}
	if dependencyProgress, ok := parseDependencyProgress(clean); ok {
		return dependencyProgress, true
	}
	return progress, found
}

func parseDependencyProgress(clean string) (recipeContainerProgress, bool) {
	components := []jobs.ComponentProgress{}
	indices := map[string]int{}
	current := ""
	phase, message := "", ""
	var currentDone, currentTotal int64
	complete := func(name string) {
		if index, ok := indices[name]; ok {
			components[index].Status = "complete"
			if components[index].BytesTotal > 0 {
				components[index].BytesDone = components[index].BytesTotal
			}
		}
	}
	start := func(name, status string, total int64) {
		if current != "" && current != name {
			complete(current)
		}
		current, currentDone, currentTotal = name, 0, total
		index, ok := indices[name]
		if !ok {
			index = len(components)
			indices[name] = index
			components = append(components, jobs.ComponentProgress{Name: name})
		}
		components[index].Status, components[index].BytesTotal = status, total
	}
	for _, raw := range strings.Split(clean, "\n") {
		line := strings.TrimSpace(raw)
		if match := pipCollectPattern.FindStringSubmatch(line); len(match) == 2 {
			name := strings.ReplaceAll(match[1], "_", "-")
			start(name, "preparing", 0)
			phase, message = "preparing-dependencies", "Preparing startup dependency "+name+"…"
		}
		if match := pipDownloadPattern.FindStringSubmatch(line); len(match) == 4 {
			name := dependencyNameFromWheel(match[1])
			total, _ := recipeByteValue(match[2], match[3])
			start(name, "downloading", total)
			phase, message = "downloading-dependencies", fmt.Sprintf("Downloading startup dependency %s (%s %s)…", name, match[2], strings.ToUpper(match[3]))
			continue // never attach an older progress line to this new wheel
		}
		if current != "" && phase == "downloading-dependencies" {
			var done, total int64
			var doneOK, totalOK bool
			if match := recipeByteProgressPattern.FindStringSubmatch(line); len(match) == 5 {
				done, doneOK = recipeByteValue(match[1], match[2])
				total, totalOK = recipeByteValue(match[3], match[4])
			} else if match := pipSharedByteProgressPattern.FindStringSubmatch(line); len(match) == 4 {
				done, doneOK = recipeByteValue(match[1], match[3])
				total, totalOK = recipeByteValue(match[2], match[3])
			}
			if doneOK && totalOK && total > 0 {
				currentDone, currentTotal = done, total
				index := indices[current]
				components[index].BytesDone, components[index].BytesTotal = done, total
				message = fmt.Sprintf("Downloading startup dependency %s — %s (%d%%)", current, formatDownloadProgress(done, total), done*100/total)
			}
		}
		if strings.Contains(line, "Installing collected packages:") {
			complete(current)
			for index := range components {
				components[index].Status = "installing"
			}
			phase, message = "installing-dependencies", "Installing startup dependencies…"
		}
		if strings.Contains(line, "Successfully installed") {
			return recipeContainerProgress{}, false
		}
	}
	if phase == "" {
		return recipeContainerProgress{}, false
	}
	percent := 51
	if phase == "downloading-dependencies" {
		percent = 52
	}
	if phase == "installing-dependencies" {
		percent = 53
	}
	return recipeContainerProgress{Phase: phase, Message: message, CurrentItem: current, OverallPercent: percent, BytesDone: currentDone, BytesTotal: currentTotal, Components: components}, true
}

const recipeDependencyStallThreshold = 75 * time.Second

type dependencyTransferTracker struct {
	item                       string
	baseReceived, lastReceived int64
	lastSample, lastMovement   time.Time
	bytesDone, rate            int64
}

func formatTransferRate(bytesPerSecond int64) string {
	const kib, mib, gib = 1024, 1024 * 1024, 1024 * 1024 * 1024
	switch {
	case bytesPerSecond >= gib:
		return fmt.Sprintf("%.1f GB/s", float64(bytesPerSecond)/gib)
	case bytesPerSecond >= mib:
		return fmt.Sprintf("%.1f MB/s", float64(bytesPerSecond)/mib)
	default:
		return fmt.Sprintf("%.0f KB/s", float64(bytesPerSecond)/kib)
	}
}

func (tracker *dependencyTransferTracker) update(now time.Time, item string, received, parsedDone, total int64) (done, rate, stalled, eta int64) {
	if tracker.item != item || received < tracker.lastReceived {
		tracker.item, tracker.baseReceived, tracker.lastReceived = item, received, received
		tracker.lastSample, tracker.lastMovement = now, now
		tracker.bytesDone, tracker.rate = parsedDone, 0
		return tracker.bytesDone, 0, 0, 0
	}
	delta, elapsed := received-tracker.lastReceived, now.Sub(tracker.lastSample)
	if delta > 0 {
		directDone := received - tracker.baseReceived
		if directDone > tracker.bytesDone {
			tracker.bytesDone = directDone
		}
		if parsedDone > tracker.bytesDone {
			tracker.bytesDone = parsedDone
		}
		if elapsed > 0 {
			tracker.rate = int64(float64(delta) / elapsed.Seconds())
		}
		tracker.lastMovement = now
	} else {
		tracker.rate = 0
	}
	if total > 0 && tracker.bytesDone > total {
		tracker.bytesDone = total
	}
	tracker.lastReceived, tracker.lastSample = received, now
	if !tracker.lastMovement.IsZero() {
		stalled = int64(now.Sub(tracker.lastMovement).Seconds())
	}
	if tracker.rate > 0 && total > tracker.bytesDone {
		eta = (total - tracker.bytesDone) / tracker.rate
	}
	return tracker.bytesDone, tracker.rate, stalled, eta
}

// observeRecipeContainerStartup reads captured container output without
// entering or mutating the container. Docker health remains authoritative;
// log parsing only explains the work occurring before the health contract is
// ready.
func observeRecipeContainerStartup(ctx context.Context, runtime engine.Engine, job *jobs.Job, containerName, engineType string, interval time.Duration) func() {
	if runtime == nil || job == nil || strings.TrimSpace(containerName) == "" {
		return func() {}
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	observerContext, cancel := context.WithCancel(ctx)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		previous := ""
		var transfer dependencyTransferTracker
		poll := func() {
			logContext, stop := context.WithTimeout(observerContext, 5*time.Second)
			logs, err := runtime.Logs(logContext, containerName)
			stop()
			if err != nil {
				return
			}
			progress, ok := parseRecipeContainerProgressForEngine(logs, engineType)
			if !ok {
				return
			}
			if progress.Phase == "downloading-dependencies" && progress.BytesTotal > 0 {
				if reader, available := runtime.(engine.ContainerIOReader); available {
					ioContext, stopIO := context.WithTimeout(observerContext, 3*time.Second)
					ioSnapshot, ioErr := reader.ContainerIO(ioContext, containerName)
					stopIO()
					if ioErr == nil {
						done, rate, stalled, eta := transfer.update(time.Now(), progress.CurrentItem, ioSnapshot.ReceivedBytes, progress.BytesDone, progress.BytesTotal)
						progress.BytesDone, progress.ETASeconds = done, eta
						for index := range progress.Components {
							if progress.Components[index].Name == progress.CurrentItem {
								progress.Components[index].BytesDone, progress.Components[index].BytesTotal = done, progress.BytesTotal
								progress.Components[index].BytesPerSec, progress.Components[index].ETASeconds = rate, eta
							}
						}
						message := fmt.Sprintf("Downloading startup dependency %s — %s", progress.CurrentItem, formatDownloadProgress(done, progress.BytesTotal))
						if rate > 0 {
							message += " at " + formatTransferRate(rate)
						}
						if eta > 0 {
							message += " · about " + (time.Duration(eta) * time.Second).Round(time.Second).String() + " remaining"
						}
						if stalled >= int64(recipeDependencyStallThreshold.Seconds()) {
							message = fmt.Sprintf("Download stalled — no data received for %s", time.Duration(stalled)*time.Second)
						}
						progress.Message = message
						job.ProgressTransfer(progress.Phase, progress.Message, progress.CurrentItem, progress.OverallPercent, progress.BytesDone, progress.BytesTotal, rate, stalled, progress.ETASeconds, progress.Components)
						previous = fmt.Sprintf("%#v", progress)
						return
					}
				}
			}
			signature := fmt.Sprintf("%#v", progress)
			if signature == previous {
				return
			}
			previous = signature
			job.ProgressOperationETA(progress.Phase, progress.Message, progress.CurrentItem, progress.OverallPercent, progress.ItemsDone, progress.ItemsTotal, progress.ETASeconds)
			if progress.BytesTotal > 0 || len(progress.Components) > 0 {
				job.ProgressTransfer(progress.Phase, progress.Message, progress.CurrentItem, progress.OverallPercent, progress.BytesDone, progress.BytesTotal, 0, 0, progress.ETASeconds, progress.Components)
			}
		}
		poll()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-observerContext.Done():
				return
			case <-ticker.C:
				poll()
			}
		}
	}()
	return func() {
		cancel()
		wait.Wait()
	}
}
