package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
)

const (
	defaultImagePullStallTimeout = 5 * time.Minute
	defaultImagePullAttempts     = 3
)

type imagePullPolicy struct {
	stallTimeout time.Duration
	attempts     int
	retryDelay   time.Duration
}

var defaultImagePullPolicy = imagePullPolicy{
	stallTimeout: defaultImagePullStallTimeout,
	attempts:     defaultImagePullAttempts,
	retryDelay:   2 * time.Second,
}

// pullImageStreamResilient prevents a broken registry transfer from leaving a
// Cloudless operation alive indefinitely. Docker already retries individual
// HTTP requests, but it can occasionally keep the pull process open without
// producing output after a truncated layer. A bounded, whole-pull retry lets
// Docker reuse content it committed successfully while preserving the parent
// operation's timeout and cancellation boundary.
func pullImageStreamResilient(ctx context.Context, runtime engine.Engine, image string, onLine func(string)) error {
	return pullImageStreamWithPolicy(ctx, runtime, image, defaultImagePullPolicy, onLine)
}

func pullImageStreamWithPolicy(ctx context.Context, runtime engine.Engine, image string, policy imagePullPolicy, onLine func(string)) error {
	if policy.stallTimeout <= 0 {
		return errors.New("image pull stall timeout must be positive")
	}
	if policy.attempts <= 0 {
		return errors.New("image pull attempts must be positive")
	}

	var lastErr error
	for attempt := 1; attempt <= policy.attempts; attempt++ {
		attemptCtx, cancel := context.WithCancel(ctx)
		activity := make(chan struct{}, 1)
		result := make(chan error, 1)
		var lastLineMu sync.Mutex
		lastLine := ""

		go func() {
			result <- runtime.PullStream(attemptCtx, image, func(line string) {
				lastLineMu.Lock()
				lastLine = line
				lastLineMu.Unlock()
				select {
				case activity <- struct{}{}:
				default:
				}
				if onLine != nil {
					onLine(line)
				}
			})
		}()

		timer := time.NewTimer(policy.stallTimeout)
		stalled := false
	waitAttempt:
		for {
			select {
			case err := <-result:
				lastErr = err
				break waitAttempt
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(policy.stallTimeout)
			case <-timer.C:
				stalled = true
				cancel()
				lastErr = fmt.Errorf("image download stalled: no registry progress for %s", policy.stallTimeout.Round(time.Second))
				// CommandContext and the broker protocol both honor cancellation.
				// Wait briefly so a failed attempt cannot overlap its replacement.
				select {
				case <-result:
				case <-time.After(10 * time.Second):
					lastErr = fmt.Errorf("%w and the container runtime did not stop the pull", lastErr)
				}
				break waitAttempt
			case <-ctx.Done():
				cancel()
				timer.Stop()
				return ctx.Err()
			}
		}
		timer.Stop()
		cancel()

		if lastErr == nil {
			return nil
		}
		lastLineMu.Lock()
		line := lastLine
		lastLineMu.Unlock()
		if attempt == policy.attempts || (!stalled && !transientImagePullFailure(lastErr, line)) {
			if stalled {
				return fmt.Errorf("pull %s failed after %d stalled attempts: %w", image, attempt, lastErr)
			}
			return lastErr
		}

		if onLine != nil {
			reason := "the registry connection was interrupted"
			if stalled {
				reason = "the registry stopped sending data"
			}
			onLine(fmt.Sprintf("Cloudless: %s; retrying image download (%d/%d)", reason, attempt+1, policy.attempts))
		}
		if policy.retryDelay > 0 {
			select {
			case <-time.After(policy.retryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}

func transientImagePullFailure(err error, lastLine string) bool {
	text := strings.ToLower(lastLine + " " + err.Error())
	for _, marker := range []string{
		"unexpected eof", "connection reset", "connection refused", "tls handshake timeout",
		"i/o timeout", "context deadline exceeded", "temporary failure", "service unavailable",
		"bad gateway", "gateway timeout", "retrying in", "eof",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
