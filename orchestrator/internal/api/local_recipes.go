package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/customengine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

var recipeInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
var localRecipeIDPattern = regexp.MustCompile(`^(?:deepseek-v4-flash-dspark-2x|deepseek-v4-flash-dual-dspark-1m|local-[0-9a-f]{16})$`)

// recipeRuntimeRoot is fixed in production. It is a package variable only so
// failure-injection tests can exercise real checkout/cleanup behavior without
// requiring root access to /var/lib.
var recipeRuntimeRoot = "/var/lib/cloudless/recipes-runtime"

// recipeDockerCompatibilityExecutable is not a Docker socket client. It sends
// the operation identity and requested arguments to cloudless-engine, which
// independently authenticates the signed recipe and enforces its policy.
var recipeDockerCompatibilityExecutable = "/usr/lib/cloudless/cloudless-docker"

// recipeStablePort is the permanent Cloudless inference port. It is a variable
// only so package-level lifecycle tests can use an isolated listener instead
// of colliding with a developer's running Cloudless instance.
var recipeStablePort = catalog.EnginePort

type recipeJobControl struct {
	operationID string
	cancel      context.CancelFunc
	job         *jobs.Job
}

func (s *Server) localRecipesList(w http.ResponseWriter, r *http.Request) {
	recipes, err := s.recipes.ListAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	active := ""
	if st := s.state.Get(); st.LocalRecipeID != "" && !st.EngineUnloaded {
		active = st.LocalRecipeID
	}
	plans := make(map[string]recipeLaunchPlan, len(recipes))
	revisions := make(map[string]string, len(recipes))
	for _, recipe := range recipes {
		plans[recipe.ID] = buildRecipeLaunchPlan(r.Context(), s.eng, recipe)
		if revision, revisionErr := recipeops.RecipeRevision(recipe); revisionErr == nil {
			revisions[recipe.ID] = revision
		}
	}
	operations := []recipeops.Operation(nil)
	if s.recipeOps != nil {
		operations = s.recipeOps.List()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recipes": recipes, "active": active, "jobs": s.jobs.List("recipe:"),
		"defaults":   localrecipes.NewManagedDraft(),
		"contract":   s.inferenceContract(),
		"cluster":    clusterCompute(r.Context(), totalVRAMGB(r.Context())),
		"plans":      plans,
		"revisions":  revisions,
		"operations": operations,
		"failures":   recipeFailureViews(operations),
		"trust":      recipeTrustViews(recipes, operations),
	})
}

func (s *Server) beginRecipeOperation(kind recipeops.Kind, recipe localrecipes.Recipe, initial recipeops.Phase) (recipeops.Operation, error) {
	if s.recipeOpsErr != nil {
		return recipeops.Operation{}, fmt.Errorf("open recipe operation journal: %w", s.recipeOpsErr)
	}
	if s.recipeOps == nil {
		return recipeops.Operation{}, errors.New("recipe operation journal is unavailable")
	}
	var operation recipeops.Operation
	var err error
	if kind == recipeops.KindRun {
		current := s.state.Get().InferenceRuntime()
		operation, err = s.recipeOps.BeginRunValidatedWithPrevious(recipe, recipeops.PreviousRuntime{
			Engine: current.Engine, Model: current.Model, EngineUnloaded: current.EngineUnloaded,
			ExecutionMode: current.ExecutionMode, LocalRecipeID: current.LocalRecipeID,
		})
	} else {
		operation, err = s.recipeOps.BeginUnique(kind, recipe, initial)
	}
	if err == nil {
		s.auditSecurity(gatewayAuditEvent{
			Category: "recipe", Event: string(kind), Outcome: "started", Actor: "local-ui",
			Target: recipe.ID, OperationID: operation.ID,
		})
	}
	return operation, err
}

func (s *Server) executeRecipeMutation(recipeID string, mutate func() error) error {
	if s.recipeOpsErr != nil {
		return fmt.Errorf("open recipe operation journal: %w", s.recipeOpsErr)
	}
	if s.recipeOps == nil {
		return errors.New("recipe operation journal is unavailable")
	}
	return s.recipeOps.ExecuteMutation(recipeID, mutate)
}

func writeRecipeMutationConflict(w http.ResponseWriter, action string, conflict *recipeops.MutationConflict) {
	operation := conflict.Operation
	writeJSON(w, http.StatusConflict, map[string]string{
		"error":       fmt.Sprintf("cannot %s this recipe while operation %s is %s", action, operation.ID, operation.Phase),
		"operationId": operation.ID,
		"phase":       string(operation.Phase),
	})
}

func (s *Server) transitionRecipeOperation(id string, phase recipeops.Phase, cause error) error {
	if s.recipeOps == nil {
		return errors.New("recipe operation journal is unavailable")
	}
	operation, err := s.recipeOps.Transition(id, phase, cause)
	if err == nil {
		outcome := ""
		switch {
		case operation.Kind == recipeops.KindRun && phase == recipeops.PhaseActive,
			operation.Kind == recipeops.KindCheck && phase == recipeops.PhasePrepared,
			operation.Kind == recipeops.KindStop && phase == recipeops.PhaseStopped:
			outcome = "succeeded"
		case phase == recipeops.PhaseFailed:
			outcome = "failed"
		case phase == recipeops.PhaseAborted:
			outcome = "aborted"
		}
		if outcome != "" {
			detail := ""
			if cause != nil {
				detail = cause.Error()
			}
			s.auditSecurity(gatewayAuditEvent{
				Category: "recipe", Event: string(operation.Kind), Outcome: outcome, Actor: "cloudlessd",
				Target: operation.RecipeID, OperationID: operation.ID, Detail: detail,
			})
		}
	}
	return err
}

func (s *Server) pruneRecipeOperations() {
	if s.recipeOps != nil {
		if err := s.recipeOps.Prune(200, 90*24*time.Hour); err != nil {
			log.Printf("[recipe-operations] prune history: %v", err)
		}
	}
	s.maybeRecipeCacheGC()
}

func (s *Server) claimRecipeResource(operationID string, resource recipeops.Resource) error {
	if s.recipeOps == nil {
		return errors.New("recipe operation journal is unavailable")
	}
	_, err := s.recipeOps.Claim(operationID, resource)
	return err
}

func (s *Server) releaseRecipeResource(operationID string, resource recipeops.Resource) error {
	if s.recipeOps == nil {
		return errors.New("recipe operation journal is unavailable")
	}
	_, err := s.recipeOps.Release(operationID, resource)
	return err
}

func (s *Server) bindRecipeSourceRevision(operationID, revision string) (recipeops.Operation, error) {
	if s.recipeOps == nil {
		return recipeops.Operation{}, errors.New("recipe operation journal is unavailable")
	}
	return s.recipeOps.BindResolvedSource(operationID, revision)
}

func (s *Server) recordRecipeCheck(operationID, id string, status recipeops.CheckStatus, summary, detail string, values map[string]string) error {
	if s.recipeOps == nil {
		return errors.New("recipe operation journal is unavailable")
	}
	_, err := s.recipeOps.RecordCheck(operationID, recipeops.CheckResult{
		ID: id, Status: status, Summary: summary, Detail: detail, Values: values,
	})
	return err
}

func (s *Server) failRecipeCheck(job *jobs.Job, operationID, id, summary string, cause error) {
	if cause == nil {
		cause = errors.New(summary)
	}
	if err := s.recordRecipeCheck(operationID, id, recipeops.CheckFail, summary, cause.Error(), nil); err != nil {
		cause = errors.Join(cause, fmt.Errorf("persist failed preflight evidence: %w", err))
	}
	s.finishRecipeOperation(job, operationID, cause)
}

func (s *Server) unknownRecipeCheck(job *jobs.Job, operationID, id, summary string, cause error) {
	if cause == nil {
		cause = errors.New(summary)
	}
	if err := s.recordRecipeCheck(operationID, id, recipeops.CheckUnknown, summary, cause.Error(), nil); err != nil {
		cause = errors.Join(cause, fmt.Errorf("persist indeterminate preflight evidence: %w", err))
	}
	s.finishRecipeOperation(job, operationID, cause)
}

func (s *Server) closeActiveRecipeOperation(recipeID string, cause error) error {
	if s.recipeOps == nil {
		return nil
	}
	active, ok := s.recipeOps.ActiveForRecipe(recipeID)
	if !ok {
		return nil
	}
	if _, err := s.recipeOps.Transition(active.ID, recipeops.PhaseStopping, nil); err != nil {
		return err
	}
	phase := recipeops.PhaseStopped
	if cause != nil {
		phase = recipeops.PhaseFailed
	}
	_, err := s.recipeOps.Transition(active.ID, phase, cause)
	s.pruneRecipeOperations()
	return err
}

func (s *Server) localRecipeCreate(w http.ResponseWriter, r *http.Request) {
	var draft localrecipes.Draft
	if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recipe details are required"})
		return
	}
	recipe, err := s.recipes.Create(draft)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, recipe)
}

func (s *Server) localRecipeUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if st := s.state.Get(); st.LocalRecipeID == id && !st.EngineUnloaded {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stop this recipe before changing its operating profile"})
		return
	}
	var draft localrecipes.Draft
	if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recipe details are required"})
		return
	}
	var recipe localrecipes.Recipe
	err := s.executeRecipeMutation(id, func() error {
		var updateErr error
		recipe, updateErr = s.recipes.Update(id, draft)
		return updateErr
	})
	var conflict *recipeops.MutationConflict
	if errors.As(err, &conflict) {
		writeRecipeMutationConflict(w, "change", conflict)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (s *Server) localRecipeImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceURL string `json:"sourceUrl"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sourceUrl is required"})
		return
	}
	preview, err := s.recipes.PreviewImport(body.SourceURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var recipe localrecipes.Recipe
	err = s.executeRecipeMutation(preview.ID, func() error {
		var importErr error
		recipe, importErr = s.recipes.Import(body.SourceURL)
		return importErr
	})
	var conflict *recipeops.MutationConflict
	if errors.As(err, &conflict) {
		writeRecipeMutationConflict(w, "re-import", conflict)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, recipe)
}

func (s *Server) localRecipeImportPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceURL string `json:"sourceUrl"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sourceUrl is required"})
		return
	}
	recipe, err := s.recipes.PreviewImport(body.SourceURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":   "cloudless",
		"recipe": recipe,
		"draft":  localrecipes.DraftFromRecipe(recipe),
	})
}

func (s *Server) localRecipeDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.recipeOpsErr != nil || s.recipeOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recipe operation journal is unavailable"})
		return
	}
	if st := s.state.Get(); !st.EngineUnloaded && st.LocalRecipeID == id {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stop this recipe before removing it"})
		return
	}
	if _, ok, err := s.recipes.GetAny(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	} else if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	err := s.recipeOps.Tombstone(id, func() error { return s.recipes.Tombstone(id) })
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	finalized, err := s.finalizeRecipeTombstone(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !finalized {
		writeJSON(w, http.StatusAccepted, map[string]any{"recipe": id, "tombstoned": true, "message": "Removal is waiting for runtime cleanup to finish."})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) finalizeRecipeTombstone(id string) (bool, error) {
	if s.recipeOps == nil || !s.recipeOps.IsTombstoned(id) {
		return false, nil
	}
	return s.recipeOps.FinalizeTombstone(id, func() error { return s.recipes.Delete(id) })
}

func (s *Server) localRecipeSource(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Could not load this recipe.", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if recipe.Source.URL != localrecipes.DeepSeekDSparkSource || recipe.Source.Revision != localrecipes.DeepSeekDSparkRevision {
		configuration, marshalErr := json.MarshalIndent(localrecipes.DraftFromRecipe(recipe), "", "  ")
		if marshalErr != nil {
			http.Error(w, "Could not render this recipe.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(w, recipeSourceDocument(recipe, "Complete editable Cloudless recipe configuration\n\n"+string(configuration)))
		return
	}
	readmeURL := "https://raw.githubusercontent.com/tonyd2wild/DeepSeek-v4-Flash-DSpark-60-tok-s-900K-ctx-2x-DGX-Spark/" + recipe.Source.Revision + "/README.md"
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readmeURL, nil)
	if err != nil {
		http.Error(w, "Could not prepare the reviewed source.", http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "The reviewed source is temporarily unavailable.", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "The reviewed source is temporarily unavailable.", http.StatusBadGateway)
		return
	}
	readme, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		http.Error(w, "Could not read the reviewed source.", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, recipeSourceDocument(recipe, string(readme)))
}

func recipeSourceDocument(recipe localrecipes.Recipe, readme string) string {
	short := func(value string) string {
		if len(value) > 12 {
			return value[:12]
		}
		return value
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(recipe.Name) + `</title><style>
:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,sans-serif;background:#0d1220;color:#eef2ff}*{box-sizing:border-box}body{margin:0;padding:32px;background:radial-gradient(circle at 85% 0,rgba(95,139,255,.12),transparent 34%),#0d1220}.page{max-width:980px;margin:auto}.head{padding:22px;border:1px solid #293149;border-radius:18px;background:#141a2a}.eyebrow{color:#7f9cff;font-size:11px;font-weight:800;letter-spacing:.12em;text-transform:uppercase}h1{margin:7px 0 8px;font-size:24px}.meta{color:#9da8c2;font-size:13px;line-height:1.6}.pin{display:inline-block;margin:12px 7px 0 0;padding:6px 9px;border:1px solid #303a56;border-radius:8px;background:#1b2235;color:#c9d3ed;font:11px ui-monospace,monospace}.source{margin-top:16px;padding:22px;overflow:auto;border:1px solid #293149;border-radius:18px;background:#111726;color:#dce4f8;font:13px/1.65 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;overflow-wrap:anywhere}</style></head><body><main class="page"><section class="head"><div class="eyebrow">Reviewed recipe source</div><h1>` + html.EscapeString(recipe.Name) + `</h1><div class="meta">` + html.EscapeString(recipe.SourceURL) + `<br>This is the README from the exact source revision saved by Cloudless.</div><span class="pin">Recipe ` + html.EscapeString(short(recipe.Revision)) + `</span><span class="pin">Model ` + html.EscapeString(short(recipe.ModelRevision)) + `</span></section><pre class="source">` + html.EscapeString(readme) + `</pre></main></body></html>`
}

func (s *Server) localRecipeRun(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	if decision := recipeExecutionPolicyEvaluator(recipe); !decision.Allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": decision.Reason, "action": "review-permissions",
		})
		return
	}
	if st := s.state.Get(); st.LocalRecipeID != "" && !st.EngineUnloaded {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stop the active local recipe before starting another one"})
		return
	}
	for _, snapshot := range s.jobs.List("recipe:") {
		if !snapshot.Done {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another recipe operation is already running", "jobId": snapshot.ID})
			return
		}
	}
	operation, err := s.beginRecipeOperation(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		if errors.Is(err, recipeops.ErrPreflightRequired) {
			writeJSON(w, http.StatusPreconditionFailed, map[string]string{
				"error":  err.Error(),
				"action": "check",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	job := s.jobs.Create("recipe:" + recipe.ID)
	s.observeRecipeJob(job, operation.ID)
	if operation.RecipeSnapshot.Runtime.Adapter == localrecipes.ManagedContainerAdapter {
		go s.runManagedContainerRecipe(job, operation.RecipeSnapshot, operation.ID)
	} else {
		go s.runLocalRecipe(job, operation.RecipeSnapshot, operation.ID)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "operationId": operation.ID, "recipe": recipe.ID})
}

func (s *Server) localRecipeCheck(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	if decision := recipeExecutionPolicyEvaluator(recipe); !decision.Allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": decision.Reason, "action": "review-permissions",
		})
		return
	}
	for _, snapshot := range s.jobs.List("recipe:") {
		if !snapshot.Done {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another recipe operation is already running", "jobId": snapshot.ID})
			return
		}
	}
	operation, err := s.beginRecipeOperation(recipeops.KindCheck, recipe, recipeops.PhaseChecking)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	job := s.jobs.Create("recipe:" + recipe.ID + ":check")
	s.observeRecipeJob(job, operation.ID)
	if operation.RecipeSnapshot.Runtime.Adapter == localrecipes.ManagedContainerAdapter {
		go s.checkManagedContainerRecipe(job, operation.RecipeSnapshot, operation.ID)
	} else {
		go s.checkLocalRecipe(job, operation.RecipeSnapshot, operation.ID)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "operationId": operation.ID, "recipe": recipe.ID})
}

func (s *Server) localRecipeAbort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.recipeJobsMu.Lock()
	control := s.recipeJobs[id]
	s.recipeJobsMu.Unlock()
	if control.cancel == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this recipe is not currently starting"})
		return
	}
	if control.job.Snapshot().Done {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this recipe operation has already finished"})
		return
	}
	operationID := control.operationID
	if s.recipeOps != nil {
		operation, ok := s.recipeOps.Get(operationID)
		if !ok || operation.Kind != recipeops.KindRun || operation.RecipeID != id || operation.Phase == recipeops.PhaseStopped || operation.Phase == recipeops.PhaseFailed || operation.Phase == recipeops.PhaseAborted {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "the running job is no longer bound to an abortable recipe operation"})
			return
		}
		if _, err := s.recipeOps.Transition(operation.ID, recipeops.PhaseStopping, nil); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "could not begin verified abort cleanup: " + err.Error()})
			return
		}
	}
	control.job.Progress("stopping", "Stopping the recipe operation and its child processes...", -1, -1)
	control.cancel()
	writeJSON(w, http.StatusAccepted, map[string]string{"recipe": id, "jobId": control.job.ID, "operationId": operationID})
}

func (s *Server) registerRecipeJob(id, operationID string, cancel context.CancelFunc, job *jobs.Job) {
	s.recipeJobsMu.Lock()
	defer s.recipeJobsMu.Unlock()
	if s.recipeJobs == nil {
		s.recipeJobs = make(map[string]recipeJobControl)
	}
	s.recipeJobs[id] = recipeJobControl{operationID: operationID, cancel: cancel, job: job}
}

func (s *Server) unregisterRecipeJob(id, operationID string) {
	s.recipeJobsMu.Lock()
	if control, ok := s.recipeJobs[id]; ok && control.operationID == operationID {
		delete(s.recipeJobs, id)
	}
	s.recipeJobsMu.Unlock()
}

func (s *Server) localRecipeStop(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	operation, err := s.beginRecipeOperation(recipeops.KindStop, recipe, recipeops.PhaseStopping)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if active, activeOK := s.recipeOps.ActiveForRecipe(recipe.ID); activeOK && active.PreparedImageReference != "" {
		operation, err = s.recipeOps.BindPreparedImage(operation.ID, active.PreparedImageReference, active.PreparedImageDigest)
		if err != nil {
			_, _ = s.recipeOps.Transition(operation.ID, recipeops.PhaseFailed, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not bind the active runtime image to the stop operation"})
			return
		}
	}
	job := s.jobs.Create("recipe:" + recipe.ID + ":stop")
	s.observeRecipeJob(job, operation.ID)
	stopRecipe := operation.RecipeSnapshot
	if operation.PreparedImageReference != "" {
		stopRecipe.Engine.Image = operation.PreparedImageReference
	}
	if stopRecipe.Runtime.Adapter == localrecipes.ManagedContainerAdapter {
		go s.stopManagedContainerRecipe(job, stopRecipe, operation.ID)
	} else {
		go s.stopLocalRecipe(job, stopRecipe, operation.ID)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "operationId": operation.ID, "recipe": recipe.ID})
}

func recipeCheckout(recipe localrecipes.Recipe) string {
	// This must survive cloudlessd restarts. The service uses PrivateTmp, so a
	// checkout under /tmp or /var/tmp disappears into a new mount namespace and
	// leaves Cloudless unable to run the recipe's stop lifecycle after an update.
	return filepath.Join(recipeRuntimeRoot, recipe.ID)
}

func commandEnv(extra map[string]string) []string {
	env := os.Environ()
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func localRecipeNodeName() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "this Spark"
	}
	return strings.TrimSpace(name)
}

func runRecipeCommand(ctx context.Context, job *jobs.Job, phase, label, dir string, env map[string]string, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = commandEnv(env)
	// Every recipe command owns a process group. Canceling only the wrapper
	// shell leaves docker/buildx and other descendants running with the output
	// pipe open, which made Abort appear to do nothing.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return err
	}
	done := make(chan error, 1)
	processDone := make(chan struct{})
	go func() {
		err := cmd.Wait()
		_ = writer.CloseWithError(err)
		done <- err
		close(processDone)
	}()
	go func() {
		select {
		case <-ctx.Done():
			// Ask the complete command tree to stop cleanly, then force it down
			// if a container client ignores termination.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-processDone:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-processDone:
		}
	}()
	progress := newRecipeCommandProgress(phase, label)
	lastLine := ""
	readErr := consumeRecipeCommandOutput(reader, func(raw string) {
		line := strings.TrimSpace(raw)
		if line == "" {
			return
		}
		if len(line) > 220 {
			line = line[:217] + "..."
		}
		lastLine = line
		if message, doneBytes, totalBytes, ok := progress.parse(line); ok {
			job.ProgressBytes(phase, message, doneBytes, totalBytes)
		} else {
			job.Progress(phase, label+": "+line, -1, -1)
		}
	})
	err := <-done
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		if lastLine != "" {
			return fmt.Errorf("%s: %s: %w", label, lastLine, err)
		}
		return fmt.Errorf("%s: %w", label, err)
	}
	return readErr
}

// consumeRecipeCommandOutput retains only a bounded preview of each line while
// continuing to drain arbitrarily long output. This prevents a child process
// from blocking forever on a full pipe when it emits a line larger than
// bufio.Scanner's token limit.
func consumeRecipeCommandOutput(reader io.Reader, emit func(string)) error {
	const previewLimit = 4096
	buffered := bufio.NewReaderSize(reader, 64*1024)
	preview := make([]byte, 0, previewLimit)
	truncated := false
	emitLine := func() {
		if len(preview) == 0 && !truncated {
			return
		}
		line := string(preview)
		if truncated {
			line += "..."
		}
		emit(line)
		preview = preview[:0]
		truncated = false
	}
	for {
		fragment, err := buffered.ReadSlice('\n')
		content := fragment
		if len(content) > 0 && content[len(content)-1] == '\n' {
			content = content[:len(content)-1]
		}
		if remaining := previewLimit - len(preview); remaining > 0 {
			if len(content) > remaining {
				preview = append(preview, content[:remaining]...)
				truncated = true
			} else {
				preview = append(preview, content...)
			}
		} else if len(content) > 0 {
			truncated = true
		}
		switch err {
		case nil:
			emitLine()
		case bufio.ErrBufferFull:
			truncated = true
			continue
		case io.EOF:
			emitLine()
			return nil
		default:
			emitLine()
			return err
		}
	}
}

func verifyRecipeFiles(recipe localrecipes.Recipe, checkout string) error {
	for name, expected := range recipe.Source.Files {
		data, err := os.ReadFile(filepath.Join(checkout, name))
		if err != nil {
			return fmt.Errorf("verify %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != expected {
			return fmt.Errorf("reviewed file changed: %s", name)
		}
	}
	return nil
}

// prepareRecipeCompose makes one explicit Cloudless-managed change after the
// reviewed source hashes have been verified: recipe containers restart after a
// host reboot. The exact match makes an upstream layout change fail closed.
func prepareRecipeCompose(checkout, restartPolicy string) error {
	path := filepath.Join(checkout, "docker-compose.dspark.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	needle := "    image: ${DSPARK_VLLM_IMAGE:-vllm-dspark-runtime:clean}\n"
	if strings.Count(string(data), needle) != 1 {
		return errors.New("reviewed compose service layout changed")
	}
	volumeMount := "      - ${HF_CACHE:-${HOME}/.cache/huggingface}:/cache/huggingface\n"
	if strings.Count(string(data), volumeMount) != 1 {
		return errors.New("reviewed compose model-cache layout changed")
	}
	managed := strings.Replace(string(data), needle, needle+"    restart: "+restartPolicy+"\n", 1)
	managed = strings.Replace(managed, volumeMount, "      - ${HF_CACHE:-/var/lib/cloudless/models-cache}:/cache/huggingface\n", 1)
	// The reviewed runtime uses host networking. Its original loopback bind is
	// healthy from the host, but unreachable from the bridge-networked stable
	// Cloudless proxy. Bind the private recipe port on the host interfaces; the
	// public contract remains protected by the loopback-only port 8000 proxy.
	host := "        --host ${VLLM_HOST:-127.0.0.1}\n"
	if strings.Count(managed, host) != 1 {
		return errors.New("reviewed compose engine-host layout changed")
	}
	managed = strings.Replace(managed, host, "        --host ${VLLM_HOST:-0.0.0.0}\n", 1)
	port := "        --port 8888\n"
	if strings.Count(managed, port) != 1 {
		return errors.New("reviewed compose engine-port layout changed")
	}
	managed = strings.Replace(managed, port, "        --port ${ENGINE_PORT:-8890}\n", 1)
	return os.WriteFile(path, []byte(managed), 0o600)
}

// prepareRecipeModelPin teaches the reviewed download helper to use the exact
// model snapshot stored in the local recipe. Each substitution is exact and
// counted so upstream changes fail instead of weakening reproducibility.
func prepareRecipeModelPin(recipe localrecipes.Recipe, checkout string) error {
	path := filepath.Join(checkout, "prepare-dspark-model-cache.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	managed := string(data)
	replacements := []struct {
		old, new string
		count    int
	}{
		{"    -e DSPARK_MODEL=\"$DSPARK_MODEL\" \\\n", "    -e DSPARK_MODEL=\"$DSPARK_MODEL\" \\\n    -e DSPARK_MODEL_REVISION=\"$DSPARK_MODEL_REVISION\" \\\n    -e HF_TOKEN=\"${HF_TOKEN:-}\" \\\n", 2},
		{"snapshot_download(os.environ[\"DSPARK_MODEL\"], max_workers=", "snapshot_download(os.environ[\"DSPARK_MODEL\"], revision=os.environ[\"DSPARK_MODEL_REVISION\"], max_workers=", 1},
		{"snapshot_download(os.environ[\"DSPARK_MODEL\"], local_files_only=True)", "snapshot_download(os.environ[\"DSPARK_MODEL\"], revision=os.environ[\"DSPARK_MODEL_REVISION\"], local_files_only=True)", 1},
	}
	for _, replacement := range replacements {
		if strings.Count(managed, replacement.old) != replacement.count {
			return errors.New("reviewed model download helper layout changed")
		}
		managed = strings.Replace(managed, replacement.old, replacement.new, replacement.count)
	}
	return os.WriteFile(path, []byte(managed), 0o700)
}

// prepareRecipeWorkerCheckout separates the root-owned coordinator checkout
// from the enrolled user's persistent checkout on worker Sparks. The reviewed
// upstream scripts originally assume the same absolute path on both nodes,
// which is neither writable nor safe when cloudlessd runs as root.
func prepareRecipeWorkerCheckout(checkout string) error {
	paths := []string{"start-deepseek-v4-flash-dspark.sh", "stop-deepseek-v4-flash-dspark.sh"}
	for _, name := range paths {
		path := filepath.Join(checkout, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		managed := string(data)
		composeLine := "COMPOSE_FILE=\"${COMPOSE_FILE:-$SCRIPT_DIR/docker-compose.dspark.yml}\"\n"
		if strings.Count(managed, composeLine) != 1 {
			return fmt.Errorf("reviewed %s worker-path layout changed", name)
		}
		managed = strings.Replace(managed, composeLine, composeLine+"WORKER_CHECKOUT=\"${WORKER_CHECKOUT:-$SCRIPT_DIR}\"\n", 1)
		remoteUses := strings.Count(managed, "cd '$SCRIPT_DIR'")
		if remoteUses < 1 {
			return fmt.Errorf("reviewed %s remote checkout layout changed", name)
		}
		managed = strings.ReplaceAll(managed, "cd '$SCRIPT_DIR'", "cd '$WORKER_CHECKOUT'")
		if name == "start-deepseek-v4-flash-dspark.sh" {
			replacements := []struct{ old, new string }{
				{"${WORKER_HOST}:${SCRIPT_DIR}", "${WORKER_HOST}:${WORKER_CHECKOUT}"},
				{"mkdir -p '$SCRIPT_DIR'", "mkdir -p '$WORKER_CHECKOUT'"},
			}
			for _, replacement := range replacements {
				if !strings.Contains(managed, replacement.old) {
					return fmt.Errorf("reviewed %s copy layout changed", name)
				}
				managed = strings.ReplaceAll(managed, replacement.old, replacement.new)
			}
		}
		if err := os.WriteFile(path, []byte(managed), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// prepareDeepSeekV4Flash1M converts the reviewed MiaAI-Lab deployment into a
// Cloudless-managed runtime. The upstream recipe assumes hand-written .env
// files and identical checkout paths on both nodes; Cloudless instead supplies
// the enrolled worker, fabric settings, stable private port, and model alias.
func prepareDeepSeekV4Flash1M(checkout, restartPolicy string) error {
	composePath := filepath.Join(checkout, "docker-compose.yml")
	data, err := os.ReadFile(composePath)
	if err != nil {
		return err
	}
	managed := string(data)
	replacements := []struct{ old, new string }{
		{"    image: aidendle94/sparkrun-vllm-ds4-gb10:production-ready\n", "    image: ${DSPARK_VLLM_IMAGE:-aidendle94/sparkrun-vllm-ds4-gb10:production-ready}\n    restart: " + restartPolicy + "\n"},
		{"      - ${HF_CACHE:-${HOME}/.cache/huggingface}:/cache/huggingface\n", "      - ${HF_CACHE:-/var/lib/cloudless/models-cache}:/cache/huggingface\n"},
		{"      HF_HUB_OFFLINE: \"0\"\n", "      HF_HUB_OFFLINE: \"1\"\n"},
		{"serve deepseek-ai/DeepSeek-V4-Flash\n", "serve ${DSPARK_MODEL}\n        --revision ${DSPARK_MODEL_REVISION}\n"},
		{"--served-model-name deepseek-v4-flash\n", "--served-model-name ${SERVED_MODEL_NAME}\n"},
		{"--port 8000\n", "--port ${ENGINE_PORT}\n"},
		{"--max-model-len 1000000\n", "--max-model-len ${MAX_MODEL_LEN}\n"},
		{"--max-num-seqs 6\n", "--max-num-seqs ${MAX_NUM_SEQS}\n"},
		{"--gpu-memory-utilization 0.83\n", "--gpu-memory-utilization ${GPU_MEMORY_UTILIZATION}\n"},
		{"--master-port 25000\n", "--master-port ${MASTER_PORT}\n"},
	}
	for _, replacement := range replacements {
		if strings.Count(managed, replacement.old) != 1 {
			return errors.New("reviewed MiaAI-Lab compose layout changed")
		}
		managed = strings.Replace(managed, replacement.old, replacement.new, 1)
	}
	if err := os.WriteFile(composePath, []byte(managed), 0o600); err != nil {
		return err
	}

	start := `#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/.env.dspark}"
: "${WORKER_HOST:?WORKER_HOST is required}"
: "${WORKER_CHECKOUT:?WORKER_CHECKOUT is required}"
cd "$SCRIPT_DIR"
ssh "$WORKER_HOST" "mkdir -p '$WORKER_CHECKOUT'"
rsync -a --delete --exclude .git "$SCRIPT_DIR/" "${WORKER_HOST}:${WORKER_CHECKOUT}/"
echo "Starting DeepSeek V4 Flash worker on ${WORKER_HOST}..."
ssh "$WORKER_HOST" "cd '$WORKER_CHECKOUT' && env COMPOSE_DISABLE_ENV_FILE=1 NODE_RANK=1 HEADLESS=1 docker compose --env-file .env.dspark -f docker-compose.yml up -d"
echo "Starting DeepSeek V4 Flash coordinator..."
COMPOSE_DISABLE_ENV_FILE=1 NODE_RANK=0 HEADLESS= docker compose --env-file "$ENV_FILE" -f docker-compose.yml up -d
echo "Both inference containers started; Cloudless will monitor model initialization on port ${ENGINE_PORT}."
`
	stop := `#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/.env.dspark}"
cd "$SCRIPT_DIR"
COMPOSE_DISABLE_ENV_FILE=1 docker compose --env-file "$ENV_FILE" -f docker-compose.yml down || true
if [ -n "${WORKER_HOST:-}" ] && [ -n "${WORKER_CHECKOUT:-}" ]; then
  ssh "$WORKER_HOST" "cd '$WORKER_CHECKOUT' && COMPOSE_DISABLE_ENV_FILE=1 docker compose --env-file .env.dspark -f docker-compose.yml down" || true
fi
`
	download := `#!/usr/bin/env bash
set -euo pipefail
: "${DSPARK_MODEL:?DSPARK_MODEL is required}"
: "${DSPARK_MODEL_REVISION:?DSPARK_MODEL_REVISION is required}"
: "${DSPARK_VLLM_IMAGE:?DSPARK_VLLM_IMAGE is required}"
: "${HF_CACHE:?HF_CACHE is required}"
mkdir -p "$HF_CACHE"
docker run --rm --entrypoint python \
  -v "$HF_CACHE:/cache/huggingface" \
  -e HF_HOME=/cache/huggingface \
  -e HF_HUB_OFFLINE=0 -e HF_HUB_DISABLE_XET=1 \
  -e DSPARK_MODEL -e DSPARK_MODEL_REVISION -e HF_TOKEN="${HF_TOKEN:-}" \
  "$DSPARK_VLLM_IMAGE" -c 'import hashlib, os, pathlib; from huggingface_hub import HfApi, snapshot_download; model=os.environ["DSPARK_MODEL"]; revision=os.environ["DSPARK_MODEL_REVISION"]; token=os.environ.get("HF_TOKEN") or None; snapshot=pathlib.Path(snapshot_download(model, revision=revision, token=token, max_workers=8)); info=HfApi(token=token).model_info(model, revision=revision, files_metadata=True); missing=[item.rfilename for item in info.siblings if item.size is not None and (not (snapshot/item.rfilename).is_file() or (snapshot/item.rfilename).stat().st_size != item.size)]; assert not missing, "incomplete pinned snapshot: "+", ".join(missing[:8]); marker=hashlib.sha256((model+"\0"+revision).encode()).hexdigest(); path=pathlib.Path(os.environ["HF_HOME"])/".cloudless-complete"/marker; path.parent.mkdir(parents=True, exist_ok=True); path.write_text(model+"@"+revision+"\n")'
`
	if err := os.WriteFile(filepath.Join(checkout, "start-deepseek-v4-flash.sh"), []byte(start), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(checkout, "stop-deepseek-v4-flash.sh"), []byte(stop), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(checkout, "prepare-deepseek-v4-flash-model.sh"), []byte(download), 0o700)
}

func prepareRecipeCheckout(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe) (string, string, error) {
	checkout := recipeCheckout(recipe)
	if !localRecipeIDPattern.MatchString(recipe.ID) || checkout != filepath.Join(recipeRuntimeRoot, recipe.ID) {
		return "", "", errors.New("unsafe recipe working directory")
	}
	if err := os.RemoveAll(checkout); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		return "", "", err
	}
	resolved, err := prepareRecipeSourceAt(ctx, job, recipe, checkout)
	if err != nil {
		return "", "", err
	}
	return checkout, resolved, nil
}

// prepareRecipeSourceAt is the single clone/verify/compatibility-transform path
// shared by Check and Run. Its caller owns and validates checkout so Check can
// use an isolated operation-scoped directory without touching the active
// runtime checkout.
func prepareRecipeSourceAt(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe, checkout string) (string, error) {
	if recipe.Source.URL == "" {
		return "", nil
	}
	if err := runRecipeCommand(ctx, job, "source", "Preparing pinned source", checkout, nil, "git", "init", "--quiet"); err != nil {
		return "", err
	}
	if err := runRecipeCommand(ctx, job, "source", "Connecting recipe source", checkout, nil, "git", "remote", "add", "origin", strings.TrimSuffix(recipe.Source.URL, ".git")+".git"); err != nil {
		return "", err
	}
	if err := runRecipeCommand(ctx, job, "source", "Downloading selected revision", checkout, nil, "git", "fetch", "--depth", "1", "origin", recipe.Source.Revision); err != nil {
		return "", err
	}
	if err := runRecipeCommand(ctx, job, "source", "Checking out reviewed revision", checkout, nil, "git", "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return "", err
	}
	resolved, err := recipeCommandOutput(recipeLocalCommand(ctx, checkout, nil, "git", "rev-parse", "HEAD"))
	if err != nil {
		return "", fmt.Errorf("resolve fetched recipe revision: %w", err)
	}
	resolved = strings.ToLower(strings.TrimSpace(resolved))
	if len(resolved) != 40 && len(resolved) != 64 {
		return "", errors.New("Git returned an invalid resolved recipe revision")
	}
	if _, err := hex.DecodeString(resolved); err != nil {
		return "", errors.New("Git returned an invalid resolved recipe revision")
	}
	if len(recipe.Source.Files) > 0 {
		job.Progress("source", "Verifying source file checksums...", -1, -1)
	}
	if err := verifyRecipeFiles(recipe, checkout); err != nil {
		return "", err
	}
	// Preserve the compatibility patches required by the reviewed DSpark
	// source. Other sources are executed exactly as configured by the user.
	if recipe.Source.URL == localrecipes.DeepSeekDSparkSource && recipe.Source.Revision == localrecipes.DeepSeekDSparkRevision {
		if err := prepareRecipeCompose(checkout, recipe.Engine.RestartPolicy); err != nil {
			return "", err
		}
		if err := prepareRecipeModelPin(recipe, checkout); err != nil {
			return "", err
		}
		if err := prepareRecipeWorkerCheckout(checkout); err != nil {
			return "", err
		}
	} else if recipe.Source.URL == localrecipes.DeepSeekV4Flash1MSource && recipe.Source.Revision == localrecipes.DeepSeekV4Flash1MRevision {
		if err := prepareDeepSeekV4Flash1M(checkout, recipe.Engine.RestartPolicy); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

func recipeCheckCheckout(stateDir string, operation recipeops.Operation) (string, error) {
	if _, err := recipeops.RuntimeName(operation); err != nil {
		return "", err
	}
	root := filepath.Join(stateDir, "recipe-checks")
	checkout := filepath.Join(root, operation.ID)
	if filepath.Dir(checkout) != root {
		return "", errors.New("unsafe recipe check directory")
	}
	return checkout, nil
}

func detectRecipeHCA(iface string) (string, error) {
	if !recipeInterfacePattern.MatchString(iface) {
		return "", errors.New("the Spark fabric interface is invalid")
	}
	entries, err := os.ReadDir(filepath.Join("/sys/class/net", iface, "device", "infiniband"))
	if err != nil || len(entries) == 0 {
		return "", fmt.Errorf("no RoCE adapter was found for %s", iface)
	}
	return entries[0].Name(), nil
}

func writeRecipeRuntime(recipe localrecipes.Recipe, checkout string, cluster sparkcluster.State, requireHealthy bool, ownership *recipeops.Operation) (map[string]string, string, error) {
	selectedCluster, err := selectRecipeClusterWithHealth(recipe, cluster, requireHealthy)
	if err != nil {
		return nil, "", err
	}
	cluster = selectedCluster
	nodes := recipe.Distributed.Nodes
	if nodes > 1 && (cluster.NodeCount != nodes || len(cluster.Nodes) != nodes-1 || (requireHealthy && (!cluster.Healthy || !cluster.WorkerReady))) {
		return nil, "", fmt.Errorf("this recipe requires exactly %d healthy nodes", nodes)
	}
	if nodes > 1 && (len(cluster.LocalIPs) == 0 || len(cluster.LocalLinks) == 0) {
		return nil, "", errors.New("the cluster is missing its local fabric address")
	}
	iface := recipe.Distributed.Interface
	if iface == "" || iface == "auto" {
		if len(cluster.LocalLinks) > 0 {
			iface = cluster.LocalLinks[0]
		}
	}
	if nodes > 1 && iface == "" {
		return nil, "", errors.New("no distributed network interface was selected")
	}
	if !recipeInterfacePattern.MatchString(iface) {
		if nodes > 1 {
			return nil, "", errors.New("the cluster fabric interface is invalid")
		}
	}
	hca := recipe.Distributed.HCA
	if nodes > 1 && requireHealthy && (hca == "" || hca == "auto") {
		var err error
		hca, err = detectRecipeHCA(iface)
		if err != nil {
			return nil, "", err
		}
	}
	key, known := sparkcluster.SSHIdentityPaths()
	home := filepath.Join(checkout, ".cloudless-home")
	sshDir, binDir := filepath.Join(home, ".ssh"), filepath.Join(home, "bin")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return nil, "", err
	}
	aliases := make([]string, 0, len(cluster.Nodes))
	var config strings.Builder
	for index, node := range cluster.Nodes {
		alias := recipe.Distributed.WorkerAlias
		if index > 0 {
			alias += "-" + strconv.Itoa(index+1)
		}
		aliases = append(aliases, alias)
		// Recipe artifacts are large. Once the fabric is configured, carry SSH,
		// image and model-cache traffic over ConnectX instead of management
		// Wi-Fi/Ethernet. HostKeyAlias preserves the key pinned during enrollment.
		host := node.Host
		hostKeyAlias := ""
		if len(node.IPs) > 0 && net.ParseIP(node.IPs[0]) != nil {
			host = node.IPs[0]
			hostKeyAlias = node.Host
		}
		fmt.Fprintf(&config, "Host %s\n  HostName %s\n  User %s\n", alias, host, node.Username)
		if hostKeyAlias != "" {
			fmt.Fprintf(&config, "  HostKeyAlias %s\n", hostKeyAlias)
		}
		fmt.Fprintf(&config, "  IdentityFile %s\n  UserKnownHostsFile %s\n  IdentitiesOnly yes\n  BatchMode yes\n  StrictHostKeyChecking yes\n", key, known)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(config.String()), 0o600); err != nil {
		return nil, "", err
	}
	wrappers := map[string]string{
		"ssh": "#!/bin/sh\nexec /usr/bin/ssh -F " + filepath.Join(sshDir, "config") + " \"$@\"\n",
		"scp": "#!/bin/sh\nexec /usr/bin/scp -F " + filepath.Join(sshDir, "config") + " \"$@\"\n",
		"docker": `#!/bin/sh
case "${1:-}" in
  run|create)
    action="$1"
    shift
    if [ -n "${CLOUDLESS_RECIPE_OPERATION_ID:-}" ]; then
      exec ` + recipeShellQuote(recipeDockerCompatibilityExecutable) + ` "$action" \
        --label "cloudless.recipe.operation=${CLOUDLESS_RECIPE_OPERATION_ID}" \
        --label "cloudless.recipe.revision=${CLOUDLESS_RECIPE_REVISION:-unknown}" "$@"
    fi
    ;;
esac
exec ` + recipeShellQuote(recipeDockerCompatibilityExecutable) + ` "$@"
`,
	}
	if ownership != nil {
		remoteRsync := "env CLOUDLESS_RECIPE_OPERATION_ID=" + recipeShellQuote(ownership.ID) + " CLOUDLESS_RECIPE_REVISION=" + recipeShellQuote(ownership.RecipeRevision) + " /usr/bin/rsync"
		wrappers["rsync"] = "#!/bin/sh\nexec /usr/bin/rsync --rsync-path " + recipeShellQuote(remoteRsync) + " \"$@\"\n"
	} else {
		wrappers["rsync"] = "#!/bin/sh\nexec /usr/bin/rsync \"$@\"\n"
	}
	for name, content := range wrappers {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o700); err != nil {
			return nil, "", err
		}
	}
	masterAddress := "127.0.0.1"
	if len(cluster.LocalIPs) > 0 {
		masterAddress = cluster.LocalIPs[0]
	}
	workerCheckout := checkout
	if len(cluster.Nodes) > 0 {
		var workerErr error
		workerCheckout, workerErr = recipePeerCheckout(recipe.ID, cluster.Nodes[0].Username)
		if workerErr != nil {
			return nil, "", workerErr
		}
	}
	values := map[string]string{
		"WORKER_HOST": strings.Join(aliases, ","), "WORKER_HOSTS": strings.Join(aliases, ","),
		"MASTER_ADDR":        masterAddress,
		"MASTER_PORT":        strconv.Itoa(recipe.Distributed.MasterPort),
		"NCCL_IB_HCA":        hca,
		"NCCL_SOCKET_IFNAME": iface, "NCCL_IB_GID_INDEX": strconv.Itoa(recipe.Distributed.IBGIDIndex),
		"DISTRIBUTED_BACKEND": recipe.Distributed.Backend, "NODE_COUNT": strconv.Itoa(nodes),
		"DSPARK_MODEL": recipe.Model.ID, "DSPARK_MODEL_REVISION": recipe.Model.Revision,
		"SERVED_MODEL_NAME": recipe.Engine.ServedModelName, "DSPARK_VLLM_IMAGE": recipe.Engine.Image,
		"ENGINE_TYPE": recipe.Engine.Type, "ENGINE_IMAGE": recipe.Engine.Image,
		"ENGINE_PORT": strconv.Itoa(recipe.Engine.ContainerPort), "ENGINE_API_PATH": recipe.Engine.APIPath,
		"MAX_MODEL_LEN": strconv.Itoa(recipe.Model.MaxContext), "MAX_NUM_SEQS": strconv.Itoa(recipe.Model.MaxSequences),
		"GPU_MEMORY_UTILIZATION": strconv.FormatFloat(recipe.Model.GPUMemoryUtilization, 'f', -1, 64),
		"TENSOR_PARALLEL_SIZE":   strconv.Itoa(recipe.Model.TensorParallel), "PIPELINE_PARALLEL_SIZE": strconv.Itoa(recipe.Model.PipelineParallel),
		"QUANTIZATION": recipe.Model.Quantization, "DTYPE": recipe.Model.DType, "KV_CACHE_DTYPE": recipe.Model.KVCacheDType,
		"TRUST_REMOTE_CODE": strconv.FormatBool(recipe.Model.TrustRemoteCode), "ENGINE_ARGS": strings.Join(recipe.Engine.Arguments, " "),
	}
	for key, value := range recipe.Runtime.Environment {
		values[key] = value
	}
	// Recipe metadata cannot redirect model downloads to an arbitrary host
	// path. Per-operation staging overrides this value later.
	values["HF_CACHE"] = modelcache.Root()
	if ownership != nil {
		runtimeName, err := recipeops.RuntimeName(*ownership)
		if err != nil {
			return nil, "", err
		}
		values["CLOUDLESS_RECIPE_OPERATION_ID"] = ownership.ID
		values["CLOUDLESS_RECIPE_REVISION"] = ownership.RecipeRevision
		values["COMPOSE_PROJECT_NAME"] = runtimeName
	}
	// A host-networked runtime must be reachable from Cloudless's isolated
	// bridge proxy. Recipes may tune the private port but cannot force a
	// loopback-only bind that strands every OS client behind port 8000.
	values["VLLM_HOST"] = "0.0.0.0"
	if recipe.Runtime.BuildOnce && nodes > 1 {
		values["WORKER_BUILD"] = "0"
	}
	if recipe.Runtime.DownloadOnce && nodes > 1 {
		values["PREPARE_WORKER"] = "0"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var envText strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&envText, "%s=%s\n", key, values[key])
	}
	if err := os.WriteFile(filepath.Join(checkout, ".env.dspark"), []byte(envText.String()), 0o600); err != nil {
		return nil, "", err
	}
	workdir := filepath.Clean(filepath.Join(checkout, recipe.Runtime.WorkingDir))
	if workdir != checkout && !strings.HasPrefix(workdir, checkout+string(os.PathSeparator)) {
		return nil, "", errors.New("runtime working directory escapes the recipe checkout")
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		return nil, "", err
	}
	commandEnvironment := map[string]string{
		"HOME": home, "PATH": binDir + ":" + os.Getenv("PATH"),
		"RSYNC_RSH":       "/usr/bin/ssh -F " + filepath.Join(sshDir, "config"),
		"ENV_FILE":        filepath.Join(checkout, ".env.dspark"),
		"WORKER_CHECKOUT": workerCheckout,
		"API_URL":         fmt.Sprintf("http://127.0.0.1:%d/v1/models", recipe.Engine.ContainerPort),
		"CHAT_URL":        fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", recipe.Engine.ContainerPort),
	}
	for key, value := range values {
		commandEnvironment[key] = value
	}
	return commandEnvironment, workdir, nil
}

func (s *Server) stopManagedEngines(ctx context.Context) error {
	_ = sparkcluster.StopWorker(ctx)
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	for _, candidate := range customengine.All(s.state) {
		if err := s.eng.Remove(ctx, candidate.ContainerName()); err != nil {
			if found, _ := s.eng.Find(ctx, candidate.ContainerName()); found != nil {
				return err
			}
		}
	}
	return nil
}

func runConfiguredRecipeCommand(ctx context.Context, job *jobs.Job, phase, label, dir string, env map[string]string, recipe localrecipes.Recipe, command localrecipes.Command) error {
	if command.Program == "" {
		return nil
	}
	if decision := recipeExecutionPolicyEvaluator(recipe); !decision.Allowed {
		return errors.New(decision.Reason)
	}
	return runRecipeCommand(ctx, job, phase, label, dir, env, command.Program, command.Args...)
}

func validateRecipeLifecycleCommands(recipe localrecipes.Recipe, workdir string) error {
	commands := []struct {
		label   string
		command localrecipes.Command
	}{
		{"build", recipe.Runtime.Lifecycle.Build},
		{"download", recipe.Runtime.Lifecycle.Download},
		{"start", recipe.Runtime.Lifecycle.Start},
		{"stop", recipe.Runtime.Lifecycle.Stop},
	}
	for _, item := range commands {
		if item.command.Program == "" {
			continue
		}
		if err := validateRecipeExecutable(workdir, item.command.Program, true); err != nil {
			return fmt.Errorf("%s command: %w", item.label, err)
		}
		if script, ok := recipeInterpreterScript(item.command); ok {
			if err := validateRecipeExecutable(workdir, script, false); err != nil {
				return fmt.Errorf("%s script: %w", item.label, err)
			}
		}
	}
	return nil
}

func validateRecipeExecutable(workdir, value string, requireExecutable bool) error {
	if !filepath.IsAbs(value) && !strings.ContainsRune(value, os.PathSeparator) {
		if _, err := exec.LookPath(value); err != nil {
			return fmt.Errorf("%q is not installed", value)
		}
		return nil
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(value) && path != workdir && !strings.HasPrefix(path, workdir+string(os.PathSeparator)) {
		return errors.New("relative path escapes the rendered runtime directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%q is unavailable: %w", value, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", value)
	}
	if requireExecutable && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%q is not executable", value)
	}
	return nil
}

func recipeInterpreterScript(command localrecipes.Command) (string, bool) {
	interpreter := filepath.Base(command.Program)
	switch interpreter {
	case "bash", "sh", "python", "python3":
	default:
		return "", false
	}
	for _, argument := range command.Args {
		if argument == "-c" || argument == "-m" {
			return "", false
		}
		if strings.HasPrefix(argument, "-") {
			continue
		}
		if filepath.IsAbs(argument) || strings.ContainsRune(argument, os.PathSeparator) {
			return argument, true
		}
		return "", false
	}
	return "", false
}

func validateRenderedRecipeCompose(ctx context.Context, checkout string, env map[string]string) error {
	composePath := filepath.Join(checkout, "docker-compose.dspark.yml")
	if info, err := os.Stat(composePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return errors.New("rendered recipe Compose configuration is not a regular file")
	}
	_, err := recipeCommandOutput(recipeLocalCommand(ctx, checkout, env, "docker", "compose",
		"--env-file", filepath.Join(checkout, ".env.dspark"), "-f", composePath, "config", "--quiet"))
	if err != nil {
		return fmt.Errorf("render recipe Compose configuration: %w", err)
	}
	return nil
}

func recipeModelCompleteMarker(recipe localrecipes.Recipe, root string) string {
	return filepath.Join(root, ".cloudless-complete", recipeModelArtifactKey(recipe)+".json")
}

func recipeModelArtifactKey(recipe localrecipes.Recipe) string {
	sum := sha256.Sum256([]byte(recipe.Model.ID + "\x00" + recipe.Model.Revision))
	return hex.EncodeToString(sum[:])
}

func (s *Server) runRecipeModelDownload(ctx context.Context, job *jobs.Job, operationID string, recipe localrecipes.Recipe, dir string, env map[string]string, token string) error {
	localNode := localRecipeNodeName()
	if manifest, err := verifyRecipeModelCache(ctx, s.eng, recipe); err == nil {
		job.ProgressBytes("model-ready", "Reusing the verified model cache on "+localNode+".", manifest.Bytes, manifest.Bytes)
		return nil
	}
	stagingVolume := recipeStagingCacheVolume(recipe)
	stagingResource := recipeops.Resource{Kind: "model-staging", ID: stagingVolume, Node: localNode}
	if err := s.claimRecipeResource(operationID, stagingResource); err != nil {
		return fmt.Errorf("record model download staging ownership: %w", err)
	}
	if err := os.MkdirAll(stagingVolume, 0o770); err != nil {
		return fmt.Errorf("create model download staging: %w", err)
	}
	stagedRecipe := recipeWithCacheVolume(recipe, stagingVolume)
	stagedEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		stagedEnv[key] = value
	}
	stagedEnv["HF_CACHE"] = stagingVolume
	if manifest, err := verifyRecipeModelCache(ctx, s.eng, stagedRecipe); err == nil {
		job.ProgressBytes("resuming-model", "A previously verified download is ready to promote.", manifest.Bytes, manifest.Bytes)
		if err := promoteStagedRecipeModel(ctx, s.eng, job, recipe, stagedRecipe, manifest); err != nil {
			return err
		}
		if err := os.RemoveAll(stagingVolume); err == nil {
			_ = s.releaseRecipeResource(operationID, stagingResource)
		}
		return nil
	}
	type modelDownloadResult struct {
		manifest recipeArtifactManifest
		err      error
	}
	result := make(chan modelDownloadResult, 1)
	go func() {
		if err := runConfiguredRecipeCommand(ctx, job, "downloading", "Internet download on "+localNode, dir, stagedEnv, recipe, recipe.Runtime.Lifecycle.Download); err != nil {
			result <- modelDownloadResult{err: err}
			return
		}
		job.Progress("verifying-model", "Verifying every downloaded model file with SHA-256...", -1, -1)
		manifest, err := certifyRecipeModelCache(ctx, s.eng, stagedRecipe)
		result <- modelDownloadResult{manifest: manifest, err: err}
	}()

	metadataCtx, metadataCancel := context.WithTimeout(ctx, 12*time.Second)
	total := huggingFaceModelRevisionBytes(metadataCtx, recipe.Model.ID, recipe.Model.Revision, token)
	metadataCancel()
	started := time.Now()
	root, _ := recipeModelVolumeMountpoint(ctx, s.eng, stagedRecipe)
	firstDone := s.modelRepoBytes(ctx, recipe.Model.ID, root)
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case outcome := <-result:
			if outcome.err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return classifyRecipeDependencyError(outcome.err)
			}
			if err := promoteStagedRecipeModel(ctx, s.eng, job, recipe, stagedRecipe, outcome.manifest); err != nil {
				return err
			}
			if err := os.RemoveAll(stagingVolume); err == nil {
				_ = s.releaseRecipeResource(operationID, stagingResource)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if root == "" {
				root, _ = recipeModelVolumeMountpoint(ctx, s.eng, stagedRecipe)
			}
			done := s.modelRepoBytes(ctx, recipe.Model.ID, root)
			message := "Internet download on " + localNode + " — " + formatDownloadProgress(done, total)
			elapsed := time.Since(started).Seconds()
			if transferred := done - firstDone; total > done && elapsed > 4 && transferred > 0 {
				if eta := recipeProgressETA(total-done, float64(transferred)/elapsed); eta != "" {
					message += " · " + eta
				}
			}
			job.ProgressBytes("downloading", message, done, total)
		}
	}
}

// stopActiveLocalRecipeRuntime is called while EngineMu is held. It makes a
// recipe participate in the normal unload/switch lifecycle even when it is an
// imported compatibility recipe rather than a catalog engine.
func (s *Server) stopActiveLocalRecipeRuntime(parent context.Context, job *jobs.Job) {
	st := s.state.Get()
	if st.LocalRecipeID == "" || st.EngineUnloaded {
		return
	}
	recipe, ok, err := s.recipes.Get(st.LocalRecipeID)
	if err != nil || !ok {
		_ = s.state.SetLocalRecipe("")
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	job.Progress("stopping", "Stopping the active recipe runtime...", -1, -1)
	checkout := recipeCheckout(recipe)
	cluster, _ := sparkcluster.Status(ctx)
	var ownership *recipeops.Operation
	if s.recipeOps != nil {
		if active, ok := s.recipeOps.ActiveForRecipe(recipe.ID); ok {
			ownership = &active
		}
	}
	if env, workdir, runtimeErr := writeRecipeRuntime(recipe, checkout, cluster, false, ownership); runtimeErr == nil {
		stopErr := runConfiguredRecipeCommand(ctx, job, "stopping", "Stop inference", workdir, env, recipe, recipe.Runtime.Lifecycle.Stop)
		_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
		if stopErr == nil {
			stopErr = s.reconcileActiveRecipeResources(ctx, recipe)
		}
		_ = s.closeActiveRecipeOperation(recipe.ID, stopErr)
	}
	_ = s.state.SetLocalRecipe("")
}

func waitRecipeHealth(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe) error {
	address := fmt.Sprintf("%s://%s:%d%s", recipe.Health.Scheme, recipe.Health.Host, recipe.Health.Port, recipe.Health.Path)
	deadline := time.Now().Add(time.Duration(recipe.Health.TimeoutSeconds) * time.Second)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		if err := probeRecipeHealth(ctx, client, address); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("engine health check did not become ready at %s", address)
		}
		job.Progress("health", "Waiting for engine health check at "+address, -1, -1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(recipe.Health.IntervalSeconds) * time.Second):
		}
	}
}

func probeRecipeHealth(ctx context.Context, client *http.Client, address string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func waitRecipePrivateContract(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe) error {
	base := strings.TrimSuffix(recipe.Engine.APIPath, "/")
	if base == "" {
		base = "/v1"
	}
	address := fmt.Sprintf("%s://%s:%d%s/models", recipe.Health.Scheme, recipe.Health.Host, recipe.Health.Port, base)
	deadline := time.Now().Add(time.Duration(recipe.Health.TimeoutSeconds) * time.Second)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	var lastErr error
	for {
		lastErr = probeRecipePrivateContract(ctx, client, address)
		if lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("private OpenAI contract did not become ready at %s: %w", address, lastErr)
		}
		job.Progress("health", "Waiting for the private engine to serve the Cloudless model identity...", -1, -1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(recipe.Health.IntervalSeconds) * time.Second):
		}
	}
}

func probeRecipePrivateContract(ctx context.Context, client *http.Client, address string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return engineModelsResponseError(response.Body)
}

// waitRecipePromotion verifies the endpoint that every Cloudless client will
// actually use. A recipe's own health URL is necessary but not sufficient: a
// host-loopback backend can be healthy while remaining unreachable from the
// bridge-networked stable proxy. Recipes are not persisted as active until this
// contract succeeds, preventing an endless "starting up" state.
func waitRecipePromotion(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := engineEndpointError(probeCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("stable Cloudless inference endpoint is unreachable: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (s *Server) runLocalRecipe(job *jobs.Job, recipe localrecipes.Recipe, operationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(recipe.Runtime.TimeoutMinutes)*time.Minute)
	defer cancel()
	s.registerRecipeJob(recipe.ID, operationID, cancel, job)
	defer s.unregisterRecipeJob(recipe.ID, operationID)
	provision.EngineMu.Lock()
	engineMuHeld := true
	defer func() {
		if engineMuHeld {
			provision.EngineMu.Unlock()
		}
	}()
	operation, ok := s.recipeOps.Get(operationID)
	if !ok {
		s.finishRecipeOperation(job, operationID, errors.New("recipe operation ownership disappeared before preparation"))
		return
	}
	if recipe.Runtime.Lifecycle.Build.Program == "" {
		if operation.Preflight == nil || operation.Preflight.ImageDigest == "" {
			s.finishRecipeOperation(job, operationID, errors.New("checked registry image has no immutable digest; run Check again"))
			return
		}
		pinned, pinErr := immutableRecipeImageReference(recipe.Engine.Image, operation.Preflight.ImageDigest)
		if pinErr != nil {
			s.finishRecipeOperation(job, operationID, pinErr)
			return
		}
		localImage, inspectErr := inspectLocalRecipeImage(ctx, s.eng, pinned)
		if inspectErr != nil {
			if pullErr := s.eng.Pull(ctx, pinned); pullErr != nil {
				s.finishRecipeOperation(job, operationID, fmt.Errorf("restore checked runtime image: %w", errors.Join(inspectErr, pullErr)))
				return
			}
			localImage, inspectErr = inspectLocalRecipeImage(ctx, s.eng, pinned)
		}
		if inspectErr != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("inspect restored checked runtime image: %w", inspectErr))
			return
		}
		if localImage.Digest != operation.Preflight.ImageDigest || !recipeImageDigestPattern.MatchString(localImage.LocalID) {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("checked registry image identity changed: expected %s, found %s", operation.Preflight.ImageDigest, localImage.Digest))
			return
		}
		operation, err := s.bindPreparedRecipeImage(operationID, localImage.LocalID, localImage.LocalID)
		if err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		recipe.Engine.Image = operation.PreparedImageReference
	}
	localNode := localRecipeNodeName()
	resources := []recipeops.Resource{
		{Kind: "checkout", ID: recipeCheckout(recipe), Node: localNode},
		{Kind: "container-set", ID: operationID, Node: localNode},
		{Kind: "image", ID: recipe.Engine.Image, Node: localNode},
		{Kind: "model-cache", ID: recipe.Model.ID + "@" + recipe.Model.Revision, Node: localNode},
		{Kind: "private-port", ID: strconv.Itoa(recipe.Engine.ContainerPort), Node: localNode},
		{Kind: "process-set", ID: operationID, Node: localNode},
	}
	if recipe.Distributed.Nodes > 1 {
		resources = append(resources, recipeops.Resource{Kind: "rendezvous-port", ID: strconv.Itoa(recipe.Distributed.MasterPort), Node: localNode})
	}
	for _, resource := range resources {
		if err := s.claimRecipeResource(operationID, resource); err != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("record recipe resource ownership: %w", err))
			return
		}
	}
	for _, command := range recipe.Runtime.Prerequisites {
		if _, err := exec.LookPath(command); err != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("recipe prerequisite %q is not installed", command))
			return
		}
	}
	cluster, err := sparkcluster.Status(ctx)
	if err != nil && recipe.Distributed.Nodes > 1 {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	cluster, err = selectRecipeCluster(recipe, cluster)
	if err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("select recipe cluster: %w", err))
		return
	}
	totalSteps := 8
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.BuildOnce {
		totalSteps++
	}
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.DownloadOnce {
		totalSteps++
	}
	step := 0
	job.Progress("source", "Preparing the recipe source and runtime...", step, totalSteps)
	if err := s.recipeBoundary(operationID, recipeBoundarySourceCheckout); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	checkout, resolvedSource, err := prepareRecipeCheckout(ctx, job, recipe)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	operation, ok = s.recipeOps.Get(operationID)
	if resolvedSource != "" {
		operation, err = s.bindRecipeSourceRevision(operationID, resolvedSource)
		ok = err == nil
	}
	if err != nil || !ok {
		if err == nil {
			err = errors.New("recipe operation ownership disappeared")
		}
		s.finishRecipeOperation(job, operationID, fmt.Errorf("bind resolved recipe source: %w", err))
		return
	}
	env, workdir, err := writeRecipeRuntime(recipe, checkout, cluster, true, &operation)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	runtimeName, err := recipeops.RuntimeName(operation)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "compose-project", ID: runtimeName, Node: localNode}); err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("record compose project ownership: %w", err))
		return
	}
	// Reuse the account connected in Model Manager for recipe downloads. Add it
	// only to the command environment, after the on-disk recipe environment was
	// written, so the credential is never copied into the checkout or to peers.
	hfToken := ""
	if token, tokenErr := s.state.HuggingFaceToken(); tokenErr == nil && token != "" {
		hfToken = token
		env["HF_TOKEN"] = token
	}
	var peers []recipePeer
	if recipe.Distributed.Nodes > 1 {
		peers, err = recipeDistributionPeers(recipe, cluster, env)
		if err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		for _, peer := range peers {
			peerResources := []recipeops.Resource{
				{Kind: "checkout", ID: peer.Checkout, Node: peer.Name, Locator: peer.Alias},
				{Kind: "compose-project", ID: runtimeName, Node: peer.Name, Locator: peer.Alias},
				{Kind: "container-set", ID: operationID, Node: peer.Name, Locator: peer.Alias},
				{Kind: "image", ID: recipe.Engine.Image, Node: peer.Name, Locator: peer.Alias},
				{Kind: "model-cache", ID: recipe.Model.ID + "@" + recipe.Model.Revision, Node: peer.Name, Locator: peer.Alias},
				{Kind: "private-port", ID: strconv.Itoa(recipe.Engine.ContainerPort), Node: peer.Name, Locator: peer.Alias},
				{Kind: "process-set", ID: operationID, Node: peer.Name, Locator: peer.Alias},
				{Kind: "rendezvous-port", ID: strconv.Itoa(recipe.Distributed.MasterPort), Node: peer.Name, Locator: peer.Alias},
			}
			for _, resource := range peerResources {
				if err := s.claimRecipeResource(operationID, resource); err != nil {
					s.finishRecipeOperation(job, operationID, fmt.Errorf("record %s ownership: %w", peer.Name, err))
					return
				}
			}
		}
	}
	step++
	job.Progress("building", "Preparing the exact inference runtime verified by Check...", step, totalSteps)
	reusePreparedImage := false
	if recipe.Runtime.Lifecycle.Build.Program != "" && operation.Preflight != nil && operation.Preflight.ImageDigest != "" {
		existingImage, inspectErr := inspectLocalRecipeImage(ctx, s.eng, recipe.Engine.Image)
		reusePreparedImage = reusablePreparedRecipeImage(operation.Preflight.ImageDigest, existingImage, inspectErr)
	}
	if !reusePreparedImage {
		if err := s.recipeBoundary(operationID, recipeBoundaryImagePrepare); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		if err := runConfiguredRecipeCommand(ctx, job, "building", "Build", workdir, env, recipe, recipe.Runtime.Lifecycle.Build); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	if recipe.Runtime.Lifecycle.Build.Program != "" {
		preparedImage, inspectErr := inspectLocalRecipeImage(ctx, s.eng, recipe.Engine.Image)
		if inspectErr != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("inspect prepared runtime image: %w", inspectErr))
			return
		}
		expected := ""
		if operation.Preflight != nil {
			expected = operation.Preflight.ImageDigest
		}
		if expected != "" && preparedImage.Digest != expected {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("custom runtime image differs from Check: expected %s, prepared %s", expected, preparedImage.Digest))
			return
		}
		if !recipeImageDigestPattern.MatchString(preparedImage.LocalID) {
			s.finishRecipeOperation(job, operationID, errors.New("custom runtime image has no immutable local image ID"))
			return
		}
		operation, err = s.bindPreparedRecipeImage(operationID, preparedImage.LocalID, preparedImage.LocalID)
		if err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		recipe.Engine.Image = operation.PreparedImageReference
		if err := writePreparedRecipeImageReference(checkout, env, recipe.Engine.Image); err != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("pin prepared runtime image in generated configuration: %w", err))
			return
		}
		if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "image", ID: recipe.Engine.Image, Node: localNode}); err != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("record immutable image ownership: %w", err))
			return
		}
	}
	step++
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.BuildOnce {
		job.Progress("syncing-image", "Preparing to send the completed runtime over the Spark fabric...", step, totalSteps)
		if err := s.recipeBoundary(operationID, recipeBoundaryPeerTransfer); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		if err := syncRecipeCheckout(ctx, job, checkout, env, peers); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		if err := distributeRecipeImage(ctx, s.eng, job, recipe, workdir, env, peers); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		for _, peer := range peers {
			if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "image", ID: recipe.Engine.Image, Node: peer.Name, Locator: peer.Alias}); err != nil {
				s.finishRecipeOperation(job, operationID, fmt.Errorf("record immutable image ownership on %s: %w", peer.Name, err))
				return
			}
		}
		step++
	}
	job.ProgressBytes("downloading", "Connecting "+localNode+" to Hugging Face over the internet...", 0, 0)
	job.Progress("downloading", "Downloading model weights from Hugging Face to "+localNode+". The direct Spark cable is not used during this stage.", step, totalSteps)
	if err := s.recipeBoundary(operationID, recipeBoundaryModelDownload); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.runRecipeModelDownload(ctx, job, operationID, recipe, workdir, env, hfToken); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	step++
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.DownloadOnce {
		job.Progress("syncing-model", "Preparing to send the model over the Spark fabric...", step, totalSteps)
		if err := s.recipeBoundary(operationID, recipeBoundaryModelTransfer); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		if err := distributeRecipeModel(ctx, s.eng, job, recipe, workdir, env, peers); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
		step++
	}
	job.Progress("revalidating", "Rechecking the image, GPUs, cluster fabric, and ports before switching models...", step, totalSteps)
	operation, ok = s.recipeOps.Get(operationID)
	if !ok {
		s.finishRecipeOperation(job, operationID, errors.New("recipe operation ownership disappeared before switch"))
		return
	}
	if err := s.revalidateRecipeBeforeSwitch(ctx, recipe, operation, checkout, env); err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("pre-switch validation failed: %w", err))
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhasePrepared, nil); err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("persist prepared recipe state: %w", err))
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseSwitching, nil); err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("persist recipe switch state: %w", err))
		return
	}
	rollbackFailure := func(cause error) {
		if engineMuHeld {
			provision.EngineMu.Unlock()
			engineMuHeld = false
		}
		s.finishRecipeOperation(job, operationID, s.rollbackFailedRecipe(job, operation, recipe, workdir, env, cause))
	}
	// Keep the current model available during the long image build and model
	// download. Only release it when the reviewed runtime is ready to start.
	job.ProgressBytes("stopping", "Switching from the current Cloudless model...", 0, 0)
	job.Progress("stopping", "Switching from the current Cloudless model...", step, totalSteps)
	if err := s.recipeBoundary(operationID, recipeBoundaryEngineStop); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.stopManagedEngines(ctx); err != nil {
		rollbackFailure(err)
		return
	}
	step++
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseStarting, nil); err != nil {
		rollbackFailure(fmt.Errorf("persist recipe start state: %w", err))
		return
	}
	job.Progress("starting", "Starting the inference recipe...", step, totalSteps)
	if err := s.recipeBoundary(operationID, recipeBoundaryRuntimeStart); err != nil {
		rollbackFailure(err)
		return
	}
	startErr := runConfiguredRecipeCommand(ctx, job, "starting", "Start inference", workdir, env, recipe, recipe.Runtime.Lifecycle.Start)
	if startErr != nil {
		rollbackFailure(startErr)
		return
	}
	step++
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseVerifying, nil); err != nil {
		rollbackFailure(fmt.Errorf("persist recipe verification state: %w", err))
		return
	}
	job.Progress("health", "Checking that the configured engine is ready...", step, totalSteps)
	if err := s.recipeBoundary(operationID, recipeBoundaryPrivateHealth); err != nil {
		rollbackFailure(err)
		return
	}
	if err := waitRecipeHealth(ctx, job, recipe); err != nil {
		rollbackFailure(err)
		return
	}
	if err := waitRecipePrivateContract(ctx, job, recipe); err != nil {
		rollbackFailure(err)
		return
	}
	step++
	job.Progress("connecting", "Connecting Cloudless apps to the recipe runtime...", step, totalSteps)
	proxyImage := s.infraImage(ctx, "socat", catalog.SocatImage)
	if err := s.recipeBoundary(operationID, recipeBoundaryProxyPull); err != nil {
		rollbackFailure(err)
		return
	}
	if err := s.eng.Pull(ctx, proxyImage); err != nil {
		rollbackFailure(err)
		return
	}
	spec := sparkcluster.ProxySpecTarget(recipe.Engine.ProxyHost, recipe.Engine.ContainerPort)
	spec.Image = proxyImage
	if err := s.recipeBoundary(operationID, recipeBoundaryProxyCreate); err != nil {
		rollbackFailure(err)
		return
	}
	proxyID, err := s.eng.Run(ctx, spec)
	if err != nil {
		rollbackFailure(err)
		return
	}
	if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "container", ID: proxyID, Node: localNode}); err != nil {
		_ = s.eng.Remove(ctx, spec.Name)
		rollbackFailure(fmt.Errorf("record stable proxy ownership: %w", err))
		return
	}
	if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "stable-port", ID: strconv.Itoa(recipeStablePort), Node: localNode}); err != nil {
		_ = s.eng.Remove(ctx, spec.Name)
		rollbackFailure(fmt.Errorf("record stable port ownership: %w", err))
		return
	}
	job.Progress("connecting", "Verifying the permanent Cloudless inference endpoint...", step, totalSteps)
	if err := s.recipeBoundary(operationID, recipeBoundaryPromotion); err != nil {
		rollbackFailure(err)
		return
	}
	if err := waitRecipePromotion(ctx); err != nil {
		rollbackFailure(fmt.Errorf("%w; the recipe backend must be reachable from its configured proxy host %q on port %d (host-networked servers must bind 0.0.0.0, not 127.0.0.1)", err, recipe.Engine.ProxyHost, recipe.Engine.ContainerPort))
		return
	}
	if err := s.recipeBoundary(operationID, recipeBoundaryStateCommit); err != nil {
		rollbackFailure(err)
		return
	}
	mode := "local"
	if recipe.Distributed.Nodes > 1 {
		mode = "cluster"
	}
	activeRuntime := s.state.Get().InferenceRuntime()
	activeRuntime.Engine = recipe.Engine.Type
	activeRuntime.Model = recipe.Model.ID
	activeRuntime.ExecutionMode = mode
	activeRuntime.LocalRecipeID = recipe.ID
	activeRuntime.EngineUnloaded = false
	if err := s.state.CommitInferenceRuntime(activeRuntime); err != nil {
		rollbackFailure(fmt.Errorf("atomically commit active recipe state: %w", err))
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseActive, nil); err != nil {
		rollbackFailure(fmt.Errorf("persist active recipe state: %w", err))
		return
	}
	job.Progress("ready", "Recipe is running through the normal Cloudless API.", totalSteps, totalSteps)
	job.Succeed(recipe.Engine.ServedModelName)
	s.pruneRecipeOperations()
}

func finishRecipeJob(job *jobs.Job, err error) {
	if errors.Is(err, context.Canceled) {
		job.Cancel()
		return
	}
	job.Fail(err)
}

func (s *Server) finishRecipeOperation(job *jobs.Job, operationID string, err error) {
	cleanupVerified := true
	abortRequested := errors.Is(err, context.Canceled)
	if s.recipeOps != nil {
		if operation, ok := s.recipeOps.Get(operationID); ok && operation.Kind == recipeops.KindRun {
			abortRequested = abortRequested || operation.Phase == recipeops.PhaseStopping
			reconcileCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			var cleanupErr error
			if abortRequested {
				cleanupErr = s.cleanupInterruptedRecipe(reconcileCtx, job, operation, operation.RecipeSnapshot)
			}
			remaining, reconcileErr := s.releaseMissingRecipeResources(reconcileCtx, operationID, operation.RecipeSnapshot)
			cancel()
			if len(remaining) > 0 {
				cleanupVerified = false
				reconcileErr = errors.Join(reconcileErr, fmt.Errorf("%d runtime cleanup obligation(s) remain", len(remaining)))
			}
			if cleanupErr != nil || reconcileErr != nil {
				cleanupVerified = false
			}
			err = errors.Join(err, cleanupErr, reconcileErr)
		}
	}
	if abortRequested && !errors.Is(err, context.Canceled) {
		err = errors.Join(context.Canceled, err)
	}
	phase := recipeops.PhaseFailed
	if abortRequested && cleanupVerified {
		phase = recipeops.PhaseAborted
	}
	if transitionErr := s.transitionRecipeOperation(operationID, phase, err); transitionErr != nil {
		err = errors.Join(err, fmt.Errorf("persist recipe operation result: %w", transitionErr))
	}
	finishRecipeJob(job, err)
	s.pruneRecipeOperations()
	if operation, ok := s.recipeOps.Get(operationID); ok {
		if _, finalizeErr := s.finalizeRecipeTombstone(operation.RecipeID); finalizeErr != nil {
			log.Printf("[recipe-tombstone] finalize %s: %v", operation.RecipeID, finalizeErr)
		}
	}
}

func (s *Server) checkLocalRecipe(job *jobs.Job, recipe localrecipes.Recipe, operationID string) {
	checkTimeout := 10 * time.Minute
	// Check builds custom runtimes now so the exact runnable image can be
	// attested before Run. Respect the recipe's reviewed runtime allowance for
	// that potentially expensive build instead of aborting it after ten minutes.
	if recipe.Runtime.Lifecycle.Build.Program != "" {
		checkTimeout = max(checkTimeout, time.Duration(recipe.Runtime.TimeoutMinutes)*time.Minute)
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	job.Progress("checking-cluster", "Reading the connected cluster topology and accelerator availability...", 0, 10)
	cluster, err := sparkcluster.Status(ctx)
	if err != nil && recipe.Distributed.Nodes > 1 {
		s.failRecipeCheck(job, operationID, "topology", "Cluster topology is unavailable", err)
		return
	}
	availableNodes := max(1, cluster.NodeCount)
	cluster, err = selectRecipeCluster(recipe, cluster)
	if err != nil {
		s.failRecipeCheck(job, operationID, "topology", "Requested Spark placement is unavailable", err)
		return
	}
	if err := s.recordRecipeCheck(operationID, "topology", recipeops.CheckPass, "Requested cluster topology is available", "", map[string]string{
		"requiredNodes": strconv.Itoa(recipe.Distributed.Nodes), "availableNodes": strconv.Itoa(availableNodes),
		"selectedWorkers": strings.Join(recipeClusterNodeIdentities(cluster), ","),
	}); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-topology", "Comparing the recipe's node requirements with this cluster...", 1, 10)
	operation, ok := s.recipeOps.Get(operationID)
	if !ok {
		s.finishRecipeOperation(job, operationID, errors.New("recipe check ownership disappeared"))
		return
	}
	checkout, err := recipeCheckCheckout(s.state.Dir(), operation)
	if err != nil {
		s.failRecipeCheck(job, operationID, "source", "Recipe source workspace is unsafe", err)
		return
	}
	resource := recipeops.Resource{Kind: "staging-checkout", ID: checkout, Node: localRecipeNodeName()}
	if err := s.claimRecipeResource(operationID, resource); err != nil {
		s.failRecipeCheck(job, operationID, "source", "Recipe source workspace could not be journaled", fmt.Errorf("record check workspace ownership: %w", err))
		return
	}
	defer func() {
		if err := os.RemoveAll(checkout); err == nil {
			_ = s.releaseRecipeResource(operationID, resource)
		}
	}()
	if err := os.RemoveAll(checkout); err != nil {
		s.failRecipeCheck(job, operationID, "source", "Recipe source workspace could not be reset", fmt.Errorf("reset recipe check workspace: %w", err))
		return
	}
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		s.failRecipeCheck(job, operationID, "source", "Recipe source workspace could not be created", fmt.Errorf("create recipe check workspace: %w", err))
		return
	}
	if err := s.recipeBoundary(operationID, recipeBoundarySourceCheckout); err != nil {
		s.failRecipeCheck(job, operationID, "source", "Pinned recipe source failed validation", err)
		return
	}
	job.Progress("checking-source", "Fetching and verifying the exact pinned recipe source...", 2, 10)
	resolvedSource, err := prepareRecipeSourceAt(ctx, job, recipe, checkout)
	if err != nil {
		s.failRecipeCheck(job, operationID, "source", "Pinned recipe source failed validation", fmt.Errorf("check pinned recipe source: %w", err))
		return
	}
	if resolvedSource != "" {
		operation, err = s.bindRecipeSourceRevision(operationID, resolvedSource)
		if err != nil {
			s.failRecipeCheck(job, operationID, "source", "Resolved source revision could not be journaled", fmt.Errorf("bind checked recipe source: %w", err))
			return
		}
	}
	sourceValues := map[string]string{}
	if resolvedSource != "" {
		sourceValues["commit"] = resolvedSource
	}
	if err := s.recordRecipeCheck(operationID, "source", recipeops.CheckPass, "Pinned recipe source and reviewed files are valid", "", sourceValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-image", "Resolving the inference image and verifying this machine architecture...", 3, 10)
	var imageResult recipeImagePreflight
	if recipe.Runtime.Lifecycle.Build.Program == "" {
		imageResult, err = inspectRecipeRegistryImage(ctx, s.eng, recipe.Engine.Image)
		if err != nil {
			s.failRecipeCheck(job, operationID, "image", "Inference image is unavailable for this architecture", err)
			return
		}
		if recordErr := s.recordRecipeCheck(operationID, "image", recipeops.CheckPass,
			"Inference image has a compatible immutable manifest", "", imageResult.checkValues()); recordErr != nil {
			s.finishRecipeOperation(job, operationID, recordErr)
			return
		}
	}
	job.Progress("checking-runtime", "Rendering the exact Cloudless-managed runtime configuration...", 4, 10)
	env, workdir, err := writeRecipeRuntime(recipe, checkout, cluster, true, &operation)
	if err != nil {
		s.failRecipeCheck(job, operationID, "runtime", "Runtime configuration could not be rendered", err)
		return
	}
	if err := validateRecipeLifecycleCommands(recipe, workdir); err != nil {
		s.failRecipeCheck(job, operationID, "runtime", "Rendered lifecycle commands are invalid", fmt.Errorf("validate rendered recipe commands: %w", err))
		return
	}
	if err := validateRenderedRecipeCompose(ctx, checkout, env); err != nil {
		s.failRecipeCheck(job, operationID, "runtime", "Rendered Compose configuration is invalid", err)
		return
	}
	if recipe.Runtime.Lifecycle.Build.Program != "" {
		job.Progress("checking-image-build", "Building the reviewed custom runtime so its exact output can be verified...", 4, 10)
		if err := s.recipeBoundary(operationID, recipeBoundaryImagePrepare); err != nil {
			s.failRecipeCheck(job, operationID, "image", "Custom runtime image build failed", err)
			return
		}
		if err := runConfiguredRecipeCommand(ctx, job, "checking-image-build", "Build runtime for Check", workdir, env, recipe, recipe.Runtime.Lifecycle.Build); err != nil {
			s.failRecipeCheck(job, operationID, "image", "Custom runtime image build failed", err)
			return
		}
		imageResult, err = inspectLocalRecipeImage(ctx, s.eng, recipe.Engine.Image)
		if err != nil {
			s.failRecipeCheck(job, operationID, "image", "Built runtime image is invalid", err)
			return
		}
		if !recipeImageDigestPattern.MatchString(imageResult.LocalID) {
			s.failRecipeCheck(job, operationID, "image", "Built runtime image has no immutable local image ID", errors.New("Docker did not report an immutable image configuration ID"))
			return
		}
		if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "image", ID: imageResult.LocalID, Node: localRecipeNodeName()}); err != nil {
			s.failRecipeCheck(job, operationID, "image", "Built runtime image ownership could not be journaled", err)
			return
		}
		if err := s.recordRecipeCheck(operationID, "image", recipeops.CheckPass,
			"Custom runtime was built and bound to a compatible immutable image", "", imageResult.checkValues()); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.Lifecycle.Build.Program == "" {
		peers, peerErr := recipeDistributionPeers(recipe, cluster, env)
		if peerErr != nil {
			s.failRecipeCheck(job, operationID, "topology", "Cluster peers could not be resolved for image verification", peerErr)
			return
		}
		imageValues := imageResult.checkValues()
		for _, peer := range peers {
			peerDigest, peerErr := inspectPeerRecipeRegistryDigest(ctx, checkout, env, peer, recipe.Engine.Image)
			if peerErr != nil {
				s.failRecipeCheck(job, operationID, "image", "A cluster node could not resolve the inference image", peerErr)
				return
			}
			if peerDigest != imageResult.Digest {
				s.failRecipeCheck(job, operationID, "image", "Cluster nodes resolved different inference images",
					fmt.Errorf("%s resolved %s while this node resolved %s", peer.Name, peerDigest, imageResult.Digest))
				return
			}
			imageValues["peer."+peer.Name+".digest"] = peerDigest
		}
		if err := s.recordRecipeCheck(operationID, "image", recipeops.CheckPass,
			"Every cluster node resolved the same compatible image digest", "", imageValues); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	preparedCheckImage, err := s.prepareRecipeImageForPreflight(ctx, job, operationID, recipe, cluster, checkout, env, imageResult)
	if err != nil {
		s.failRecipeCheck(job, operationID, "image", "Verified runtime image could not be prepared on every node", err)
		return
	}
	if err := s.recordRecipeCheck(operationID, "runtime", recipeops.CheckPass, "Runtime environment and lifecycle commands render successfully", "", map[string]string{
		"workingDirectory": workdir, "enginePort": strconv.Itoa(recipe.Engine.ContainerPort),
	}); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-capacity", "Measuring model, runtime, staging, and safety-margin storage on every node...", 5, 10)
	metadataCtx, metadataCancel := context.WithTimeout(ctx, 25*time.Second)
	hfToken := ""
	if token, tokenErr := s.state.HuggingFaceToken(); tokenErr == nil {
		hfToken = token
	}
	modelBytes, sizeErr := recipeModelPreflightBytes(metadataCtx, recipe, hfToken)
	metadataCancel()
	if sizeErr != nil {
		s.unknownRecipeCheck(job, operationID, "capacity", "Model storage requirement could not be determined", sizeErr)
		return
	}
	runtimeBytes := imageResult.CompressedBytes
	if recipe.Runtime.Lifecycle.Build.Program == "" {
		if runtimeBytes > math.MaxInt64/recipeRegistryExpansionFactor {
			s.unknownRecipeCheck(job, operationID, "capacity", "Runtime image storage requirement is invalid", errors.New("runtime image size overflowed"))
			return
		}
		runtimeBytes *= recipeRegistryExpansionFactor
	}
	if runtimeBytes <= 0 {
		runtimeBytes = recipeRuntimeEstimate(recipe)
	}
	if runtimeBytes <= 0 {
		s.unknownRecipeCheck(job, operationID, "capacity", "Runtime image storage requirement could not be determined",
			errors.New("the recipe needs a reviewed runtime size estimate or an inspectable image"))
		return
	}
	capacityPath := s.modelVolumePath(ctx)
	if capacityPath == "" {
		capacityPath = checkout
	}
	availableBytes, measuredAt, capacityErr := filesystemAvailableBytes(capacityPath)
	if capacityErr != nil {
		s.unknownRecipeCheck(job, operationID, "capacity", "Local storage availability could not be measured", capacityErr)
		return
	}
	_, localImageErr := inspectLocalRecipeImage(ctx, s.eng, recipe.Engine.Image)
	localCapacity, capacityErr := calculateRecipeCapacity(modelBytes, runtimeBytes, directoryBytes(checkout), availableBytes,
		recipeCachedModelReady(ctx, s.eng, recipe), localImageErr == nil)
	if capacityErr != nil {
		s.unknownRecipeCheck(job, operationID, "capacity", "Local storage requirement could not be calculated", capacityErr)
		return
	}
	capacityValues := localCapacity.values("local.")
	capacityValues["local.filesystem"] = measuredAt
	if localCapacity.RequiredBytes > localCapacity.AvailableBytes {
		s.failRecipeCheck(job, operationID, "capacity", "This node does not have enough free storage",
			fmt.Errorf("requires %d bytes including reserve; %d bytes are available", localCapacity.RequiredBytes, localCapacity.AvailableBytes))
		return
	}
	if recipe.Distributed.Nodes > 1 {
		peers, peerErr := recipeDistributionPeers(recipe, cluster, env)
		if peerErr != nil {
			s.failRecipeCheck(job, operationID, "capacity", "Cluster peer storage could not be measured", peerErr)
			return
		}
		for _, peer := range peers {
			peerAvailable, peerErr := peerRecipeAvailableBytes(ctx, checkout, env, peer)
			if peerErr != nil {
				s.unknownRecipeCheck(job, operationID, "capacity", "A cluster node did not report storage availability", fmt.Errorf("%s: %w", peer.Name, peerErr))
				return
			}
			peerCapacity, peerErr := calculateRecipeCapacity(modelBytes, runtimeBytes, directoryBytes(checkout), peerAvailable, false, false)
			if peerErr != nil {
				s.unknownRecipeCheck(job, operationID, "capacity", "A cluster node storage requirement could not be calculated", fmt.Errorf("%s: %w", peer.Name, peerErr))
				return
			}
			for key, value := range peerCapacity.values("peer." + peer.Name + ".") {
				capacityValues[key] = value
			}
			if peerCapacity.RequiredBytes > peerCapacity.AvailableBytes {
				s.failRecipeCheck(job, operationID, "capacity", "A cluster node does not have enough free storage",
					fmt.Errorf("%s requires %d bytes including reserve; %d bytes are available", peer.Name, peerCapacity.RequiredBytes, peerCapacity.AvailableBytes))
				return
			}
		}
	}
	if err := s.recordRecipeCheck(operationID, "capacity", recipeops.CheckPass,
		"Every node has enough storage for preparation and a safety reserve", "", capacityValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-accelerators", "Verifying NVIDIA drivers, memory, compute capability, and node consistency...", 6, 10)
	acceleratorValues, acceleratorErr := s.preflightRecipeAccelerators(ctx, recipe, cluster, checkout, env, modelBytes)
	if acceleratorErr != nil {
		s.failRecipeCheck(job, operationID, "accelerators", "Accelerator requirements are not satisfied", acceleratorErr)
		return
	}
	if err := s.recordRecipeCheck(operationID, "accelerators", recipeops.CheckPass,
		"Every selected node satisfies the accelerator contract", "", acceleratorValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-ports", "Checking private, distributed, and stable API port ownership on every node...", 7, 10)
	portValues, portErr := s.preflightRecipePorts(ctx, recipe, cluster, checkout, env)
	if portErr != nil {
		s.failRecipeCheck(job, operationID, "ports", "A required network port is unavailable or inconsistent", portErr)
		return
	}
	if err := s.recordRecipeCheck(operationID, "ports", recipeops.CheckPass,
		"Required runtime ports are available or owned by the active recipe", "", portValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-fabric", "Testing peer login, transfer integrity, and distributed TCP bootstrap...", 8, 10)
	fabricValues, fabricErr := preflightRecipeFabric(ctx, recipe, cluster, checkout, env, preparedCheckImage)
	if fabricErr != nil {
		s.failRecipeCheck(job, operationID, "fabric", "Cluster fabric smoke test failed", fabricErr)
		return
	}
	if err := s.recordRecipeCheck(operationID, "fabric", recipeops.CheckPass,
		"Peer access, bounded transfer, TCP bootstrap, and NCCL collective succeeded", "", fabricValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-tools", "Checking required commands without launching the model...", 9, 10)
	for _, command := range recipe.Runtime.Prerequisites {
		if _, err := exec.LookPath(command); err != nil {
			s.failRecipeCheck(job, operationID, "tools", "A required host command is unavailable", fmt.Errorf("recipe prerequisite %q is not installed", command))
			return
		}
	}
	if err := s.recordRecipeCheck(operationID, "tools", recipeops.CheckPass, "Required host commands are installed", "", map[string]string{
		"count": strconv.Itoa(len(recipe.Runtime.Prerequisites)),
	}); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("checking-contract", "Confirming the pinned revision and rendered launch contract...", 9, 10)
	if recipe.Source.URL != "" && recipe.Source.Revision == "" {
		s.failRecipeCheck(job, operationID, "contract", "Recipe source is not pinned", errors.New("a pinned source revision is required"))
		return
	}
	contractValues, contractErr := s.probePreparedRecipeContract(ctx, operationID, checkout, workdir, env, recipe, preparedCheckImage)
	if contractErr != nil {
		s.failRecipeCheck(job, operationID, "contract", "Prepared runtime does not satisfy the Cloudless API contract", contractErr)
		return
	}
	contractValues["stablePort"] = "8000"
	if err := s.recordRecipeCheck(operationID, "contract", recipeops.CheckPass, "Rendered runtime and prepared image preserve the Cloudless API contract", "", contractValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	platformFingerprint, err := recipePlatformFingerprint(acceleratorValues)
	if err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("fingerprint recipe platform: %w", err))
		return
	}
	clusterFingerprint, err := recipeClusterFingerprint(cluster)
	if err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("fingerprint recipe cluster: %w", err))
		return
	}
	operation, err = s.recipeOps.BindPreflight(operationID, imageResult.Digest, platformFingerprint, clusterFingerprint)
	if err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("persist hash-bound recipe preflight: %w", err))
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhasePrepared, nil); err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("persist checked recipe state: %w", err))
		return
	}
	if operation.Preflight != nil && operation.Preflight.Launchable {
		job.Progress("validated", "Cloudless verified this recipe and bound the evidence to the current machine and cluster.", 10, 10)
		job.Succeed("validated")
		s.pruneRecipeOperations()
		return
	}
	job.Progress("validated-with-warnings", "Requirements were checked, but one or more launch checks remain indeterminate.", 10, 10)
	job.Succeed("validated-with-warnings")
	s.pruneRecipeOperations()
}

func (s *Server) stopLocalRecipe(job *jobs.Job, recipe localrecipes.Recipe, operationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	provision.EngineMu.Lock()
	defer provision.EngineMu.Unlock()
	checkout := recipeCheckout(recipe)
	job.Progress("stopping", "Running the configured stop step...", 0, 1)
	cluster, err := sparkcluster.Status(ctx)
	if err == nil || recipe.Distributed.Nodes == 1 {
		var ownership *recipeops.Operation
		if s.recipeOps != nil {
			if active, ok := s.recipeOps.ActiveForRecipe(recipe.ID); ok {
				ownership = &active
			}
		}
		if env, workdir, envErr := writeRecipeRuntime(recipe, checkout, cluster, false, ownership); envErr != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("prepare recipe stop: %w", envErr))
			return
		} else if stopErr := runConfiguredRecipeCommand(ctx, job, "stopping", "Stop inference", workdir, env, recipe, recipe.Runtime.Lifecycle.Stop); stopErr != nil {
			s.finishRecipeOperation(job, operationID, fmt.Errorf("stop recipe runtime: %w", stopErr))
			return
		}
	} else {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("read cluster before stopping recipe: %w", err))
		return
	}
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	stoppedRuntime := s.state.Get().InferenceRuntime()
	stoppedRuntime.LocalRecipeID = ""
	stoppedRuntime.EngineUnloaded = true
	if err := s.state.CommitInferenceRuntime(stoppedRuntime); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	cleanupErr := s.reconcileActiveRecipeResources(ctx, recipe)
	if err := s.closeActiveRecipeOperation(recipe.ID, cleanupErr); err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("persist active recipe shutdown: %w", err))
		return
	}
	if cleanupErr != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("recipe stopped but cleanup is incomplete: %w", cleanupErr))
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseStopped, nil); err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("persist stopped recipe state: %w", err))
		return
	}
	job.Progress("stopped", "Recipe stopped. The downloaded model remains cached.", 1, 1)
	job.Succeed("")
	s.pruneRecipeOperations()
}
