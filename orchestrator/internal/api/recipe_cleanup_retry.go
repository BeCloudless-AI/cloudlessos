package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func operationCleanupResources(operation recipeops.Operation) []recipeops.Resource {
	resources := make([]recipeops.Resource, 0)
	for _, resource := range operation.Resources {
		if resource.RequiresCleanup() {
			resources = append(resources, resource)
		}
	}
	return resources
}

func (s *Server) localRecipeCleanupRetry(w http.ResponseWriter, r *http.Request) {
	if s.recipeOpsErr != nil || s.recipeOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recipe operation journal is unavailable"})
		return
	}
	operationID := strings.TrimSpace(r.PathValue("operation"))
	operation, ok := s.recipeOps.Get(operationID)
	if !ok || operation.RecipeID != r.PathValue("id") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "recipe cleanup operation not found"})
		return
	}
	if operation.Phase != recipeops.PhaseFailed && operation.Phase != recipeops.PhaseAborted && operation.Phase != recipeops.PhaseStopped {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this recipe operation is not waiting for cleanup"})
		return
	}
	if len(operationCleanupResources(operation)) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this recipe operation has no unresolved runtime resources"})
		return
	}
	if _, err := s.recipeOps.Transition(operation.ID, recipeops.PhaseRecovering, nil); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not begin cleanup retry: " + err.Error()})
		return
	}
	job := s.jobs.Create("recipe:" + operation.RecipeID + ":cleanup")
	s.observeRecipeJob(job, operation.ID)
	go s.retryRecipeCleanup(job, operation)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "operationId": operation.ID, "recipe": operation.RecipeID})
}

func (s *Server) retryRecipeCleanup(job *jobs.Job, original recipeops.Operation) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	job.Progress("recovering", "Stopping recipe resources on every last-known Spark...", 0, 3)
	cleanupErr := s.cleanupInterruptedRecipe(ctx, job, original, original.RecipeSnapshot)
	job.Progress("verifying-cleanup", "Checking every journaled container, process, and port...", 2, 3)
	remaining, inventoryErr := s.releaseMissingRecipeResources(ctx, original.ID, original.RecipeSnapshot)
	if len(remaining) > 0 {
		inventoryErr = errors.Join(inventoryErr, fmt.Errorf("%d cleanup obligation(s) remain on %s", len(remaining), cleanupResourceNodes(remaining)))
	}
	resultErr := errors.Join(cleanupErr, inventoryErr)
	if resultErr != nil {
		_, transitionErr := s.recipeOps.Transition(original.ID, recipeops.PhaseFailed, resultErr)
		job.Fail(errors.Join(resultErr, transitionErr))
		return
	}
	target := original.Phase
	if target != recipeops.PhaseFailed && target != recipeops.PhaseAborted && target != recipeops.PhaseStopped {
		target = recipeops.PhaseFailed
	}
	var originalErr error
	if target == recipeops.PhaseFailed {
		message := strings.TrimSpace(original.Error)
		if message == "" {
			message = "the recipe operation failed before cleanup completed"
		}
		originalErr = errors.New(message)
	}
	if _, err := s.recipeOps.Transition(original.ID, target, originalErr); err != nil {
		job.Fail(fmt.Errorf("persist verified recipe cleanup: %w", err))
		return
	}
	job.Progress("cleanup-complete", "No recipe-owned runtime resources remain on reachable Sparks.", 3, 3)
	job.Succeed("cleanup-complete")
	if _, err := s.finalizeRecipeTombstone(original.RecipeID); err != nil {
		job.Fail(fmt.Errorf("cleanup succeeded but recipe removal could not be finalized: %w", err))
		return
	}
	s.pruneRecipeOperations()
}

func cleanupResourceNodes(resources []recipeops.Resource) string {
	seen := make(map[string]struct{})
	nodes := make([]string, 0)
	for _, resource := range resources {
		node := strings.TrimSpace(resource.Node)
		if node == "" {
			node = localRecipeNodeName()
		}
		if _, ok := seen[node]; ok {
			continue
		}
		seen[node] = struct{}{}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return "unknown nodes"
	}
	return strings.Join(nodes, ", ")
}
