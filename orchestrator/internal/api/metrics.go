package api

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
)

// engineMetrics is a normalized snapshot of the active inference engine's live work,
// scraped from its Prometheus /metrics endpoint. Names differ between vLLM (vllm:*)
// and SGLang (sglang:*); we map both onto the same fields. Counters (PromptTokens,
// GenTokens, TTFT/TPOT sum+count) are cumulative — the UI derives rates from deltas.
type engineMetrics struct {
	Available     bool    `json:"available"`
	Engine        string  `json:"engine"`          // "vllm" | "sglang" | "" — the ACTIVE engine (even when metrics are off)
	Model         string  `json:"model,omitempty"` // served model (basename) for display
	Ready         bool    `json:"ready"`           // the engine is up and serving
	CanEnable     bool    `json:"canEnable"`       // metrics are off but a restart would turn them on
	Running       float64 `json:"running"`         // requests decoding right now
	Waiting       float64 `json:"waiting"`         // requests queued
	KVCache       float64 `json:"kvCache"`         // KV-cache utilization, 0..1
	PromptTokens  float64 `json:"promptTokens"`    // cumulative prefill tokens
	GenTokens     float64 `json:"genTokens"`       // cumulative decode/output tokens
	TTFTSum       float64 `json:"ttftSum"`         // time-to-first-token histogram (prefill latency)
	TTFTCount     float64 `json:"ttftCount"`
	TPOTSum       float64 `json:"tpotSum"` // time-per-output-token histogram (decode latency)
	TPOTCount     float64 `json:"tpotCount"`
	GenThroughput float64 `json:"genThroughput,omitempty"` // SGLang reports tok/s directly
	Preemptions   float64 `json:"preemptions,omitempty"`
	Hint          string  `json:"hint,omitempty"`
}

func (s *Server) engineMetricsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	model := s.state.Get().Model
	if model == "" {
		model = catalog.DefaultModel()
	}
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:] // basename for display
	}

	// Live metrics available → return them (Engine is filled from the metric prefix).
	if body, ok := scrapeMetrics(ctx); ok {
		if m := parseEngineMetrics(body); m.Available {
			m.Ready = true
			m.Model = model
			writeJSON(w, http.StatusOK, m)
			return
		}
	}

	// No metrics — report WHICH engine is actually running and an accurate reason
	// (not a misleading "warming up"). SGLang serves /metrics only with --enable-metrics,
	// which an engine restart applies.
	active := s.activeEngine(ctx)
	ready := active != "" && engineReady(ctx)
	name := engineDisplayName(active)
	res := engineMetrics{Available: false, Engine: active, Ready: ready, Model: model}
	switch {
	case active == "":
		res.Hint = "No inference engine is running right now."
	case !ready:
		res.Hint = name + " is starting up — this can take up to a minute."
	default:
		res.Hint = name + " is running, but it isn't reporting live metrics yet. Turn them on to see activity here."
		res.CanEnable = true
	}
	writeJSON(w, http.StatusOK, res)
}

// engineDisplayName turns an engine id into its short display name ("vLLM", "SGLang").
func engineDisplayName(id string) string {
	if id == "" {
		return "The engine"
	}
	if a, ok := catalog.Get(id); ok {
		return strings.TrimSuffix(a.Name, " Engine")
	}
	return id
}

// scrapeMetrics fetches the engine's Prometheus text exposition (host port 8000).
func scrapeMetrics(ctx context.Context) (string, bool) {
	url := "http://127.0.0.1:" + strconv.Itoa(catalog.EnginePort) + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// parseEngineMetrics turns Prometheus text into the normalized snapshot.
func parseEngineMetrics(text string) engineMetrics {
	m := promSum(text)
	engine := ""
	for k := range m {
		if strings.HasPrefix(k, "vllm:") {
			engine = "vllm"
			break
		}
		if strings.HasPrefix(k, "sglang:") {
			engine = "sglang"
			break
		}
		if strings.HasPrefix(k, "llamacpp:") {
			engine = "llamacpp"
			break
		}
	}
	get := func(names ...string) float64 {
		for _, n := range names {
			if v, ok := m[n]; ok {
				return v
			}
		}
		return 0
	}
	return engineMetrics{
		Available:     engine != "",
		Engine:        engine,
		Running:       get("vllm:num_requests_running", "sglang:num_running_reqs", "llamacpp:requests_processing"),
		Waiting:       get("vllm:num_requests_waiting", "sglang:num_queue_reqs", "llamacpp:requests_deferred"),
		KVCache:       get("vllm:gpu_cache_usage_perc", "sglang:token_usage", "llamacpp:kv_cache_usage_ratio"),
		PromptTokens:  get("vllm:prompt_tokens_total", "sglang:prompt_tokens_total", "llamacpp:prompt_tokens_total"),
		GenTokens:     get("vllm:generation_tokens_total", "sglang:generation_tokens_total", "llamacpp:tokens_predicted_total"),
		TTFTSum:       get("vllm:time_to_first_token_seconds_sum", "sglang:time_to_first_token_seconds_sum"),
		TTFTCount:     get("vllm:time_to_first_token_seconds_count", "sglang:time_to_first_token_seconds_count"),
		TPOTSum:       get("vllm:time_per_output_token_seconds_sum", "sglang:inter_token_latency_seconds_sum", "sglang:time_per_output_token_seconds_sum"),
		TPOTCount:     get("vllm:time_per_output_token_seconds_count", "sglang:inter_token_latency_seconds_count", "sglang:time_per_output_token_seconds_count"),
		GenThroughput: get("sglang:gen_throughput", "llamacpp:predicted_tokens_seconds"),
		Preemptions:   get("vllm:num_preemptions_total"),
	}
}

// promSum parses a Prometheus text exposition, summing each metric's value across
// all label sets (most of our metrics have a single series, but counters can be
// split per finish-reason etc.). Comments and bucket lines are skipped.
func promSum(text string) map[string]float64 {
	out := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		// name{labels} value   OR   name value
		name := line
		if i := strings.IndexByte(line, '{'); i >= 0 {
			name = line[:i]
		} else if i := strings.IndexByte(line, ' '); i >= 0 {
			name = line[:i]
		}
		if strings.HasSuffix(name, "_bucket") {
			continue // histogram buckets aren't needed
		}
		sp := strings.LastIndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(line[sp+1:]), 64)
		if err != nil {
			continue
		}
		out[name] += v
	}
	return out
}
