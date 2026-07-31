package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/modelfit"
	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/platform"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
	"github.com/cloudless/orchestrator/internal/state"
)

func recipeOperationBlockingClusterDisconnect(store *recipeops.Store) (recipeops.Operation, bool) {
	if store == nil {
		return recipeops.Operation{}, false
	}
	operations := store.List()
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.Kind == recipeops.KindCheck || operation.Phase == recipeops.PhaseActive || operation.Phase == recipeops.PhaseStopped {
			continue
		}
		if !operation.Terminal() {
			return operation, true
		}
		for _, resource := range operation.Resources {
			if resource.RequiresCleanup() && resource.Locator != "" {
				return operation, true
			}
		}
	}
	return recipeops.Operation{}, false
}

func inferenceOperationBlocksClusterMutation(operation state.InferenceOperation) bool {
	return operation.ID != "" && operation.Phase != "" && operation.Phase != "error"
}

// localModelAfterClusterDisconnect keeps the selected model when it has a
// known single-machine memory fit. Cluster-only or unknown models fall back to
// the platform default instead of leaving the local engine in a crash loop.
func localModelAfterClusterDisconnect(st state.State, memoryGB int) (string, bool) {
	current := resolveModel(st.Model)
	model, known := models.Get(current)
	if !known {
		model, known = st.CustomModels[current]
	}
	if known {
		engineID := st.Engine
		if engineID == "" {
			engineID = catalog.DefaultEngine()
		}
		fit := modelfit.EstimateModel(model, modelfit.Envelope{
			MemoryGB: float64(memoryGB), MemoryType: "unified", Nodes: 1,
			Engine: engineID, Architecture: platform.Architecture(), Platform: platform.DGXSpark,
		})
		if fit.Status == "fits" || fit.Status == "tight" {
			return current, false
		}
	}
	fallback := catalog.DefaultModel()
	return fallback, current != fallback
}

func (s *Server) sparkClusterStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, err := sparkcluster.Status(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) sparkClusterDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	peers, err := sparkcluster.Discover(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "peers": []sparkcluster.Peer{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": peers})
}

func (s *Server) sparkClusterPreflight(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request sparkcluster.PreflightRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cluster request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := sparkcluster.PreflightCheck(ctx, request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) sparkClusterCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	if r.Header.Get("X-Cloudless-Action") != "spark-cluster-create" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cluster confirmation header required"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request sparkcluster.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cluster request"})
		return
	}
	job, created := s.jobs.CreateUnique("cluster:connect:"+request.Host, "cluster:connect:")
	if !created {
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
		return
	}
	s.auditJob(job, "cluster", "connect", request.Host)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := sparkcluster.CreateWithProgress(ctx, request, func(phase, message string, percent int) {
			job.ProgressOperation(phase, message, request.Host, percent, 0, 1)
		})
		if err != nil {
			job.Fail(err)
			return
		}
		job.Succeed("")
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func (s *Server) sparkClusterSelection(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	if r.Header.Get("X-Cloudless-Action") != "spark-cluster-selection" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cluster selection confirmation header required"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request struct {
		Nodes []string `json:"nodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cluster selection"})
		return
	}
	st := s.state.Get()
	if inferenceOperationBlocksClusterMutation(st.InferenceOperation) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Wait for the current model operation to finish or abort it before changing the Spark compute subset.",
		})
		return
	}
	if st.ExecutionMode == "cluster" && !st.EngineUnloaded {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Unload the distributed model before changing which Sparks participate.",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, err := sparkcluster.SetSelection(ctx, request.Nodes)
	if err != nil {
		s.auditSecurity(gatewayAuditEvent{Category: "cluster", Event: "select-nodes", Outcome: "failed", Actor: "local-ui", Detail: err.Error()})
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "cluster", Event: "select-nodes", Outcome: "succeeded", Actor: "local-ui", Target: strings.Join(request.Nodes, ",")})
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) sparkClusterRebind(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	if r.Header.Get("X-Cloudless-Action") != "spark-cluster-rebind" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cluster address confirmation header required"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request sparkcluster.RebindRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Spark address update"})
		return
	}
	st := s.state.Get()
	if inferenceOperationBlocksClusterMutation(st.InferenceOperation) ||
		(st.ExecutionMode == "cluster" && !st.EngineUnloaded) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Unload distributed inference before changing a Spark management address.",
		})
		return
	}
	if operation, blocked := recipeOperationBlockingClusterDisconnect(s.recipeOps); blocked {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":       "Finish or abort the current recipe operation before changing a Spark management address.",
			"operationId": operation.ID,
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	status, err := sparkcluster.RebindManagementAddress(ctx, request)
	if err != nil {
		s.auditSecurity(gatewayAuditEvent{Category: "cluster", Event: "rebind", Outcome: "failed", Actor: "local-ui", Target: request.Node, Detail: err.Error()})
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "cluster", Event: "rebind", Outcome: "succeeded", Actor: "local-ui", Target: request.Node})
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) sparkClusterDisconnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	if r.Header.Get("X-Cloudless-Action") != "spark-cluster-disconnect" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cluster disconnect confirmation header required"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request struct {
		Password  string            `json:"password"`
		Passwords map[string]string `json:"passwords"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid disconnect request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if request.Password != "" && len(request.Passwords) == 0 {
		request.Passwords = map[string]string{"*": request.Password}
	}
	if operation, blocked := recipeOperationBlockingClusterDisconnect(s.recipeOps); blocked {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":       "A recipe operation is still " + string(operation.Phase) + ". Abort it and wait for verified cleanup before disconnecting the Spark cluster.",
			"operationId": operation.ID,
			"recipeId":    operation.RecipeID,
		})
		return
	}
	if operation := s.state.Get().InferenceOperation; inferenceOperationBlocksClusterMutation(operation) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":       "A model lifecycle operation is still running. Abort it and wait for verified cleanup before disconnecting the Spark cluster.",
			"operationId": operation.ID,
		})
		return
	}
	if err := sparkcluster.ValidateDisconnectAccess(ctx, request.Passwords); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	job, created := s.jobs.CreateUnique("engine:cluster-disconnect-fallback", "engine:cluster-disconnect")
	if !created {
		writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
		return
	}
	s.auditJob(job, "cluster", "disconnect", "spark-cluster")
	passwords := request.Passwords
	go s.runSparkClusterDisconnect(job, passwords)
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

func (s *Server) runSparkClusterDisconnect(job *jobs.Job, passwords map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	st := s.state.Get()
	if st.LocalRecipeID != "" && !st.EngineUnloaded {
		job.ProgressOperation("stopping-recipe", "Stopping the distributed recipe and verifying cleanup.", st.LocalRecipeID, 8, 0, 3)
		recipe, ok, recipeErr := s.recipes.Get(st.LocalRecipeID)
		if recipeErr != nil || !ok {
			job.Fail(errors.New("the active distributed recipe could not be loaded for a safe stop"))
			return
		}
		operation, operationErr := s.beginRecipeOperation(recipeops.KindStop, recipe, recipeops.PhaseStopping)
		if operationErr != nil {
			job.Fail(fmt.Errorf("the active recipe could not enter its safe stop operation: %w", operationErr))
			return
		}
		stopJob := s.jobs.Create("recipe:" + recipe.ID + ":disconnect-stop")
		s.observeRecipeJob(stopJob, operation.ID)
		s.stopLocalRecipe(stopJob, operation.RecipeSnapshot, operation.ID)
		if stopped := stopJob.Snapshot(); stopped.Error != "" {
			job.Fail(errors.New("the recipe could not be fully stopped, so the cluster was left connected: " + stopped.Error))
			return
		}
	} else if st.ExecutionMode == "cluster" && !st.EngineUnloaded {
		job.ProgressOperation("stopping-inference", "Stopping distributed inference on every Spark.", st.Model, 10, 0, 3)
		stopJob := s.jobs.Create("engine:cluster-disconnect-stop")
		s.observeInferenceJob(stopJob, "unload", "", st.InferenceRuntime())
		s.runEngineUnload(stopJob)
		if stopped := stopJob.Snapshot(); stopped.Error != "" {
			job.Fail(errors.New("distributed inference could not be fully stopped, so the cluster was left connected: " + stopped.Error))
			return
		}
	}
	if err := sparkcluster.DisconnectWithProgress(ctx, passwords, func(phase, message string, percent int) {
		job.ProgressOperation(phase, message, "Spark fabric", 18+(percent*55/100), 1, 3)
	}); err != nil {
		job.Fail(err)
		return
	}

	// The private link is gone, so distributed inference must never remain the
	// persisted target. The same job continues through the local fallback.
	st = s.state.Get()
	fallback, changed := localModelAfterClusterDisconnect(st, totalVRAMGB(ctx))
	runtime := st.InferenceRuntime()
	runtime.ExecutionMode = "local"
	runtime.LocalRecipeID = ""
	if changed {
		runtime.Model = fallback
	}
	if err := s.state.CommitInferenceRuntime(runtime); err != nil {
		job.Fail(errors.New("the cluster was disconnected, but local inference state could not be saved"))
		return
	}
	engineID := st.Engine
	if engineID == "" {
		engineID = catalog.DefaultEngine()
	}
	target, ok := catalog.Get(engineID)
	if !ok || !target.Engine {
		target, _ = catalog.Get(catalog.DefaultEngine())
	}
	job.ProgressOperation("local-fallback", "Starting a compatible model locally on this Spark.", fallback, 78, 2, 3)
	s.observeInferenceJob(job, "cluster-fallback", target.ID, st.InferenceRuntime())
	s.applyEngine(job, target)
}
