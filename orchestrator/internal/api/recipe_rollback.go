package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/customengine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/state"
)

func inferenceRuntimeFromRecipeOperation(operation recipeops.Operation) state.InferenceRuntime {
	if operation.PreviousRuntime == nil {
		return state.InferenceRuntime{EngineUnloaded: true}
	}
	previous := operation.PreviousRuntime
	return state.InferenceRuntime{
		Engine: previous.Engine, Model: previous.Model, EngineUnloaded: previous.EngineUnloaded,
		ExecutionMode: previous.ExecutionMode, LocalRecipeID: previous.LocalRecipeID,
	}
}

// cleanFailedRecipeRuntime is the single compensating cleanup path for every
// error after the previous engine has been stopped.
func (s *Server) cleanFailedRecipeRuntime(job *jobs.Job, recipe localrecipes.Recipe, workdir string, env map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	job.Progress("rolling-back", "Stopping the failed recipe runtime before rollback...", -1, -1)
	var proxyErr error
	if proxy, findErr := s.eng.Find(ctx, "cloudless-cluster-engine-proxy"); findErr != nil {
		proxyErr = findErr
	} else if proxy != nil {
		proxyErr = s.eng.Remove(ctx, proxy.Name)
	}
	return errors.Join(
		proxyErr,
		runConfiguredRecipeCommand(ctx, job, "rolling-back", "Stop failed recipe runtime", workdir, env, recipe, recipe.Runtime.Lifecycle.Stop),
		s.reconcileActiveRecipeResources(ctx, recipe),
	)
}

// restorePreviousInferenceRuntime is called without EngineMu held because the
// normal managed-engine launcher owns that lock. It restores persisted state
// first so a reboot during rollback deterministically retries the old target.
func (s *Server) restorePreviousInferenceRuntime(job *jobs.Job, previous state.InferenceRuntime) error {
	if err := s.state.CommitInferenceRuntime(previous); err != nil {
		return fmt.Errorf("restore previous inference state: %w", err)
	}
	if previous.EngineUnloaded {
		job.Progress("rolled-back", "The previous state was unloaded; Cloudless is safely unloaded again.", -1, -1)
		return nil
	}
	if previous.LocalRecipeID != "" {
		return errors.New("the previous runtime was another local recipe and requires recovery before it can be restarted")
	}
	engineID := previous.Engine
	if engineID == "" {
		engineID = catalog.DefaultEngine()
	}
	target, ok := customengine.Get(s.state, engineID)
	if !ok || !target.Engine {
		return fmt.Errorf("previous inference engine %q is unavailable", engineID)
	}
	job.Progress("rolling-back", "Restarting the previous healthy engine...", -1, -1)
	rollbackJob := jobs.NewManager().Create("engine:recipe-rollback")
	rollbackJob.Observe(func(update jobs.Update) {
		if !update.Done && update.Message != "" {
			job.Progress("rolling-back", update.Message, update.ItemsDone, update.ItemsTotal)
		}
	})
	s.applyEngine(rollbackJob, target)
	result := rollbackJob.Snapshot()
	if result.Error != "" {
		return fmt.Errorf("restart previous inference engine: %s", result.Error)
	}
	if !result.Done {
		return errors.New("previous inference engine rollback did not finish")
	}
	// applyEngine may normalize engine selection; restore the exact durable
	// snapshot only after its stable endpoint is healthy.
	if err := s.state.CommitInferenceRuntime(previous); err != nil {
		return fmt.Errorf("finalize previous inference state: %w", err)
	}
	job.Progress("rolled-back", "The previous Cloudless model is available again.", -1, -1)
	return nil
}

// restorePreviousOrMarkUnloaded enforces the final rollback invariant. If the
// exact previous runtime cannot be made healthy again, Cloudless must persist
// that same selection as explicitly unloaded. A failure to persist the safe
// state is returned rather than hidden, so recovery keeps the operation and
// its cleanup obligations visible.
func (s *Server) restorePreviousOrMarkUnloaded(job *jobs.Job, previous state.InferenceRuntime) error {
	restoreErr := s.restorePreviousInferenceRuntime(job, previous)
	if restoreErr == nil {
		return nil
	}
	safe := previous
	safe.EngineUnloaded = true
	safeErr := s.state.CommitInferenceRuntime(safe)
	if safeErr != nil {
		safeErr = fmt.Errorf("mark previous inference selection safely unloaded: %w", safeErr)
	}
	return errors.Join(restoreErr, safeErr)
}

func (s *Server) rollbackFailedRecipe(job *jobs.Job, operation recipeops.Operation, recipe localrecipes.Recipe, workdir string, env map[string]string, cause error) error {
	cleanupErr := s.cleanFailedRecipeRuntime(job, recipe, workdir, env)
	previous := inferenceRuntimeFromRecipeOperation(operation)
	rollbackErr := s.restorePreviousOrMarkUnloaded(job, previous)
	return errors.Join(cause, cleanupErr, rollbackErr)
}
