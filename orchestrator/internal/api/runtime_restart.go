package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/models"
)

type runtimeRestartOfferResponse struct {
	Pending       bool   `json:"pending"`
	InstanceID    string `json:"instanceId,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Name          string `json:"name,omitempty"`
	RecipeID      string `json:"recipeId,omitempty"`
	ModelID       string `json:"modelId,omitempty"`
	EngineID      string `json:"engineId,omitempty"`
	ExecutionMode string `json:"executionMode,omitempty"`
	Automatic     bool   `json:"automatic,omitempty"`
}

func (s *Server) runtimeRestartSettingGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.state.RuntimeRestartAutomatic()})
}

func (s *Server) runtimeRestartSettingSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled must be true or false"})
		return
	}
	if err := s.state.SetRuntimeRestartAutomatic(*body.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": *body.Enabled})
}

func (s *Server) runtimeRestartOfferGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	current := s.state.Get()
	offer := current.RuntimeRestartOffer
	if offer.InstanceID == "" || offer.InstanceID != s.runtimeInstanceID {
		writeJSON(w, http.StatusOK, runtimeRestartOfferResponse{})
		return
	}
	runtime := offer.Runtime
	response := runtimeRestartOfferResponse{
		Pending: true, InstanceID: offer.InstanceID, ModelID: resolveModel(runtime.Model),
		EngineID: runtime.Engine, ExecutionMode: runtime.ExecutionMode, Automatic: offer.Automatic,
	}
	if response.EngineID == "" {
		response.EngineID = catalog.DefaultEngine()
	}
	if runtime.LocalRecipeID != "" {
		response.Kind, response.RecipeID, response.Name = "recipe", runtime.LocalRecipeID, runtime.LocalRecipeID
		if recipe, ok, err := s.recipes.Get(runtime.LocalRecipeID); err == nil && ok && strings.TrimSpace(recipe.Name) != "" {
			response.Name = recipe.Name
		}
	} else {
		response.Kind, response.Name = "model", response.ModelID
		if model, ok := models.Get(response.ModelID); ok && strings.TrimSpace(model.Name) != "" {
			response.Name = model.Name
		} else if custom, ok := s.state.Get().CustomModels[response.ModelID]; ok && strings.TrimSpace(custom.Name) != "" {
			response.Name = custom.Name
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) runtimeRestartOfferDismiss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceID string `json:"instanceId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || strings.TrimSpace(body.InstanceID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "restart offer identity is required"})
		return
	}
	if err := s.state.ClearRuntimeRestartOffer(body.InstanceID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"dismissed": true})
}
