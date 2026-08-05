package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestRuntimeRestartOfferNamesModelAndRequiresMatchingDismissal(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: "owner/exact-model", ExecutionMode: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-current"); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, recipes: localrecipes.New(store.Dir()), runtimeInstanceID: "daemon-current"}

	response := httptest.NewRecorder()
	server.runtimeRestartOfferGet(response, httptest.NewRequest(http.MethodGet, "/api/runtime/restart-offer", nil))
	var offer runtimeRestartOfferResponse
	if err := json.NewDecoder(response.Body).Decode(&offer); err != nil {
		t.Fatal(err)
	}
	if !offer.Pending || offer.Kind != "model" || offer.Name != "owner/exact-model" || offer.ModelID != "owner/exact-model" || offer.EngineID != "vllm" {
		t.Fatalf("restart offer = %#v", offer)
	}

	stale := httptest.NewRecorder()
	server.runtimeRestartOfferDismiss(stale, httptest.NewRequest(http.MethodPost, "/api/runtime/restart-offer/dismiss", bytes.NewBufferString(`{"instanceId":"daemon-stale"}`)))
	if store.Get().RuntimeRestartOffer.InstanceID != "daemon-current" {
		t.Fatal("stale dismissal cleared current offer")
	}
	current := httptest.NewRecorder()
	server.runtimeRestartOfferDismiss(current, httptest.NewRequest(http.MethodPost, "/api/runtime/restart-offer/dismiss", bytes.NewBufferString(`{"instanceId":"daemon-current"}`)))
	if current.Code != http.StatusOK || store.Get().RuntimeRestartOffer.InstanceID != "" {
		t.Fatalf("current dismissal failed: status=%d state=%#v", current.Code, store.Get().RuntimeRestartOffer)
	}
}

func TestRuntimeRestartOfferResolvesExactRecipeName(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipes := localrecipes.New(store.Dir())
	draft := localrecipes.NewDraft()
	draft.Name = "Laguna S 2.1 NVFP4"
	recipe, err := recipes.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: draft.Model.ID, LocalRecipeID: recipe.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-current"); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, recipes: recipes, runtimeInstanceID: "daemon-current"}
	response := httptest.NewRecorder()
	server.runtimeRestartOfferGet(response, httptest.NewRequest(http.MethodGet, "/api/runtime/restart-offer", nil))
	var offer runtimeRestartOfferResponse
	if err := json.NewDecoder(response.Body).Decode(&offer); err != nil {
		t.Fatal(err)
	}
	if !offer.Pending || offer.Kind != "recipe" || offer.RecipeID != recipe.ID || offer.Name != draft.Name {
		t.Fatalf("named recipe restart offer = %#v", offer)
	}
}

func TestRuntimeRestartOfferMarksAutomaticRecoveryAsInformational(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: "owner/model"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRuntimeRestartAutomatic(true); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-current"); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, recipes: localrecipes.New(store.Dir()), runtimeInstanceID: "daemon-current"}
	response := httptest.NewRecorder()
	server.runtimeRestartOfferGet(response, httptest.NewRequest(http.MethodGet, "/api/runtime/restart-offer", nil))
	var offer runtimeRestartOfferResponse
	if err := json.NewDecoder(response.Body).Decode(&offer); err != nil {
		t.Fatal(err)
	}
	if !offer.Pending || !offer.Automatic || store.Get().EngineUnloaded {
		t.Fatalf("automatic restart notice = %#v, state=%#v", offer, store.Get())
	}
}

func TestRuntimeRestartOfferDoesNotLeakAcrossDaemonInstances(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Model: "owner/model"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-old"); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, recipes: localrecipes.New(store.Dir()), runtimeInstanceID: "daemon-new"}
	response := httptest.NewRecorder()
	server.runtimeRestartOfferGet(response, httptest.NewRequest(http.MethodGet, "/api/runtime/restart-offer", nil))
	var offer runtimeRestartOfferResponse
	if err := json.NewDecoder(response.Body).Decode(&offer); err != nil {
		t.Fatal(err)
	}
	if offer.Pending {
		t.Fatalf("stale offer was presented: %#v", offer)
	}
}

func TestRuntimeRestartOfferSurvivesRuntimeBeingSafelyUnloaded(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: "owner/old-model"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-current"); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, recipes: localrecipes.New(store.Dir()), runtimeInstanceID: "daemon-current"}
	response := httptest.NewRecorder()
	server.runtimeRestartOfferGet(response, httptest.NewRequest(http.MethodGet, "/api/runtime/restart-offer", nil))
	var offer runtimeRestartOfferResponse
	if err := json.NewDecoder(response.Body).Decode(&offer); err != nil {
		t.Fatal(err)
	}
	if !offer.Pending || offer.ModelID != "owner/old-model" || !store.Get().EngineUnloaded {
		t.Fatalf("safely unloaded runtime lost exact restart offer: response=%#v state=%#v", offer, store.Get())
	}
}

func TestRuntimeRestartSettingRoundTrip(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	set := httptest.NewRecorder()
	server.runtimeRestartSettingSet(set, httptest.NewRequest(http.MethodPost, "/api/settings/runtime-restart", bytes.NewBufferString(`{"enabled":true}`)))
	if set.Code != http.StatusOK || !store.RuntimeRestartAutomatic() {
		t.Fatalf("setting was not enabled: status=%d body=%s", set.Code, set.Body.String())
	}
	get := httptest.NewRecorder()
	server.runtimeRestartSettingGet(get, httptest.NewRequest(http.MethodGet, "/api/settings/runtime-restart", nil))
	var response map[string]bool
	if err := json.NewDecoder(get.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response["enabled"] {
		t.Fatalf("setting response = %#v", response)
	}
}
