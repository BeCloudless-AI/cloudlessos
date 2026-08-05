package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestOnboardingSetupRejectsAutomaticProvisioning(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}

	get := httptest.NewRecorder()
	server.onboardingGet(get, httptest.NewRequest(http.MethodGet, "/api/onboarding", nil))
	var status map[string]any
	if err := json.Unmarshal(get.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["setupRequired"] != true || status["setupChoice"] != state.FirstLaunchSetupPending {
		t.Fatalf("unexpected fresh onboarding response: %#v", status)
	}

	rejected := httptest.NewRecorder()
	server.onboardingSetup(rejected, httptest.NewRequest(http.MethodPost, "/api/onboarding/setup", bytes.NewBufferString(`{"choice":"install"}`)))
	if rejected.Code != http.StatusBadRequest || !store.FirstLaunchSetupRequired() {
		t.Fatalf("automatic install must be rejected and leave consent pending: status=%d choice=%q", rejected.Code, store.FirstLaunchSetup())
	}
}

func TestOnboardingManualChoiceStartsNothing(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	recorder := httptest.NewRecorder()
	server.onboardingSetup(recorder, httptest.NewRequest(http.MethodPost, "/api/onboarding/setup", bytes.NewBufferString(`{"choice":"manual"}`)))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("manual choice status=%d", recorder.Code)
	}
	if store.FirstLaunchSetup() != state.FirstLaunchSetupManual || !store.Get().EngineUnloaded {
		t.Fatal("manual choice must persist without loading inference")
	}
}
