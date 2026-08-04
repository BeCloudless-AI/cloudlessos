package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestSettingsIdentifiesActiveRecipeForDesktopRuntimeControl(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipes := localrecipes.New(store.Dir())
	draft := localrecipes.NewDraft()
	draft.Name = "Exact active recipe"
	recipe, err := recipes.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: draft.Model.ID, LocalRecipeID: recipe.ID}); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, recipes: recipes}
	response := httptest.NewRecorder()
	server.settingsGet(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var payload struct {
		RuntimeKind string `json:"runtimeKind"`
		RuntimeID   string `json:"runtimeId"`
		RuntimeName string `json:"runtimeName"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.RuntimeKind != "recipe" || payload.RuntimeID != recipe.ID || payload.RuntimeName != draft.Name {
		t.Fatalf("active recipe identity = %#v", payload)
	}
}

func TestSettingsIdentifiesStandaloneModelAndHidesUnloadedRuntime(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const modelID = "Qwen/Qwen2.5-1.5B-Instruct"
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: modelID}); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, recipes: localrecipes.New(store.Dir())}
	read := func() map[string]any {
		response := httptest.NewRecorder()
		server.settingsGet(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		var payload map[string]any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	active := read()
	if active["runtimeKind"] != "model" || active["runtimeId"] != modelID || active["runtimeName"] == "" {
		t.Fatalf("active model identity = %#v", active)
	}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: modelID, EngineUnloaded: true}); err != nil {
		t.Fatal(err)
	}
	unloaded := read()
	if unloaded["runtimeKind"] != "" || unloaded["runtimeId"] != "" || unloaded["runtimeName"] != "" {
		t.Fatalf("unloaded runtime was exposed as active: %#v", unloaded)
	}
}
