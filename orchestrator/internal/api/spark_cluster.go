package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
	"github.com/cloudless/orchestrator/internal/state"
)

// localModelAfterClusterDisconnect keeps the selected model when it has a
// known single-machine memory fit. Cluster-only or unknown models fall back to
// the platform default instead of leaving the local engine in a crash loop.
func localModelAfterClusterDisconnect(st state.State, memoryGB int) (string, bool) {
	current := resolveModel(st.Model)
	model, known := models.Get(current)
	if !known {
		model, known = st.CustomModels[current]
	}
	if known && fitFor(model.MinVRAMGB, memoryGB) != "over" {
		return current, false
	}
	fallback := catalog.DefaultModel()
	return fallback, current != fallback
}

func (s *Server) sparkClusterStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.SparkCluster) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
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
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	result, err := sparkcluster.Create(ctx, request)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, result)
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
	if err := sparkcluster.Disconnect(ctx, request.Passwords); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	// The private link is gone, so distributed inference must never remain the
	// persisted target. applyEngine removes the coordinator/proxy, releases its
	// allocations, and starts a clean single-Spark engine.
	st := s.state.Get()
	fallback, changed := localModelAfterClusterDisconnect(st, totalVRAMGB(ctx))
	if err := s.state.SetExecutionMode("local"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the cluster was disconnected, but local inference mode could not be saved"})
		return
	}
	if changed {
		if err := s.state.SetModel(fallback); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the cluster was disconnected, but the fallback model could not be saved"})
			return
		}
	}
	engineID := st.Engine
	if engineID == "" {
		engineID = catalog.DefaultEngine()
	}
	target, ok := catalog.Get(engineID)
	if !ok || !target.Engine {
		target, _ = catalog.Get(catalog.DefaultEngine())
	}
	// Keep this under the engine: namespace so /api/engine reports this newest
	// local transition instead of a stale distributed launch job.
	job := s.jobs.Create("engine:cluster-disconnect-fallback")
	go s.applyEngine(job, target)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"disconnected":  true,
		"jobId":         job.ID,
		"fallbackModel": fallback,
		"modelChanged":  changed,
	})
}
