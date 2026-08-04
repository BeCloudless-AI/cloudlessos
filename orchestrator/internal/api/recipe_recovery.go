package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

type interruptedRecipeOperation struct {
	operation     recipeops.Operation
	originalPhase recipeops.Phase
}

type recipeRecoveryAction string

var recipeRecoveryEngineProbeInterval = 500 * time.Millisecond

const (
	recipeRecoveryReconcile   recipeRecoveryAction = "reconcile"
	recipeRecoveryResumeCheck recipeRecoveryAction = "resume-check"
	recipeRecoveryResumeRun   recipeRecoveryAction = "resume-run"
	recipeRecoveryRollback    recipeRecoveryAction = "rollback"
)

func recipeRecoveryActionFor(kind recipeops.Kind, phase recipeops.Phase) recipeRecoveryAction {
	switch {
	case kind == recipeops.KindCheck && phase == recipeops.PhaseChecking:
		return recipeRecoveryResumeCheck
	case kind == recipeops.KindRun && (phase == recipeops.PhasePreparing || phase == recipeops.PhasePrepared):
		return recipeRecoveryResumeRun
	case kind == recipeops.KindRun && (phase == recipeops.PhaseSwitching || phase == recipeops.PhaseStarting || phase == recipeops.PhaseVerifying):
		return recipeRecoveryRollback
	default:
		return recipeRecoveryReconcile
	}
}

// recipeInterruptedPhase survives a second daemon restart while the operation
// is already marked recovering. The preceding durable checkpoint is the phase
// whose recovery policy must be repeated.
func recipeInterruptedPhase(operation recipeops.Operation) recipeops.Phase {
	if operation.Phase != recipeops.PhaseRecovering {
		return operation.Phase
	}
	for index := len(operation.Checkpoints) - 1; index >= 0; index-- {
		if operation.Checkpoints[index].Phase != recipeops.PhaseRecovering {
			return operation.Checkpoints[index].Phase
		}
	}
	return recipeops.PhaseRecovering
}

// startRecipeRecovery changes every interrupted operation to recovering before
// the API can admit new work, then reconciles real resources asynchronously.
// This prevents a daemon restart from leaving a stale "starting" state or from
// racing a new recipe against containers owned by the previous process.
func (s *Server) startRecipeRecovery() {
	if s.recipeOps == nil || s.recipeOpsErr != nil {
		return
	}
	interrupted := make([]interruptedRecipeOperation, 0)
	for _, operation := range s.recipeOps.List() {
		if !operation.NeedsRecovery() {
			continue
		}
		original := recipeInterruptedPhase(operation)
		if operation.Phase != recipeops.PhaseRecovering {
			var err error
			operation, err = s.recipeOps.Transition(operation.ID, recipeops.PhaseRecovering,
				fmt.Errorf("cloudlessd restarted while operation was %s", original))
			if err != nil {
				log.Printf("[recipe-recovery] mark %s recovering: %v", operation.ID, err)
				continue
			}
		}
		interrupted = append(interrupted, interruptedRecipeOperation{operation: operation, originalPhase: original})
	}
	if len(interrupted) == 0 {
		s.finalizeRecipeTombstones()
		return
	}
	go s.recoverRecipeOperations(interrupted)
}

func (s *Server) finalizeRecipeTombstones() {
	if s.recipeOps == nil {
		return
	}
	for _, id := range s.recipeOps.Tombstones() {
		if _, err := s.finalizeRecipeTombstone(id); err != nil {
			log.Printf("[recipe-tombstone] finalize %s: %v", id, err)
		}
	}
}

func (s *Server) recoverRecipeOperations(interrupted []interruptedRecipeOperation) {
	for _, item := range interrupted {
		timeout := 3 * time.Minute
		if item.originalPhase == recipeops.PhaseActive {
			timeout = 30 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := s.recoverRecipeOperation(ctx, item)
		cancel()
		if err != nil {
			log.Printf("[recipe-recovery] %s: %v", item.operation.ID, err)
		}
	}
	s.finalizeRecipeTombstones()
}

func (s *Server) recoverRecipeOperation(ctx context.Context, item interruptedRecipeOperation) error {
	operation := item.operation
	recipe, err := s.recoveryRecipeSnapshot(operation)
	if err != nil {
		_, transitionErr := s.recipeOps.Transition(operation.ID, recipeops.PhaseFailed, err)
		return errors.Join(err, transitionErr)
	}

	job := s.recoveryJob(operation)
	if job != nil {
		job.Progress("recovering", "Waiting for the container engine before restoring the recipe...", 0, 1)
	}
	if err := waitForRecipeRecoveryEngine(ctx, s.eng); err != nil {
		if job != nil {
			job.Fail(err)
		}
		// Keep the durable operation in recovering. Marking it failed or trying
		// cleanup while the broker is unavailable destroys the exact runtime
		// identity that a later daemon restart can still restore safely.
		return err
	}
	action := recipeRecoveryActionFor(operation.Kind, item.originalPhase)
	if action == recipeRecoveryResumeCheck || action == recipeRecoveryResumeRun {
		if job != nil {
			job.Progress("recovering", "Verifying interrupted preparation before resuming it...", 0, 1)
		}
		if err := s.reconcileRecipePreparationForResume(ctx, operation, recipe); err != nil {
			return s.failRecipeRecovery(job, operation.ID, fmt.Errorf("interrupted preparation could not be resumed safely: %w", err))
		}
		if action == recipeRecoveryResumeCheck {
			if _, err := s.recipeOps.Transition(operation.ID, recipeops.PhaseChecking, nil); err != nil {
				return s.failRecipeRecovery(job, operation.ID, fmt.Errorf("resume recipe Check: %w", err))
			}
			if job != nil {
				job.Progress("resuming", "Resuming the interrupted recipe Check...", 0, 10)
			}
			s.checkLocalRecipe(job, recipe, operation.ID)
			return nil
		}
		if _, err := s.recipeOps.Transition(operation.ID, recipeops.PhasePreparing, nil); err != nil {
			return s.failRecipeRecovery(job, operation.ID, fmt.Errorf("resume recipe preparation: %w", err))
		}
		if job != nil {
			job.Progress("resuming", "Resuming preparation from verified caches and staging data...", 0, 8)
		}
		s.runLocalRecipe(job, recipe, operation.ID)
		return nil
	}
	// A previously active runtime may already be completely healthy. Preserve it
	// only when both durable Cloudless state and the stable API agree.
	if operation.Kind == recipeops.KindRun && item.originalPhase == recipeops.PhaseActive {
		state := s.state.Get()
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		probeErr := engineEndpointError(probeCtx)
		cancel()
		if state.LocalRecipeID == operation.RecipeID && !state.EngineUnloaded && probeErr == nil {
			_, err := s.recipeOps.Transition(operation.ID, recipeops.PhaseActive, nil)
			if job != nil && err == nil {
				job.Succeed("active")
			}
			return err
		}
		if state.LocalRecipeID == operation.RecipeID && !state.EngineUnloaded {
			if restartErr := s.restartActiveRecipeAfterBoot(ctx, job, operation, recipe); restartErr == nil {
				_, transitionErr := s.recipeOps.Transition(operation.ID, recipeops.PhaseActive, nil)
				if job != nil {
					if transitionErr == nil {
						job.Succeed("active")
					} else {
						job.Fail(transitionErr)
					}
				}
				return transitionErr
			} else {
				log.Printf("[recipe-recovery] coordinated restart %s failed: %v", operation.ID, restartErr)
			}
		}
	}

	if job != nil {
		job.Progress("recovering", "Reconciling resources left by an interrupted recipe operation...", 0, 1)
	}
	actionErr := s.cleanupInterruptedRecipe(ctx, job, operation, recipe)
	remaining, inventoryErr := s.releaseMissingRecipeResources(ctx, operation.ID, recipe)
	cleanupErr := inventoryErr
	if len(remaining) > 0 {
		cleanupErr = errors.Join(actionErr, cleanupErr, fmt.Errorf("%d runtime cleanup obligation(s) remain", len(remaining)))
	} else if actionErr != nil {
		// Stop hooks are best-effort during reconciliation. The authoritative
		// inventory proved all ephemeral claims absent, so a non-idempotent hook
		// must not leave the operation blocked forever.
		log.Printf("[recipe-recovery] %s cleanup command reported an error after resources were absent: %v", operation.ID, actionErr)
	}

	if cleanupErr == nil && action == recipeRecoveryRollback {
		previous := inferenceRuntimeFromRecipeOperation(operation)
		rollbackJob := job
		if rollbackJob == nil {
			rollbackJob = jobs.NewManager().Create("recipe:" + operation.RecipeID + ":recovery")
		}
		if rollbackErr := s.restorePreviousInferenceRuntime(rollbackJob, previous); rollbackErr != nil {
			safe := previous
			safe.EngineUnloaded = true
			stateErr := s.state.CommitInferenceRuntime(safe)
			cleanupErr = errors.Join(cleanupErr, rollbackErr, stateErr)
		}
	} else if state := s.state.Get(); state.LocalRecipeID == operation.RecipeID {
		runtime := state.InferenceRuntime()
		runtime.LocalRecipeID = ""
		runtime.EngineUnloaded = true
		_ = s.state.CommitInferenceRuntime(runtime)
	}
	final := recipeops.PhaseFailed
	if cleanupErr == nil && operation.Kind == recipeops.KindCheck {
		final = recipeops.PhaseAborted
	} else if cleanupErr == nil && (item.originalPhase == recipeops.PhaseStopping || item.originalPhase == recipeops.PhaseStopped) {
		final = recipeops.PhaseStopped
	} else if cleanupErr == nil && item.originalPhase == recipeops.PhasePreparing {
		final = recipeops.PhaseAborted
	}
	cause := cleanupErr
	if cause == nil && final != recipeops.PhaseStopped {
		cause = fmt.Errorf("operation was safely stopped after cloudlessd restarted during %s", item.originalPhase)
	}
	_, transitionErr := s.recipeOps.Transition(operation.ID, final, cause)
	if job != nil {
		if transitionErr != nil || cleanupErr != nil {
			job.Fail(errors.Join(cleanupErr, transitionErr))
		} else if final == recipeops.PhaseStopped {
			job.Succeed("stopped")
		} else {
			job.Cancel()
		}
	}
	s.pruneRecipeOperations()
	if _, err := s.finalizeRecipeTombstone(operation.RecipeID); err != nil {
		return errors.Join(cleanupErr, transitionErr, fmt.Errorf("finalize recipe removal: %w", err))
	}
	return errors.Join(cleanupErr, transitionErr)
}

// waitForRecipeRecoveryEngine closes the systemd Type=simple readiness gap:
// cloudless-engine.service is considered started when its process exists, but
// its protected socket is created only after model-cache reconciliation. A
// cold boot must wait here rather than treating that brief gap as a permanent
// recipe failure and unloading the remembered runtime.
func waitForRecipeRecoveryEngine(ctx context.Context, runtime engine.Engine) error {
	if runtime == nil {
		return errors.New("container engine is unavailable during recipe recovery")
	}
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		lastErr = runtime.Available(probeCtx)
		cancel()
		if lastErr == nil {
			return nil
		}
		timer := time.NewTimer(recipeRecoveryEngineProbeInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for container engine before recipe recovery: %w (last probe: %v)", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

// reconcileRecipePreparationForResume removes only ephemeral resources owned
// by the interrupted attempt. In particular, it does not remove the stable
// proxy or stop the previously active engine, which must remain available
// throughout preparation. Model caches, staging volumes and immutable images
// are deliberately retained so downloads and distribution can resume.
func (s *Server) reconcileRecipePreparationForResume(ctx context.Context, operation recipeops.Operation, recipe localrecipes.Recipe) error {
	if current, ok := s.recipeOps.Get(operation.ID); ok {
		operation = current
	} else {
		return errors.New("recipe operation disappeared during preparation recovery")
	}
	var actionErr error
	if operation.Kind == recipeops.KindCheck {
		for _, resource := range operation.Resources {
			if resource.Kind == "staging-checkout" {
				actionErr = errors.Join(actionErr, s.removeRecoveryStagingCheckout(resource.ID))
			}
		}
	} else {
		if s.eng != nil {
			actionErr = errors.Join(actionErr, s.removeRecoveryContainers(ctx, operation))
		}
		actionErr = errors.Join(actionErr, s.cleanupInterruptedRecipePeers(ctx, operation, recipe))
		actionErr = errors.Join(actionErr, terminateRecipeProcesses(operation.ID))
	}

	remaining, inventoryErr := s.releaseMissingRecipeResources(ctx, operation.ID, recipe)
	var ownedRuntime []recipeops.Resource
	for _, resource := range remaining {
		switch resource.Kind {
		case "private-port", "stable-port", "rendezvous-port":
			// Port claims are prospective during preparation. Once operation-
			// labelled containers/processes are absent, a remaining listener can
			// belong to the still-active previous engine and is not a leak from
			// this attempt.
		default:
			ownedRuntime = append(ownedRuntime, resource)
		}
	}
	if len(ownedRuntime) > 0 {
		return errors.Join(actionErr, inventoryErr, fmt.Errorf("%d operation-owned runtime resource(s) remain: %v", len(ownedRuntime), ownedRuntime))
	}
	for _, resource := range remaining {
		switch resource.Kind {
		case "private-port", "stable-port", "rendezvous-port":
			if _, err := s.recipeOps.Release(operation.ID, resource); err != nil {
				inventoryErr = errors.Join(inventoryErr, err)
			}
		}
	}
	if actionErr != nil && inventoryErr == nil {
		log.Printf("[recipe-recovery] %s preparation cleanup reported an error after ownership was absent: %v", operation.ID, actionErr)
		actionErr = nil
	}
	return errors.Join(actionErr, inventoryErr)
}

func (s *Server) failRecipeRecovery(job *jobs.Job, operationID string, cause error) error {
	_, transitionErr := s.recipeOps.Transition(operationID, recipeops.PhaseFailed, cause)
	result := errors.Join(cause, transitionErr)
	if job != nil {
		job.Fail(result)
	}
	s.pruneRecipeOperations()
	return result
}

func (s *Server) recoveryRecipeSnapshot(operation recipeops.Operation) (localrecipes.Recipe, error) {
	if operation.RecipeSnapshot.ID != "" {
		return operation.RecipeSnapshot, nil
	}
	recipe, ok, err := s.recipes.GetAny(operation.RecipeID)
	if err != nil {
		return localrecipes.Recipe{}, err
	}
	if !ok {
		return localrecipes.Recipe{}, errors.New("recipe snapshot and current recipe are unavailable")
	}
	return recipe, nil
}

func (s *Server) recoveryJob(operation recipeops.Operation) *jobs.Job {
	if s.jobs == nil {
		return nil
	}
	job := s.jobs.Create("recipe:" + operation.RecipeID + ":recovery")
	s.observeRecipeJob(job, operation.ID)
	return job
}

func (s *Server) cleanupInterruptedRecipe(ctx context.Context, job *jobs.Job, operation recipeops.Operation, recipe localrecipes.Recipe) error {
	var cleanupErr error
	if operation.Kind == recipeops.KindCheck {
		for _, resource := range operation.Resources {
			if resource.Kind == "staging-checkout" {
				cleanupErr = errors.Join(cleanupErr, s.removeRecoveryStagingCheckout(resource.ID))
			}
		}
		return cleanupErr
	}

	checkout := recipeCheckout(recipe)
	if info, err := os.Stat(checkout); err == nil && info.IsDir() {
		cluster, _ := sparkcluster.Status(ctx)
		if env, workdir, runtimeErr := writeRecipeRuntime(recipe, checkout, cluster, false, &operation); runtimeErr == nil {
			if job == nil {
				job = jobs.NewManager().Create("recipe:" + operation.RecipeID + ":recovery")
			}
			cleanupErr = errors.Join(cleanupErr, runConfiguredRecipeCommand(ctx, job, "recovering", "Stopping interrupted recipe runtime", workdir, env, recipe, recipe.Runtime.Lifecycle.Stop))
		}
	}
	if s.eng != nil {
		if proxy, err := s.eng.Find(ctx, "cloudless-cluster-engine-proxy"); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		} else if proxy != nil {
			cleanupErr = errors.Join(cleanupErr, s.eng.Remove(ctx, "cloudless-cluster-engine-proxy"))
		}
		cleanupErr = errors.Join(cleanupErr, s.removeRecoveryContainers(ctx, operation))
	}
	cleanupErr = errors.Join(cleanupErr, s.cleanupInterruptedRecipePeers(ctx, operation, recipe))
	cleanupErr = errors.Join(cleanupErr, terminateRecipeProcesses(operation.ID))
	return cleanupErr
}

func (s *Server) removeRecoveryContainers(ctx context.Context, operation recipeops.Operation) error {
	filters := []string{"label=cloudless.recipe.operation=" + operation.ID}
	for _, resource := range operation.Resources {
		if resource.Kind == "compose-project" && (resource.Node == "" || resource.Node == localRecipeNodeName()) {
			filters = append(filters, "label=com.docker.compose.project="+resource.ID)
		}
	}
	ids := make(map[string]struct{})
	var result error
	for _, filter := range filters {
		label, value, found := strings.Cut(strings.TrimPrefix(filter, "label="), "=")
		if !found {
			result = errors.Join(result, errors.New("invalid recipe ownership label"))
			continue
		}
		names, err := s.eng.ContainerNamesByLabel(ctx, label, value)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		for _, name := range names {
			ids[name] = struct{}{}
		}
	}
	for id := range ids {
		result = errors.Join(result, s.eng.Remove(ctx, id))
	}
	return result
}

// cleanupOwnedRecipeRuntime stops only resources journaled by the exact active
// run operation. It is the safe stop path when an editable recipe's lifecycle
// command cannot be executed. Model caches, images and source checkouts are
// retained; unrelated containers and processes are never selected.
func (s *Server) cleanupOwnedRecipeRuntime(ctx context.Context, operation recipeops.Operation, recipe localrecipes.Recipe) error {
	if !ownedRecipeCleanupOperationAllowed(operation, recipe.ID) {
		return errors.New("recipe cleanup requires the exact active run operation")
	}
	var actionErr error
	if s.eng != nil {
		if proxy, err := s.eng.Find(ctx, "cloudless-cluster-engine-proxy"); err != nil {
			actionErr = errors.Join(actionErr, err)
		} else if proxy != nil {
			actionErr = errors.Join(actionErr, s.eng.Remove(ctx, "cloudless-cluster-engine-proxy"))
		}
		actionErr = errors.Join(actionErr, s.removeRecoveryContainers(ctx, operation))
	}
	actionErr = errors.Join(actionErr, s.cleanupInterruptedRecipePeers(ctx, operation, recipe))
	actionErr = errors.Join(actionErr, terminateRecipeProcesses(operation.ID))

	remaining, inventoryErr := s.releaseMissingRecipeResources(ctx, operation.ID, recipe)
	if len(remaining) > 0 {
		return errors.Join(actionErr, inventoryErr, fmt.Errorf("%d recipe-owned runtime cleanup obligation(s) remain", len(remaining)))
	}
	if actionErr != nil && inventoryErr == nil {
		log.Printf("[recipe-stop] %s cleanup reported an error after ownership inventory proved resources absent: %v", operation.ID, actionErr)
		actionErr = nil
	}
	return errors.Join(actionErr, inventoryErr)
}

func ownedRecipeCleanupOperationAllowed(operation recipeops.Operation, targetRecipeID string) bool {
	return targetRecipeID != "" && operation.Kind == recipeops.KindRun && operation.RecipeID == targetRecipeID && operation.Phase == recipeops.PhaseActive
}

func (s *Server) removeRecoveryStagingCheckout(path string) error {
	base := filepath.Join(s.state.Dir(), "recipe-checks")
	relative, err := filepath.Rel(base, filepath.Clean(path))
	if err != nil || relative == "." || relative == "" || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." || filepath.IsAbs(relative) {
		return fmt.Errorf("refusing to remove unsafe recovery path %q", path)
	}
	return os.RemoveAll(path)
}

func (s *Server) releaseMissingRecipeResources(ctx context.Context, operationID string, recipe localrecipes.Recipe) ([]recipeops.Resource, error) {
	operation, ok := s.recipeOps.Get(operationID)
	if !ok {
		return nil, errors.New("recipe operation disappeared during recovery")
	}
	inspector := &recipeResourceInspector{server: s, recipe: recipe, localNode: localRecipeNodeName()}
	remaining := make([]recipeops.Resource, 0)
	var result error
	for _, observation := range recipeops.Inventory(ctx, operation, inspector) {
		if !observation.Resource.RequiresCleanup() {
			continue
		}
		if observation.Presence == recipeops.PresenceMissing {
			if _, err := s.recipeOps.Release(operationID, observation.Resource); err != nil {
				result = errors.Join(result, err)
			}
			continue
		}
		remaining = append(remaining, observation.Resource)
	}
	return remaining, result
}

func (s *Server) reconcileActiveRecipeResources(ctx context.Context, recipe localrecipes.Recipe) error {
	if s.recipeOps == nil {
		return nil
	}
	active, ok := s.recipeOps.ActiveForRecipe(recipe.ID)
	if !ok {
		return nil
	}
	remaining, err := s.releaseMissingRecipeResources(ctx, active.ID, recipe)
	if len(remaining) > 0 {
		err = errors.Join(err, fmt.Errorf("%d runtime cleanup obligation(s) remain", len(remaining)))
	}
	return err
}

func terminateRecipeProcesses(operationID string) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	needle := "CLOUDLESS_RECIPE_OPERATION_ID=" + operationID
	pids := make([]int, 0)
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 1 {
			continue
		}
		environment, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if readErr != nil {
			continue
		}
		for _, value := range strings.Split(string(environment), "\x00") {
			if value == needle {
				pids = append(pids, pid)
				break
			}
		}
	}
	var result error
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			result = errors.Join(result, err)
		}
	}
	return result
}
