package api

import (
	"net/http"

	"github.com/cloudless/orchestrator/internal/catalog"
)

// effectivePins returns the current dashboard fast-launch pins: the user's custom
// set if they've changed it, otherwise the catalog default.
func (s *Server) effectivePins() []string {
	ids, customized := s.state.Pins()
	if !customized {
		ids = catalog.DefaultPins()
	}
	// keep only ids that are real, launchable apps (drop stale/uninstalled recipes)
	out := []string{}
	for _, id := range ids {
		if a, ok := catalog.Get(id); ok && a.Launchable() {
			out = append(out, id)
		}
	}
	return out
}

// pinsGet returns the ordered list of pinned app ids.
func (s *Server) pinsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"pinned": s.effectivePins()})
}

// pinToggle pins or unpins an app on the dashboard fast-launch, returning the new list.
func (s *Server) pinToggle(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || !app.Launchable() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	cur := s.effectivePins()
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
		next = append(next, app.ID) // pin (append → newest last)
	}
	if err := s.state.SetPins(next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pinned": next, "pinnedNow": !found})
}
