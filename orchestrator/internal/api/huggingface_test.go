package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestNormalizeHuggingFaceRepo(t *testing.T) {
	for input, want := range map[string]string{
		"Qwen/Qwen2.5-7B-Instruct":                                  "Qwen/Qwen2.5-7B-Instruct",
		"https://huggingface.co/Qwen/Qwen2.5-7B-Instruct":           "Qwen/Qwen2.5-7B-Instruct",
		"https://huggingface.co/Qwen/Qwen2.5-7B-Instruct/tree/main": "Qwen/Qwen2.5-7B-Instruct",
	} {
		got, ok := normalizeHuggingFaceRepo(input)
		if !ok || got != want {
			t.Errorf("normalizeHuggingFaceRepo(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "one-part", "https://example.com/org/model", "../model", "org/../model", "org/model/extra"} {
		if got, ok := normalizeHuggingFaceRepo(input); ok {
			t.Errorf("normalizeHuggingFaceRepo(%q) = %q, true; want rejected", input, got)
		}
	}
}

func TestHuggingFaceAccountConnectVerifyAndDisconnect(t *testing.T) {
	const token = "hf_valid_test_token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/whoami-v2" {
			t.Fatalf("unexpected Hugging Face request: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "samuel", "fullname": "Samuel", "avatarUrl": "https://example.test/avatar.png"})
	}))
	defer server.Close()
	original := huggingFaceBaseURL
	huggingFaceBaseURL = server.URL
	t.Cleanup(func() { huggingFaceBaseURL = original })

	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{state: store}
	req := httptest.NewRequest(http.MethodPost, "/api/models/huggingface/account", strings.NewReader(`{"token":"`+token+`"}`))
	rec := httptest.NewRecorder()
	srv.huggingFaceConnect(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), token) || !strings.Contains(rec.Body.String(), `"name":"samuel"`) {
		t.Fatalf("connect status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, err := store.HuggingFaceToken(); err != nil || got != token {
		t.Fatalf("stored token = %q, %v", got, err)
	}

	rec = httptest.NewRecorder()
	srv.huggingFaceAccount(rec, httptest.NewRequest(http.MethodGet, "/api/models/huggingface/account", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), token) || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Fatalf("account status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.huggingFaceDisconnect(rec, httptest.NewRequest(http.MethodDelete, "/api/models/huggingface/account", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, err := store.HuggingFaceToken(); err != nil || got != "" {
		t.Fatalf("token after disconnect = %q, %v", got, err)
	}
}

func TestHuggingFaceSearchKeepsPublicGenerativeModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" || r.URL.Query().Get("search") != "qwen" {
			t.Fatalf("unexpected Hugging Face request: %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "Qwen/Test-Instruct", "pipeline_tag": "text-generation", "library_name": "transformers", "downloads": 1200, "likes": 30, "tags": []string{"chat", "safetensors"}},
			{"id": "someone/private", "pipeline_tag": "text-generation", "private": true},
			{"id": "someone/classifier", "pipeline_tag": "text-classification"},
		})
	}))
	defer server.Close()
	original := huggingFaceBaseURL
	huggingFaceBaseURL = server.URL
	t.Cleanup(func() { huggingFaceBaseURL = original })

	req := httptest.NewRequest(http.MethodGet, "/api/models/huggingface/search?q=qwen", nil)
	rec := httptest.NewRecorder()
	(&Server{}).huggingFaceSearch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []hfSearchView `json:"results"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 || body.Results[0].ID != "Qwen/Test-Instruct" {
		t.Fatalf("filtered search results = %#v", body.Results)
	}
}

func TestHuggingFaceSearchUsesConnectedAccountForPrivateModels(t *testing.T) {
	const token = "hf_connected_test_token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": "samuel/private-model", "pipeline_tag": "text-generation", "library_name": "transformers", "private": true,
		}})
	}))
	defer server.Close()
	original := huggingFaceBaseURL
	huggingFaceBaseURL = server.URL
	t.Cleanup(func() { huggingFaceBaseURL = original })
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetHuggingFaceToken(token); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/models/huggingface/search?q=private", nil)
	rec := httptest.NewRecorder()
	(&Server{state: store}).huggingFaceSearch(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"private":true`) || !strings.Contains(rec.Body.String(), "samuel/private-model") {
		t.Fatalf("private search status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHuggingFaceMetadataDoesNotInventCompatibilityEstimate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/resolve/main/config.json") {
			t.Fatalf("unexpected metadata request: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"max_position_embeddings":32768}`))
	}))
	defer server.Close()
	original := huggingFaceBaseURL
	huggingFaceBaseURL = server.URL
	t.Cleanup(func() { huggingFaceBaseURL = original })

	info := hfAPIModel{ID: "Qwen/Test-7B-AWQ", PipelineTag: "text-generation", LibraryName: "transformers", Tags: []string{"chat", "awq", "license:apache-2.0"}}
	info.Safetensors.Total = 7_000_000_000
	model := hfModelFromInfo(context.Background(), info.ID, info)
	if model.Source != "huggingface" || model.Quant != "AWQ" || model.ContextK != 33 || model.MinVRAMGB != 0 {
		t.Fatalf("imported model metadata = %#v", model)
	}
	if !strings.Contains(model.RuntimeNote, "will not claim a memory fit") {
		t.Fatalf("imported model does not explain missing fit evidence: %#v", model)
	}
}

func TestEmbeddedWebContainsHuggingFaceDiscoveryFlow(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{"Browse Hugging Face", "function renderHuggingFace", "/api/models/huggingface/search", "/api/models/huggingface/import", "function openHFTokenDialog"} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
}
