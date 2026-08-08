package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

const vllmScrape = `# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="cloudless"} 3.0
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="cloudless"} 12.0
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{model_name="cloudless"} 0.87
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="cloudless"} 1841022.0
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="cloudless"} 402118.0
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_sum{model_name="cloudless"} 84.2
vllm:time_to_first_token_seconds_count{model_name="cloudless"} 921.0
`

// stubScrape points the shared metrics cache at fixed Prometheus text.
func stubScrape(t *testing.T, text string, ok bool) {
	t.Helper()
	old := scrapeFetch
	scrapeFetch = func(context.Context) (string, bool) { return text, ok }
	resetMetricsCache()
	t.Cleanup(func() { scrapeFetch = old; resetMetricsCache() })
}

// gatewayMetricsServer returns a server with one model-scoped key and its secret.
func gatewayMetricsServer(t *testing.T) (*Server, string) {
	t.Helper()
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := st.AddAPIKey("monitoring", state.APIKeyScopeModel)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{state: st}, secret
}

func TestGatewayMetricsRequireAModelScopedKey(t *testing.T) {
	stubScrape(t, vllmScrape, true)
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentSecret, _, err := st.AddAPIKey("agent only", state.APIKeyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	handler := (&Server{state: st}).GatewayHandler()
	for _, test := range []struct {
		name, path, auth string
		want             int
	}{
		{"anonymous text", "/metrics", "", http.StatusUnauthorized},
		{"anonymous json", "/v1/metrics", "", http.StatusUnauthorized},
		{"agent scope denied", "/metrics", "Bearer " + agentSecret, http.StatusForbidden},
		{"bad key", "/metrics", "Bearer sk-cloudless-nope", http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.RemoteAddr = "203.0.113.9:5000"
		if test.auth != "" {
			request.Header.Set("Authorization", test.auth)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Errorf("%s: status = %d, want %d", test.name, recorder.Code, test.want)
		}
		if strings.Contains(recorder.Body.String(), "vllm:") {
			t.Errorf("%s: unauthenticated response leaked engine metrics", test.name)
		}
	}
}

func TestGatewayMetricsServePrometheusTextAndJSON(t *testing.T) {
	stubScrape(t, vllmScrape, true)
	server, secret := gatewayMetricsServer(t)
	handler := server.GatewayHandler()

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("text status = %d", recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type = %q", ct)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"cloudless_engine_up 1",
		"cloudless_requests_running 3",
		"cloudless_requests_waiting 12",
		"cloudless_kv_cache_usage_ratio 0.87",
		"cloudless_prompt_tokens_total 1841022", // no exponent notation
		"cloudless_generation_tokens_total 402118",
		"cloudless_ttft_seconds_sum 84.2",
		"cloudless_ttft_seconds_count 921",
		"# TYPE cloudless_requests_running gauge",
		"# TYPE cloudless_prompt_tokens_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("text exposition missing %q\n%s", want, body)
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("json status = %d", recorder.Code)
	}
	json := recorder.Body.String()
	for _, want := range []string{`"available":true`, `"running":3`, `"waiting":12`, `"promptTokens":1841022`} {
		if !strings.Contains(json, want) {
			t.Errorf("json missing %q\n%s", want, json)
		}
	}
}

// The gateway hides which engine is behind it and what the engine calls itself.
// Re-exporting under cloudless_* is what keeps that true for observability too.
func TestGatewayMetricsDoNotLeakThePrivateEngineIdentity(t *testing.T) {
	stubScrape(t, vllmScrape, true)
	server, secret := gatewayMetricsServer(t)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	server.GatewayHandler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	for _, leak := range []string{"vllm:", "sglang:", "llamacpp:", `model_name="cloudless"`} {
		if strings.Contains(body, leak) {
			t.Errorf("exposition leaked %q\n%s", leak, body)
		}
	}
	alias := server.inferenceContract().ModelAlias
	if !strings.Contains(body, `cloudless_engine_info{engine="vllm",model="`+alias+`"} 1`) {
		t.Errorf("engine_info did not report the client-facing alias %q\n%s", alias, body)
	}
}

// A scrape must still carry a signal when the engine is down, otherwise a stopped
// engine looks exactly like an unreachable host.
func TestGatewayMetricsReportEngineDownWithoutFakeZeroes(t *testing.T) {
	text := renderGatewayMetrics(engineMetrics{
		Available: false, Ready: false, Hint: "No inference engine is running right now.",
	}, "my-model")
	if !strings.Contains(text, "cloudless_engine_up 0") || !strings.Contains(text, "cloudless_engine_ready 0") {
		t.Fatalf("missing up/ready signal\n%s", text)
	}
	if strings.Contains(text, "cloudless_requests_running") || strings.Contains(text, "cloudless_prompt_tokens_total") {
		t.Fatalf("emitted zeroed activity for an engine with no metrics\n%s", text)
	}
	if !strings.Contains(text, "# No inference engine is running right now.") {
		t.Fatalf("hint not carried as a comment\n%s", text)
	}
}

// Metrics reads are authenticated traffic, but counting them as inference would
// let a 15-second scraper dominate the per-key counters it is reporting on.
func TestGatewayMetricsAreAuditedButNotBilledAsInference(t *testing.T) {
	stubScrape(t, vllmScrape, true)
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret, key, err := st.AddAPIKey("monitoring", state.APIKeyScopeModel)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: st}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	server.GatewayHandler().ServeHTTP(httptest.NewRecorder(), request)

	for _, k := range st.APIKeys() {
		if k.ID == key.ID && (k.Requests != 0 || k.ModelRequests != 0) {
			t.Fatalf("metrics read billed as inference: requests=%d model=%d", k.Requests, k.ModelRequests)
		}
	}
	_, audit := server.gatewaySecurity()
	events, err := audit.Latest(10)
	if err != nil || len(events) == 0 || events[0].Event != "gateway-metrics" || events[0].KeyID != key.ID {
		t.Fatalf("metrics audit = %#v, %v", events, err)
	}
}

// /v1/metrics must resolve to the local handler, never the engine reverse proxy.
func TestGatewayMetricsRoutingAndMethods(t *testing.T) {
	stubScrape(t, vllmScrape, true)
	server, secret := gatewayMetricsServer(t)
	handler := server.GatewayHandler()
	for _, test := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/metrics", http.StatusNotFound},
		{http.MethodPost, "/v1/metrics", http.StatusNotFound},
		{http.MethodGet, "/metrics/extra", http.StatusNotFound},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Errorf("%s %s = %d, want %d", test.method, test.path, recorder.Code, test.want)
		}
	}
}

func TestPromValueAndLabelFormatting(t *testing.T) {
	for value, want := range map[float64]string{1841022: "1841022", 0.87: "0.87", 0: "0"} {
		if got := promValue(value); got != want {
			t.Errorf("promValue(%v) = %q, want %q", value, got, want)
		}
	}
	if got := promLabel(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("promLabel = %s", got)
	}
}
