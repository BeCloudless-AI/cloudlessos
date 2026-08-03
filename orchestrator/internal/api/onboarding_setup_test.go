package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestOnboardingSetupRequiresExplicitChoiceBeforeProvisioning(t *testing.T) {
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

	missing := httptest.NewRecorder()
	server.onboardingSetup(missing, httptest.NewRequest(http.MethodPost, "/api/onboarding/setup", bytes.NewBufferString(`{"choice":"install"}`)))
	if missing.Code != http.StatusServiceUnavailable || !store.FirstLaunchSetupRequired() {
		t.Fatalf("installer absence must leave consent pending: status=%d choice=%q", missing.Code, store.FirstLaunchSetup())
	}

	started := 0
	server.SetInitialProvisioner(func() { started++ })
	accepted := httptest.NewRecorder()
	server.onboardingSetup(accepted, httptest.NewRequest(http.MethodPost, "/api/onboarding/setup", bytes.NewBufferString(`{"choice":"install"}`)))
	if accepted.Code != http.StatusAccepted || started != 1 {
		t.Fatalf("install choice status=%d starts=%d", accepted.Code, started)
	}
	if store.FirstLaunchSetup() != state.FirstLaunchSetupInstall || store.Get().EngineUnloaded {
		t.Fatal("install choice was not persisted as an active inference setup")
	}
}

func TestOnboardingManualChoiceStartsNothing(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := 0
	server := &Server{state: store, initialProvisioner: func() { started++ }}
	recorder := httptest.NewRecorder()
	server.onboardingSetup(recorder, httptest.NewRequest(http.MethodPost, "/api/onboarding/setup", bytes.NewBufferString(`{"choice":"manual"}`)))
	if recorder.Code != http.StatusAccepted || started != 0 {
		t.Fatalf("manual choice status=%d starts=%d", recorder.Code, started)
	}
	if store.FirstLaunchSetup() != state.FirstLaunchSetupManual || !store.Get().EngineUnloaded {
		t.Fatal("manual choice must persist without loading inference")
	}
}
