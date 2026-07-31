package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// bootHealth returns the root-owned graphical-boot audit without weakening its
// file permissions. It gives the desktop and support tools one structured view
// of the same evidence used by systemd recovery and release qualification.
func (s *Server) bootHealth(w http.ResponseWriter, _ *http.Request) {
	if s.state == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	path := filepath.Join(s.state.Dir(), "boot-health.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "boot health is unavailable"})
		return
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil || result["schema"] != "cloudless.boot-health.v1" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "boot health is invalid"})
		return
	}
	result["available"] = true
	writeJSON(w, http.StatusOK, result)
}
