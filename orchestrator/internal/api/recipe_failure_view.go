package api

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/cloudless/orchestrator/internal/recipeops"
)

type recipeFailureView struct {
	OperationID       string   `json:"operationId"`
	Phase             string   `json:"phase"`
	Category          string   `json:"category"`
	Summary           string   `json:"summary"`
	Detail            string   `json:"detail"`
	Retryable         bool     `json:"retryable"`
	Remedy            string   `json:"remedy"`
	AffectedNodes     []string `json:"affectedNodes,omitempty"`
	RetainedArtifacts []string `json:"retainedArtifacts,omitempty"`
	CleanupPending    bool     `json:"cleanupPending"`
	UpdatedAt         string   `json:"updatedAt"`
}

var recipeDiagnosticSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(hf_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|cfat_[A-Za-z0-9_-]{8,})\b`),
	regexp.MustCompile(`(?i)\b((?:token|password|passwd|secret|api[_-]?key|access[_-]?key)\s*[=:]\s*)[^\s,;]+`),
	regexp.MustCompile(`(?i)(https?://)[^/@\s:]+:[^/@\s]+@`),
}

func redactRecipeDiagnosticText(value string) string {
	return redactRecipeDiagnosticTextLimit(value, 4000)
}

func redactRecipeDiagnosticTextLimit(value string, limit int) string {
	value = strings.TrimSpace(value)
	for index, pattern := range recipeDiagnosticSecretPatterns {
		if index == 1 {
			value = pattern.ReplaceAllString(value, `${1}<redacted>`)
		} else if index == 2 {
			value = pattern.ReplaceAllString(value, `${1}<redacted>@`)
		} else {
			value = pattern.ReplaceAllString(value, `<redacted>`)
		}
	}
	if limit > 0 && len(value) > limit {
		value = value[:limit-3] + "..."
	}
	return value
}

func recipeFailureViews(operations []recipeops.Operation) map[string]recipeFailureView {
	latest := make(map[string]recipeops.Operation)
	for _, operation := range operations {
		if current, ok := latest[operation.RecipeID]; !ok || operation.UpdatedAt > current.UpdatedAt || (operation.UpdatedAt == current.UpdatedAt && operation.ID > current.ID) {
			latest[operation.RecipeID] = operation
		}
	}
	views := make(map[string]recipeFailureView, len(latest))
	for recipeID, operation := range latest {
		if operation.Phase == recipeops.PhaseFailed {
			views[recipeID] = buildRecipeFailureView(operation)
		}
	}
	return views
}

func buildRecipeFailureView(operation recipeops.Operation) recipeFailureView {
	detail := redactRecipeDiagnosticText(operation.Error)
	phase := recipeFailurePhase(operation)
	category, summary, retryable, remedy := classifyRecipeFailureView(phase, detail)
	nodeSet, artifactSet := make(map[string]struct{}), make(map[string]struct{})
	cleanupPending := false
	for _, resource := range operation.Resources {
		if resource.RequiresCleanup() {
			cleanupPending = true
			if node := strings.TrimSpace(resource.Node); node != "" {
				nodeSet[node] = struct{}{}
			}
			continue
		}
		switch resource.Kind {
		case "model-cache", "model-staging", "image", "checkout":
			artifactSet[resource.Kind+": "+resource.ID] = struct{}{}
		}
	}
	return recipeFailureView{
		OperationID: operation.ID, Phase: phase, Category: category, Summary: summary,
		Detail: detail, Retryable: retryable, Remedy: remedy,
		AffectedNodes: sortedRecipeFailureValues(nodeSet), RetainedArtifacts: sortedRecipeFailureValues(artifactSet),
		CleanupPending: cleanupPending, UpdatedAt: operation.UpdatedAt,
	}
}

func recipeFailurePhase(operation recipeops.Operation) string {
	if operation.Progress != nil && strings.TrimSpace(operation.Progress.Stage) != "" {
		return operation.Progress.Stage
	}
	for index := len(operation.Checkpoints) - 1; index >= 0; index-- {
		phase := operation.Checkpoints[index].Phase
		if phase != recipeops.PhaseFailed && phase != recipeops.PhaseAborted {
			return string(phase)
		}
	}
	return "unknown"
}

func classifyRecipeFailureView(phase, detail string) (category, summary string, retryable bool, remedy string) {
	message := strings.ToLower(detail)
	if classified, ok := classifyRecipeDependencyError(errors.New(detail)).(*recipeDependencyError); ok && classified.Kind != "failed" {
		return "dependency", "External model dependency " + classified.Kind, classified.Retryable, classified.Remedy
	}
	switch {
	case strings.Contains(phase, "source"):
		return "source", "Pinned recipe source could not be prepared", false, "Verify the immutable source revision and reviewed file checksums, then run Check again."
	case strings.Contains(phase, "image") || strings.Contains(phase, "build"):
		return "runtime-image", "Inference runtime preparation failed", true, "Review the pinned image/build output and available storage, then retry. Prepared model files remain cached."
	case strings.Contains(phase, "sync") || strings.Contains(phase, "transfer") || strings.Contains(message, "ssh"):
		return "cluster-transfer", "A selected Spark could not complete artifact transfer", true, "Open the Spark cluster dashboard, verify the affected node and fabric, then retry. Verified files are reused."
	case strings.Contains(phase, "port") || strings.Contains(message, "address already in use") || strings.Contains(message, "port") && strings.Contains(message, "occupied"):
		return "port-conflict", "A required private port is already owned", true, "Stop the reported process or choose another private recipe port, then run Check again. The stable Cloudless API port does not change."
	case strings.Contains(phase, "starting") || strings.Contains(phase, "health") || strings.Contains(message, "health check"):
		return "runtime-start", "The inference runtime did not become healthy", true, "Inspect the runtime command and diagnostic bundle. Correct its engine arguments or health route, then retry."
	case strings.Contains(phase, "connect") || strings.Contains(phase, "promot") || strings.Contains(message, "/v1/models"):
		return "api-contract", "The runtime failed the stable Cloudless API contract", false, "Ensure the private server binds to the configured host/port and exposes OpenAI-compatible /v1/models with the internal model name cloudless."
	case strings.Contains(message, "cleanup obligation") || strings.Contains(message, "cleanup"):
		return "cleanup", "Cloudless could not prove that every runtime resource stopped", true, "Use Retry cleanup. New launches remain blocked until every reachable Spark reports the resources absent."
	default:
		return "runtime", "The recipe operation failed", true, "Open the diagnostic details, correct the reported phase, and retry. Cloudless preserves verified artifacts whenever safe."
	}
}

func sortedRecipeFailureValues(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
