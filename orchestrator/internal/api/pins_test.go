package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/state"
)

type pinEngine struct {
	engine.Engine
	states map[string]string
}

func (p pinEngine) Find(_ context.Context, name string) (*engine.Container, error) {
	state := p.states[name]
	if state == "" {
		return nil, nil
	}
	return &engine.Container{Name: name, State: state}, nil
}

func TestPackLaunchAppCanBePinnedWhileHiddenFromDuplicateListing(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, eng: pinEngine{states: map[string]string{"cloudless-n8n": "running"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/apps/n8n/pin", nil)
	request.SetPathValue("id", "n8n")
	response := httptest.NewRecorder()

	server.pinToggle(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("pin n8n returned %d: %s", response.Code, response.Body.String())
	}
	pins, customized := store.Pins()
	if !customized || len(pins) != 1 || pins[0] != "n8n" {
		t.Fatalf("pins = %v, customized=%v", pins, customized)
	}
}

func TestUninstalledAppCannotBePinned(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, eng: pinEngine{states: map[string]string{}}}
	request := httptest.NewRequest(http.MethodPost, "/api/apps/n8n/pin", nil)
	request.SetPathValue("id", "n8n")
	response := httptest.NewRecorder()

	server.pinToggle(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("pin uninstalled n8n returned %d: %s", response.Code, response.Body.String())
	}
	pins, customized := store.Pins()
	if customized || len(pins) != 0 {
		t.Fatalf("uninstalled app changed pins: %v, customized=%v", pins, customized)
	}
}

func TestFailedContainerCannotBePinned(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, eng: pinEngine{states: map[string]string{"cloudless-comfyui": "exited"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/apps/comfyui/pin", nil)
	request.SetPathValue("id", "comfyui")
	response := httptest.NewRecorder()

	server.pinToggle(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("pin failed ComfyUI returned %d: %s", response.Code, response.Body.String())
	}
}
