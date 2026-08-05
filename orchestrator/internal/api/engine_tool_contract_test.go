package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEngineToolContractExercisesHermesRequestPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Fatalf("unexpected probe request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Model      string           `json:"model"`
			Tools      []map[string]any `json:"tools"`
			ToolChoice string           `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "cloudless" || payload.ToolChoice != "auto" || len(payload.Tools) != 1 {
			t.Fatalf("probe did not exercise tool parsing: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"READY"}}]}`))
	}))
	defer server.Close()

	if err := engineToolContractErrorAt(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
}

func TestEngineToolContractReportsRuntimeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "cannot import normalize_tool_choice", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := engineToolContractErrorAt(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "normalize_tool_choice") {
		t.Fatalf("runtime failure was not preserved: %v", err)
	}
}
