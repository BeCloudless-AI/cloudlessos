package api

import (
	"context"
	"net/http"

	"github.com/cloudless/orchestrator/internal/catalog"
)

// validPins returns saved shortcuts whose recipes still exist. Installation is
// checked separately so stale shortcuts remain removable through the API.
func (s *Server) validPins() []string {
	ids, customized := s.state.Pins()
	if !customized {
		ids = catalog.DefaultPins()
	}
	// keep only ids that are real, launchable apps (drop stale/uninstalled recipes)
	out := []string{}
	for _, id := range ids {
		if a, ok := catalog.Get(id); ok && a.Pinnable() {
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) installedPins(ctx context.Context) []string {
	out := []string{}
	for _, id := range s.validPins() {
		app, _ := catalog.Get(id)
		if container, err := s.eng.Find(ctx, app.ContainerName()); err == nil && container != nil && container.State == "running" {
			out = append(out, id)
		}
	}
	return out
}

// pinsGet returns the ordered list of pinned app ids.
func (s *Server) pinsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"pinned": s.installedPins(r.Context())})
}

// pinToggle pins or unpins an app on the dashboard fast-launch, returning the new list.
func (s *Server) pinToggle(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || !app.Pinnable() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	cur := s.validPins()
	next := []string{}
	found := false
	for _, id := range cur {
		if id == app.ID {
			found = true
			continue // unpin
		}
		next = append(next, id)
	}
	if !found {
		container, err := s.eng.Find(r.Context(), app.ContainerName())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "container runtime unavailable"})
			return
		}
		if container == nil || container.State != "running" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "install the app before pinning it"})
			return
		}
		next = append(next, app.ID) // pin (append → newest last)
	}
	if err := s.state.SetPins(next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pinned": next, "pinnedNow": !found})
}
