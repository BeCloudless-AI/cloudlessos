package api

import "testing"

func TestParseEngineMetricsVLLM(t *testing.T) {
	text := `# HELP vllm:num_requests_running Number of requests currently running on GPU.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="cloudless"} 3.0
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="cloudless"} 1.0
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{model_name="cloudless"} 0.42
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="cloudless"} 12000.0
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="cloudless"} 8000.0
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_sum{model_name="cloudless"} 4.5
vllm:time_to_first_token_seconds_count{model_name="cloudless"} 50.0
vllm:time_to_first_token_seconds_bucket{le="0.1",model_name="cloudless"} 10.0
# TYPE vllm:time_per_output_token_seconds histogram
vllm:time_per_output_token_seconds_sum{model_name="cloudless"} 16.0
vllm:time_per_output_token_seconds_count{model_name="cloudless"} 800.0
# TYPE vllm:num_preemptions_total counter
vllm:num_preemptions_total{model_name="cloudless"} 2.0
`
	m := parseEngineMetrics(text)
	if !m.Available || m.Engine != "vllm" {
		t.Fatalf("engine detect: available=%v engine=%q", m.Available, m.Engine)
	}
	checks := map[string][2]float64{
		"running": {m.Running, 3}, "waiting": {m.Waiting, 1}, "kv": {m.KVCache, 0.42},
		"prompt": {m.PromptTokens, 12000}, "gen": {m.GenTokens, 8000},
		"ttftSum": {m.TTFTSum, 4.5}, "ttftCount": {m.TTFTCount, 50},
		"tpotSum": {m.TPOTSum, 16}, "tpotCount": {m.TPOTCount, 800}, "preempt": {m.Preemptions, 2},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %v, want %v", name, c[0], c[1])
		}
	}
}

func TestParseEngineMetricsSGLang(t *testing.T) {
	text := `# TYPE sglang:num_running_reqs gauge
sglang:num_running_reqs{model_name="cloudless"} 2.0
# TYPE sglang:num_queue_reqs gauge
sglang:num_queue_reqs{model_name="cloudless"} 0.0
# TYPE sglang:token_usage gauge
sglang:token_usage{model_name="cloudless"} 0.31
# TYPE sglang:gen_throughput gauge
sglang:gen_throughput{model_name="cloudless"} 145.0
# TYPE sglang:prompt_tokens_total counter
sglang:prompt_tokens_total{model_name="cloudless"} 5000.0
# TYPE sglang:generation_tokens_total counter
sglang:generation_tokens_total{model_name="cloudless"} 3000.0
# TYPE sglang:time_to_first_token_seconds histogram
sglang:time_to_first_token_seconds_sum{model_name="cloudless"} 2.0
sglang:time_to_first_token_seconds_count{model_name="cloudless"} 20.0
# TYPE sglang:inter_token_latency_seconds histogram
sglang:inter_token_latency_seconds_sum{model_name="cloudless"} 9.0
sglang:inter_token_latency_seconds_count{model_name="cloudless"} 600.0
`
	m := parseEngineMetrics(text)
	if !m.Available || m.Engine != "sglang" {
		t.Fatalf("engine detect: available=%v engine=%q", m.Available, m.Engine)
	}
	if m.Running != 2 || m.KVCache != 0.31 || m.GenThroughput != 145 {
		t.Errorf("running=%v kv=%v gen/s=%v", m.Running, m.KVCache, m.GenThroughput)
	}
	if m.TPOTSum != 9 || m.TPOTCount != 600 {
		t.Errorf("tpot sum/count = %v/%v, want 9/600", m.TPOTSum, m.TPOTCount)
	}
}

func TestParseEngineMetricsEmpty(t *testing.T) {
	if m := parseEngineMetrics(`{"detail":"Not Found"}`); m.Available || m.Engine != "" {
		t.Errorf("non-prometheus body should be unavailable, got %+v", m)
	}
}
