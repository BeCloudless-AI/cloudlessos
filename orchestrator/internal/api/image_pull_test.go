package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

type resilientPullEngine struct {
	engine.Engine
	mu       sync.Mutex
	attempts int
	pull     func(context.Context, int, func(string)) error
}

func (e *resilientPullEngine) PullStream(ctx context.Context, _ string, onLine func(string)) error {
	e.mu.Lock()
	e.attempts++
	attempt := e.attempts
	e.mu.Unlock()
	return e.pull(ctx, attempt, onLine)
}

func (e *resilientPullEngine) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attempts
}

func TestResilientImagePullRetriesAStalledDockerProcess(t *testing.T) {
	runtime := &resilientPullEngine{}
	runtime.pull = func(ctx context.Context, attempt int, onLine func(string)) error {
		if attempt == 1 {
			onLine("58aacae73b54: Retrying in 1 second")
			<-ctx.Done()
			return ctx.Err()
		}
		onLine("58aacae73b54: Pull complete")
		return nil
	}
	var lines []string
	err := pullImageStreamWithPolicy(context.Background(), runtime, "cloudless/runtime:test", imagePullPolicy{
		stallTimeout: 20 * time.Millisecond,
		attempts:     2,
	}, func(line string) { lines = append(lines, line) })
	if err != nil {
		t.Fatal(err)
	}
	if runtime.count() != 2 {
		t.Fatalf("pull attempts = %d, want 2", runtime.count())
	}
	if !strings.Contains(strings.Join(lines, "\n"), "registry stopped sending data") {
		t.Fatalf("retry reason was not surfaced: %#v", lines)
	}
}

func TestResilientImagePullRetriesUnexpectedEOF(t *testing.T) {
	runtime := &resilientPullEngine{}
	runtime.pull = func(_ context.Context, attempt int, onLine func(string)) error {
		if attempt == 1 {
			onLine("Download failed: unexpected EOF")
			return errors.New("exit status 1")
		}
		return nil
	}
	err := pullImageStreamWithPolicy(context.Background(), runtime, "cloudless/runtime:test", imagePullPolicy{
		stallTimeout: time.Second,
		attempts:     2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.count() != 2 {
		t.Fatalf("pull attempts = %d, want 2", runtime.count())
	}
}

func TestResilientImagePullDoesNotRetryPermanentRegistryFailure(t *testing.T) {
	runtime := &resilientPullEngine{pull: func(context.Context, int, func(string)) error {
		return errors.New("unauthorized: authentication required")
	}}
	err := pullImageStreamWithPolicy(context.Background(), runtime, "cloudless/runtime:test", imagePullPolicy{
		stallTimeout: time.Second,
		attempts:     3,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("error = %v", err)
	}
	if runtime.count() != 1 {
		t.Fatalf("permanent failure attempts = %d, want 1", runtime.count())
	}
}

func TestResilientImagePullKeepsParentCancellationAuthoritative(t *testing.T) {
	runtime := &resilientPullEngine{pull: func(ctx context.Context, _ int, _ func(string)) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := pullImageStreamWithPolicy(ctx, runtime, "cloudless/runtime:test", imagePullPolicy{
		stallTimeout: time.Second,
		attempts:     3,
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if runtime.count() > 1 {
		t.Fatalf("canceled pull attempts = %d, want at most 1", runtime.count())
	}
}

func TestRecipeImageProgressUsesMeasuredManifestSize(t *testing.T) {
	operation := recipeops.Operation{Preflight: &recipeops.PreflightArtifact{Checks: []recipeops.CheckResult{
		{ID: "capacity", Values: map[string]string{"compressedBytes": "1"}},
		{ID: "image", Values: map[string]string{"compressedBytes": "9659938060"}},
	}}}
	if got := recipePreflightImageCompressedBytes(operation); got != 9659938060 {
		t.Fatalf("compressed image bytes = %d", got)
	}
	operation.Preflight.Checks[1].Values["compressedBytes"] = "invalid"
	if got := recipePreflightImageCompressedBytes(operation); got != 0 {
		t.Fatalf("invalid compressed image bytes = %d", got)
	}
}
