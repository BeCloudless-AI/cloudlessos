package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type recipeDependencyError struct {
	Kind      string
	Retryable bool
	Remedy    string
	Cause     error
}

func (e *recipeDependencyError) Error() string {
	return fmt.Sprintf("model download %s: %v. %s", e.Kind, e.Cause, e.Remedy)
}

func (e *recipeDependencyError) Unwrap() error { return e.Cause }

func classifyRecipeDependencyError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	classified := &recipeDependencyError{Kind: "failed", Cause: err, Remedy: "Check the detailed operation log and try again."}
	switch {
	case strings.Contains(message, "401"), strings.Contains(message, "403"), strings.Contains(message, "unauthorized"), strings.Contains(message, "forbidden"), strings.Contains(message, "gated repo"), strings.Contains(message, "invalid token"):
		classified.Kind = "authentication failed"
		classified.Remedy = "Connect a Hugging Face account that can access this model, accept any gated-model license, then retry."
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"), strings.Contains(message, "too many requests"):
		classified.Kind = "was rate-limited"
		classified.Retryable = true
		classified.Remedy = "Cloudless preserved downloaded chunks. Wait for the Hugging Face limit to reset, then retry."
	case strings.Contains(message, "404"), strings.Contains(message, "repository not found"), strings.Contains(message, "revision not found"), strings.Contains(message, "entry not found"):
		classified.Kind = "artifact was not found"
		classified.Remedy = "Verify the model ID and immutable revision in the recipe."
	case strings.Contains(message, "no space left"), strings.Contains(message, "disk quota"):
		classified.Kind = "ran out of storage"
		classified.Remedy = "Free storage or choose another cache location. The active model cache was not replaced."
	case strings.Contains(message, "checksum"), strings.Contains(message, "hash mismatch"), strings.Contains(message, "incomplete pinned snapshot"), strings.Contains(message, "integrity"):
		classified.Kind = "failed integrity verification"
		classified.Retryable = true
		classified.Remedy = "Cloudless rejected the incomplete or corrupt artifact and retained verified data for a clean retry."
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"), strings.Contains(message, "temporary failure"), strings.Contains(message, "connection reset"), strings.Contains(message, "network is unreachable"), strings.Contains(message, "name or service not known"):
		classified.Kind = "was interrupted by the network"
		classified.Retryable = true
		classified.Remedy = "Cloudless preserved partial chunks. Restore connectivity and retry to resume."
	}
	return classified
}
