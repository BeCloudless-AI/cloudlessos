package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/power"
	"github.com/cloudless/orchestrator/internal/state"
	"github.com/cloudless/orchestrator/internal/usage"
)

func TestRoutesRegisterWithoutConflicts(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(&engine.Docker{}, st, manifest.New(""), manifest.NewModels(""), manifest.NewDiffusion(""), usage.Open(dir+"/usage.json"), power.Open(dir+"/power.json"))
	if server.Routes() == nil {
		t.Fatal("route handler is nil")
	}
}

func TestEmbeddedAppUpstreamPath(t *testing.T) {
	for input, want := range map[string]string{
		"/apps/n8n/":                        "/",
		"/apps/n8n/assets/editor.js":        "/assets/editor.js",
		"/apps/n8n/rest/settings?ignored=1": "/rest/settings?ignored=1",
	} {
		if got := appUpstreamPath(input, "/apps/n8n/"); got != want {
			t.Errorf("appUpstreamPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestConfigureCloudlessResearchCreatesModelOnce(t *testing.T) {
	var mu sync.Mutex
	providerCreates := 0
	modelCreates := 0
	setupCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/providers":
			providers := []map[string]any{}
			if providerCreates > 0 {
				models := []map[string]string{}
				if modelCreates > 0 {
					models = append(models, map[string]string{"key": "cloudless", "name": "Cloudless"})
				}
				providers = append(providers, map[string]any{"id": "provider-1", "name": "Cloudless", "chatModels": models})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": providers})
		case r.Method == http.MethodPost && r.URL.Path == "/api/providers":
			providerCreates++
			_ = json.NewEncoder(w).Encode(map[string]any{"provider": map[string]any{"id": "provider-1", "name": "Cloudless", "chatModels": []any{}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/providers/provider-1/models":
			modelCreates++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/config/setup-complete":
			setupCalls++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := configureCloudlessResearch(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	if err := configureCloudlessResearch(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	if providerCreates != 1 || modelCreates != 1 || setupCalls != 2 {
		t.Fatalf("provider creates=%d model creates=%d setup=%d", providerCreates, modelCreates, setupCalls)
	}
}
