package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/state"
)

// Optional observability on the authenticated gateway. The dashboard listener already
// serves normalized engine metrics, but it is loopback-only and unauthenticated,
// so nothing off-box can read them. These two routes publish the SAME normalized
// snapshot on the shareable gateway port, behind the same scoped bearer keys,
// after the user explicitly enables the metrics API capability.
//
// The engine's own Prometheus surface is deliberately NOT proxied: it names the
// private "cloudless" served identity and engine-specific series (vllm:*), both
// of which the gateway exists to hide. Re-exporting under cloudless_* keeps the
// contract stable across the vLLM / SGLang / llama.cpp engines (D15).
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

// gatewayMetricsText serves the Prometheus text exposition at GET /metrics.
func (s *Server) gatewayMetricsText(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot, alias, ok := s.gatewayMetricsSnapshot(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", metricsContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, renderGatewayMetrics(snapshot, alias))
}

// gatewayMetricsJSON serves the same snapshot as JSON at GET /v1/metrics, for
// clients that would otherwise have to parse the text exposition themselves.
func (s *Server) gatewayMetricsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot, alias, ok := s.gatewayMetricsSnapshot(w, r)
	if !ok {
		return
	}
	snapshot.Model = alias
	writeJSON(w, http.StatusOK, snapshot)
}

// gatewayMetricsSnapshot authenticates a metrics read and gathers the snapshot.
// Reads are authorized and rate-limited like any other gateway call, but they are
// NOT recorded as inference usage: a monitoring scraper polling every few seconds
// would otherwise dominate the per-key request and token counters it is reading.
func (s *Server) gatewayMetricsSnapshot(w http.ResponseWriter, r *http.Request) (engineMetrics, string, bool) {
	if !s.state.GatewayMetricsEnabled() {
		writeOpenAIError(w, http.StatusNotFound, "The Cloudless metrics API is not enabled.")
		return engineMetrics{}, "", false
	}
	_, ok := s.authorizeGateway(w, r, state.APIKeyScopeModel)
	if !ok {
		return engineMetrics{}, "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	snapshot := s.engineMetricsSnapshot(ctx)
	return snapshot, s.inferenceContract().ModelAlias, true
}

// gatewayMetricsSet changes only the authenticated API exposure. The desktop
// Activity view continues using the loopback metrics endpoint either way.
func (s *Server) gatewayMetricsSet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "gateway-metrics" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "explicit metrics API confirmation required"})
		return
	}
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if body.Enable && len(s.state.APIKeys()) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "create a scoped API key before exposing metrics"})
		return
	}
	if err := s.state.SetGatewayMetricsEnabled(body.Enable); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save metrics API preference"})
		return
	}
	outcome := "disabled"
	if body.Enable {
		outcome = "enabled"
	}
	s.auditGateway(gatewayAuditEvent{Event: "gateway-capability", Outcome: outcome, Scope: "metrics"})
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enable})
}

// renderGatewayMetrics writes the normalized snapshot as a Prometheus text
// exposition. cloudless_engine_up is always present so a scrape still carries a
// signal when the engine is down — an empty body or an error status would make an
// unloaded engine indistinguishable from an unreachable host.
func renderGatewayMetrics(m engineMetrics, alias string) string {
	var b strings.Builder
	gauge := func(name, help string, v float64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", name, help, name, name, promValue(v))
	}
	counter := func(name, help string, v float64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %s\n", name, help, name, name, promValue(v))
	}
	summary := func(name, help string, sum, count float64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s summary\n%s_sum %s\n%s_count %s\n",
			name, help, name, name, promValue(sum), name, promValue(count))
	}
	boolValue := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}

	gauge("cloudless_engine_up", "1 when the inference engine is reporting live metrics.", boolValue(m.Available))
	gauge("cloudless_engine_ready", "1 when the inference engine is up and serving requests.", boolValue(m.Ready))
	fmt.Fprintf(&b, "# HELP cloudless_engine_info The active engine and served model name.\n# TYPE cloudless_engine_info gauge\ncloudless_engine_info{engine=%s,model=%s} 1\n",
		promLabel(m.Engine), promLabel(alias))
	if !m.Available {
		// The engine is not exposing metrics. Say why in a comment rather than
		// emitting zeroes that a dashboard would read as genuine idle traffic.
		if m.Hint != "" {
			fmt.Fprintf(&b, "# %s\n", strings.ReplaceAll(m.Hint, "\n", " "))
		}
		return b.String()
	}

	gauge("cloudless_requests_running", "Requests currently being decoded by the engine.", m.Running)
	gauge("cloudless_requests_waiting", "Requests queued and waiting for a slot.", m.Waiting)
	gauge("cloudless_kv_cache_usage_ratio", "KV-cache utilization, 0..1.", m.KVCache)
	counter("cloudless_prompt_tokens_total", "Cumulative prefill (input) tokens processed.", m.PromptTokens)
	counter("cloudless_generation_tokens_total", "Cumulative decode (output) tokens generated.", m.GenTokens)
	counter("cloudless_requests_total", "Cumulative requests served.", m.RequestsCtr)
	summary("cloudless_ttft_seconds", "Time to first token.", m.TTFTSum, m.TTFTCount)
	summary("cloudless_tpot_seconds", "Time per output token.", m.TPOTSum, m.TPOTCount)
	counter("cloudless_preemptions_total", "Cumulative requests preempted under memory pressure.", m.Preemptions)
	counter("cloudless_prefix_cache_hits_total", "Cumulative prefix-cache hits.", m.CacheHits)
	counter("cloudless_prefix_cache_queries_total", "Cumulative prefix-cache lookups.", m.CacheQueries)
	if m.GenThroughput > 0 {
		// Only SGLang and llama.cpp report an instantaneous rate; vLLM consumers
		// derive it from the token counters instead.
		gauge("cloudless_generation_tokens_per_second", "Instantaneous decode throughput, when the engine reports it.", m.GenThroughput)
	}
	return b.String()
}

// promValue formats without exponent notation, which keeps integral counters
// readable ("1841022", not "1.841022e+06").
func promValue(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// promLabel quotes and escapes a label value per the exposition format.
func promLabel(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}
