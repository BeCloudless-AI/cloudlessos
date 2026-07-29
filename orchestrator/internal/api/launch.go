package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/customengine"
	"github.com/cloudless/orchestrator/internal/engine"
)

// resolveModel turns "" (the "use the catalog default" sentinel) into the concrete
// default model id, so launch-command overrides key on a stable, concrete model.
func resolveModel(model string) string {
	if model == "" {
		return catalog.DefaultModel()
	}
	return model
}

// engineForLaunch returns the engine a launch command applies to: the running one,
// else the user's selected engine, else the catalog default.
func (s *Server) engineForLaunch(ctx context.Context) (catalog.App, bool) {
	id := s.activeEngine(ctx)
	if id == "" {
		id = s.state.Get().Engine
	}
	if id == "" {
		id = catalog.DefaultEngine()
	}
	return customengine.Get(s.state, id)
}

// launchInfo is the editable launch command for a model on the active engine.
type launchInfo struct {
	Engine         string `json:"engine"`
	EngineName     string `json:"engineName"`
	Model          string `json:"model"`
	Image          string `json:"image"`
	Prefix         string `json:"prefix"`         // managed `docker run …` up to and including the image
	Command        string `json:"command"`        // the editable container command (effective: override or default)
	DefaultCommand string `json:"defaultCommand"` // catalog default (for "reset to default")
	Overridden     bool   `json:"overridden"`     // a custom command is saved for this engine+model
	Note           string `json:"note,omitempty"`
}

func (s *Server) launchInfoFor(ctx context.Context, model string) (launchInfo, bool) {
	app, ok := s.engineForLaunch(ctx)
	if !ok || !app.Engine {
		return launchInfo{}, false
	}
	resolved := resolveModel(model)
	def := catalog.EngineSpec(app, model)
	override, has := s.state.EngineCmd(app.ID, resolved)
	eff := catalog.EngineSpecOverride(app, model, override)
	prefix, command := engine.PreviewParts(eff)
	_, defCommand := engine.PreviewParts(def)
	info := launchInfo{
		Engine: app.ID, EngineName: s.engineDisplayName(app.ID),
		Model: resolved, Image: eff.Image,
		Prefix: prefix, Command: command, DefaultCommand: defCommand, Overridden: has,
	}
	if app.ID == "llamacpp" {
		info.Note = "llama.cpp serves its own bundled GGUF model — the model chosen here applies to vLLM/SGLang. You can still edit and save its launch command."
	}
	return info, true
}

// engineLaunchGet returns the launch command for ?model= on the active engine.
func (s *Server) engineLaunchGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	info, ok := s.launchInfoFor(ctx, r.URL.Query().Get("model"))
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no inference engine available"})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// engineLaunchSet saves an edited launch command for (active engine, model). A
// command that matches the default clears the override instead of storing a no-op.
func (s *Server) engineLaunchSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model   string `json:"model"`
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	app, ok := s.engineForLaunch(ctx)
	if !ok || !app.Engine {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no inference engine"})
		return
	}
	args := engine.ShellSplit(body.Command)
	if len(args) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the launch command can't be empty"})
		return
	}
	resolved := resolveModel(body.Model)
	def := catalog.EngineSpec(app, body.Model)
	if engine.ShellJoin(args) == engine.ShellJoin(def.Args) {
		_ = s.state.ClearEngineCmd(app.ID, resolved) // identical to default → no override
	} else if err := s.state.SetEngineCmd(app.ID, resolved, args); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	info, _ := s.launchInfoFor(ctx, body.Model)
	writeJSON(w, http.StatusOK, info)
}

// engineLaunchClear removes the saved override for (active engine, ?model=).
func (s *Server) engineLaunchClear(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	app, ok := s.engineForLaunch(ctx)
	if !ok || !app.Engine {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no inference engine"})
		return
	}
	_ = s.state.ClearEngineCmd(app.ID, resolveModel(r.URL.Query().Get("model")))
	info, _ := s.launchInfoFor(ctx, r.URL.Query().Get("model"))
	writeJSON(w, http.StatusOK, info)
}
