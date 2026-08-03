package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestRecipeContainerProgressReportsDependencyInstall(t *testing.T) {
	logs := strings.Join([]string{
		"Collecting flashinfer-jit-cache==0.6.15.dev20260712+cu130",
		"Downloading flashinfer_jit_cache-0.6.15.dev20260712+cu130-cp39-abi3-manylinux_2_28_aarch64.whl (1816.3 MB)",
		"1.2/1.8 GB 13.3 MB/s 0:01:40",
	}, "\n")
	progress, ok := parseRecipeContainerProgress(logs)
	if !ok || progress.Phase != "downloading-dependencies" || progress.OverallPercent != 52 || progress.CurrentItem != "flashinfer-jit-cache" || progress.BytesDone != 1_200_000_000 || progress.BytesTotal != 1_800_000_000 {
		t.Fatalf("progress = %#v, ok=%v", progress, ok)
	}
	if !strings.Contains(progress.Message, "66%") {
		t.Fatalf("dependency percentage is missing: %s", progress.Message)
	}
}

func TestRecipeDependencyProgressDoesNotReusePreviousWheelBytes(t *testing.T) {
	logs := "Downloading small-1.0.whl (465.7 MB)\n465.7/465.7 MB\nDownloading large-1.0.whl (1816.3 MB)"
	progress, ok := parseRecipeContainerProgress(logs)
	if !ok || progress.CurrentItem != "large" || progress.BytesDone != 0 || progress.BytesTotal != 1_816_300_000 {
		t.Fatalf("latest dependency progress = %#v, ok=%v", progress, ok)
	}
	if len(progress.Components) != 2 || progress.Components[0].Status != "complete" || progress.Components[1].Status != "downloading" {
		t.Fatalf("dependency components = %#v", progress.Components)
	}
}

func TestDependencyTransferTrackerReportsRateETAAndStall(t *testing.T) {
	start := time.Unix(100, 0)
	var tracker dependencyTransferTracker
	tracker.update(start, "wheel", 1_000, 0, 10_000)
	done, rate, stalled, eta := tracker.update(start.Add(2*time.Second), "wheel", 5_000, 0, 10_000)
	if done != 4_000 || rate != 2_000 || stalled != 0 || eta != 3 {
		t.Fatalf("moving transfer = done %d rate %d stalled %d eta %d", done, rate, stalled, eta)
	}
	_, _, stalled, _ = tracker.update(start.Add(80*time.Second), "wheel", 5_000, 0, 10_000)
	if stalled < 75 {
		t.Fatalf("stalled seconds = %d", stalled)
	}
}

func TestRecipeContainerProgressReportsCheckpointShardsAndETA(t *testing.T) {
	logs := strings.Join([]string{
		"(EngineCore pid=301) Starting to load model poolside/Laguna-S-2.1-NVFP4...",
		"(EngineCore pid=301) Checkpoint size: 92.85 GiB. Available RAM: 17.97 GiB.",
		"Auto-prefetch is disabled because the checkpoint exceeds 90% of available RAM.",
		"\x1b[32mLoading safetensors checkpoint shards:  22% Completed | 11/49 [04:29<18:47, 29.67s/it]\x1b[0m",
	}, "\r")
	progress, ok := parseRecipeContainerProgress(logs)
	if !ok || progress.Phase != "loading-model" || progress.ItemsDone != 11 || progress.ItemsTotal != 49 || progress.ETASeconds != 18*60+47 {
		t.Fatalf("progress = %#v, ok=%v", progress, ok)
	}
	if progress.OverallPercent != 59 || !strings.Contains(progress.Message, "92.85 GiB") || !strings.Contains(progress.Message, "22%") || !strings.Contains(progress.Message, "17.97 GiB RAM") {
		t.Fatalf("checkpoint detail is incomplete: %#v", progress)
	}
}

func TestRecipeContainerProgressDistinguishesDFlashAndWarmup(t *testing.T) {
	draft, ok := parseRecipeContainerProgress("Starting to load model poolside/Laguna-S-2.1-DFlash-NVFP4...\nLoading safetensors checkpoint shards: 60% Completed | 3/5 [00:30<00:20, 10s/it]")
	if !ok || draft.Phase != "loading-draft" || draft.OverallPercent != 75 || draft.ETASeconds != 20 {
		t.Fatalf("draft progress = %#v, ok=%v", draft, ok)
	}
	warmup, ok := parseRecipeContainerProgress("Capturing CUDA graphs (mixed prefill-decode, PIECEWISE): 40%")
	if !ok || warmup.Phase != "warming-engine" || warmup.OverallPercent != 80 {
		t.Fatalf("warmup progress = %#v, ok=%v", warmup, ok)
	}
}

type recipeProgressLogEngine struct {
	engine.Engine
	logs     string
	received int64
}

func (e recipeProgressLogEngine) Logs(context.Context, string) (string, error) {
	return e.logs, nil
}

func (e recipeProgressLogEngine) ContainerIO(context.Context, string) (engine.ContainerIO, error) {
	return engine.ContainerIO{ReceivedBytes: e.received}, nil
}

func TestObserveDependencyPublishesStructuredComponents(t *testing.T) {
	job := jobs.NewManager().Create("recipe:test:run")
	runtime := recipeProgressLogEngine{logs: "Downloading flashinfer_jit_cache-1.0.whl (100.0 MB)", received: 20_000_000}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := observeRecipeContainerStartup(ctx, runtime, job, "cloudless-recipe-test", time.Millisecond)
	defer stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := job.Snapshot()
		if snapshot.Phase == "downloading-dependencies" && len(snapshot.Components) == 1 {
			if snapshot.Components[0].Name != "flashinfer-jit-cache" || snapshot.BytesTotal != 100_000_000 {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("observer did not publish structured dependency progress: %#v", job.Snapshot())
}

func TestObserveRecipeContainerStartupPublishesParsedProgress(t *testing.T) {
	job := jobs.NewManager().Create("recipe:test:run")
	runtime := recipeProgressLogEngine{logs: "Starting to load model example/main...\nLoading safetensors checkpoint shards: 50% Completed | 4/8 [00:10<00:10, 2s/it]"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := observeRecipeContainerStartup(ctx, runtime, job, "cloudless-recipe-test", time.Millisecond)
	defer stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := job.Snapshot()
		if snapshot.Phase == "loading-model" {
			if snapshot.ItemsDone != 4 || snapshot.ItemsTotal != 8 || snapshot.ETASecs != 10 {
				t.Fatalf("published snapshot = %#v", snapshot)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("observer did not publish progress: %#v", job.Snapshot())
}

func TestManagedHealthPollingPreservesContainerProgress(t *testing.T) {
	recipe := localrecipes.Recipe{Health: localrecipes.Health{
		Scheme: "http", Host: "127.0.0.1", Port: 1, Path: "/health",
		TimeoutSeconds: 60, IntervalSeconds: 1,
	}}
	job := jobs.NewManager().Create("recipe:test")
	job.ProgressOperationETA("loading-model", "Loading main model — checkpoint shard 11 of 49 (22%)", "model", 59, 11, 49, 1127)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	err := waitRecipeHealthWithoutUpdates(ctx, job, recipe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("health wait error = %v", err)
	}
	snapshot := job.Snapshot()
	if snapshot.Phase != "loading-model" || snapshot.ItemsDone != 11 || snapshot.ItemsTotal != 49 || !strings.Contains(snapshot.Message, "11 of 49") {
		t.Fatalf("health polling overwrote parsed progress: %#v", snapshot)
	}
}

func TestOrdinaryHealthPollingStillReportsWaiting(t *testing.T) {
	recipe := localrecipes.Recipe{Health: localrecipes.Health{
		Scheme: "http", Host: "127.0.0.1", Port: 1, Path: "/health",
		TimeoutSeconds: 60, IntervalSeconds: 1,
	}}
	job := jobs.NewManager().Create("recipe:test")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	_ = waitRecipeHealth(ctx, job, recipe)
	if snapshot := job.Snapshot(); snapshot.Phase != "health" || !strings.Contains(snapshot.Message, "Waiting for engine health check") {
		t.Fatalf("ordinary health progress = %#v", snapshot)
	}
}
