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

func parseRecipeContainerProgress(logs string) (recipeContainerProgress, bool) {
	clean := containerANSISequencePattern.ReplaceAllString(logs, "")
	clean = strings.ReplaceAll(clean, "\r", "\n")
	currentModel, checkpointSize, availableRAM, currentDependency := "", "", "", ""
	prefetchDisabled := false
	progress := recipeContainerProgress{}
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
			progress = recipeContainerProgress{Phase: "installing-runtime", Message: "Installing FlashInfer and DFlash startup dependencies…", CurrentItem: "FlashInfer / DFlash", OverallPercent: 53}
			found = true
		}
		if strings.Contains(line, "Successfully installed") {
			progress = recipeContainerProgress{Phase: "initializing-engine", Message: "Startup dependencies are installed. Initializing vLLM…", CurrentItem: "vLLM", OverallPercent: 54}
			found = true
		}
		if strings.Contains(line, "Initializing a V1 LLM engine") || strings.Contains(line, "Resolved architecture:") {
			progress = recipeContainerProgress{Phase: "initializing-engine", Message: "vLLM is resolving the model architecture and execution plan…", CurrentItem: "vLLM", OverallPercent: 55}
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
		if strings.Contains(lower, "torch.compile") || strings.Contains(lower, "compiling model") {
			progress = recipeContainerProgress{Phase: "compiling-kernels", Message: "Compiling optimized GPU kernels for this model and Spark…", CurrentItem: "GPU kernels", OverallPercent: 78}
			found = true
		}
		if strings.Contains(lower, "capturing cuda graph") || strings.Contains(lower, "graph capturing finished") || strings.Contains(lower, "warming up model") {
			progress = recipeContainerProgress{Phase: "warming-engine", Message: "Warming the engine and capturing reusable CUDA graphs…", CurrentItem: "CUDA graphs", OverallPercent: 80}
			found = true
		}
		if strings.Contains(lower, "application startup complete") || strings.Contains(lower, "uvicorn running on") || strings.Contains(lower, "starting vllm api server") {
			progress = recipeContainerProgress{Phase: "health", Message: "The model server has started. Verifying its health and Cloudless model identity…", CurrentItem: "Health contract", OverallPercent: 82}
			found = true
		}
	}
	return progress, found
}

// observeRecipeContainerStartup reads captured container output without
// entering or mutating the container. Docker health remains authoritative;
// log parsing only explains the work occurring before the health contract is
// ready.
func observeRecipeContainerStartup(ctx context.Context, runtime engine.Engine, job *jobs.Job, containerName string, interval time.Duration) func() {
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
		var previous recipeContainerProgress
		poll := func() {
			logContext, stop := context.WithTimeout(observerContext, 5*time.Second)
			logs, err := runtime.Logs(logContext, containerName)
			stop()
			if err != nil {
				return
			}
			progress, ok := parseRecipeContainerProgress(logs)
			if !ok || progress == previous {
				return
			}
			previous = progress
			job.ProgressOperationETA(progress.Phase, progress.Message, progress.CurrentItem, progress.OverallPercent, progress.ItemsDone, progress.ItemsTotal, progress.ETASeconds)
			if progress.BytesTotal > 0 {
				job.ProgressBytesDetail(progress.Phase, progress.Message, progress.BytesDone, progress.BytesTotal)
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
