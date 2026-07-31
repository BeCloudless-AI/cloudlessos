package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var browserRequestDir = "/run/cloudless-browser/requests"
var browserStatusPath = "/run/cloudless-browser/status.json"

func browserAvailable() bool {
	info, err := os.Stat(browserRequestDir)
	return err == nil && info.IsDir()
}

func (s *Server) systemBrowserStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status := map[string]any{
		"available":          browserAvailable(),
		"running":            false,
		"minimized":          false,
		"profilePersistence": "persistent",
		"downloadsPath":      "/home/cloudless/Downloads",
		"updatePolicy":       "managed by the CloudlessOS package or Snap update channel",
	}
	if payload, err := os.ReadFile(browserStatusPath); err == nil && len(payload) <= 4096 {
		var runtime map[string]bool
		if json.Unmarshal(payload, &runtime) == nil {
			status["running"] = runtime["running"]
			status["minimized"] = runtime["minimized"]
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func normalizeBrowserURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "https://www.google.com", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("enter a valid http or https address")
	}
	return parsed.String(), nil
}

func (s *Server) systemBrowserOpen(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL    string `json:"url"`
		Action string `json:"action"`
	}
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid browser request"})
		return
	}
	input.Action = strings.TrimSpace(input.Action)
	if input.Action == "" {
		input.Action = "open"
	}
	if input.Action != "open" && input.Action != "show" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid browser action"})
		return
	}
	target, err := normalizeBrowserURL(input.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !browserAvailable() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Cloudless Browser is only available in an installed desktop session", "url": target})
		return
	}
	payload, _ := json.Marshal(map[string]string{"url": target, "action": input.Action})
	name := fmt.Sprintf("%d-%d.json", time.Now().UnixNano(), os.Getpid())
	if err := queueBrowserRequest(filepath.Join(browserRequestDir, name), payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue the browser window"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"opened": true, "url": target})
}

func queueBrowserRequest(path string, payload []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cloudless-browser-*")
	if err != nil {
		return err
	}
	tempName := temporary.Name()
	defer os.Remove(tempName)
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// cloudlessd has a private service umask. The desktop browser agent is a
	// separate, unprivileged process in the cloudless group and must be able to
	// consume only this typed request file.
	if err := os.Chmod(tempName, 0o660); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
