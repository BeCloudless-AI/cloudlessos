package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func recipeLaunchGuidanceStatus(t *testing.T, server *Server) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/guidance/recipe-launch", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guidance status = %d", recorder.Code)
	}
	var status map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestRecipeLaunchGuidanceAppearsAfterRecipeAndPersistsAcknowledgement(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	if status := recipeLaunchGuidanceStatus(t, server); status["show"] != false || status["acknowledged"] != false {
		t.Fatalf("fresh guidance status = %#v", status)
	}

	if err := store.CommitInferenceRuntime(state.InferenceRuntime{Engine: "vllm", Model: "owner/model", LocalRecipeID: "recipe-1"}); err != nil {
		t.Fatal(err)
	}
	if status := recipeLaunchGuidanceStatus(t, server); status["show"] != true {
		t.Fatalf("active first recipe guidance status = %#v", status)
	}

	recorder := httptest.NewRecorder()
	server.Routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/guidance/recipe-launch", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guidance acknowledgement = %d", recorder.Code)
	}

	reopened, err := state.Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if status := recipeLaunchGuidanceStatus(t, &Server{state: reopened}); status["show"] != false || status["acknowledged"] != true {
		t.Fatalf("persisted guidance status = %#v", status)
	}
}
