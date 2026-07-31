package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
)

// Cloudflare quick tunnels print a URL like https://<words>.trycloudflare.com.
var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// tunnelURL scrapes the assigned quick-tunnel URL from a cloudflared container's logs.
func (s *Server) tunnelURL(ctx context.Context, name string) string {
	out, err := s.eng.Logs(ctx, name)
	if err != nil {
		return ""
	}
	return trycloudflareRe.FindString(out)
}

// tunnelGet reports whether an app is currently exposed online, and its public URL.
func (s *Server) tunnelGet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	if !app.Tunnelable() {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	c, _ := s.eng.Find(ctx, app.TunnelName())
	enabled := c != nil && c.State == "running"
	url := ""
	if enabled {
		url = s.tunnelURL(ctx, app.TunnelName())
	}
	writeJSON(w, http.StatusOK, map[string]any{"supported": true, "enabled": enabled, "url": url})
}

// tunnelSet enables or disables a Cloudflare quick tunnel for an app. Enabling runs
// a host-networked cloudflared container pointed at the app's host port, then waits
// for it to report its public URL.
func (s *Server) tunnelSet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || !app.Tunnelable() {
		s.auditMutation(r, "exposure", "app-public", r.PathValue("id"), "failed", "app cannot be shared online", http.StatusNotFound)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "app cannot be shared online"})
		return
	}
	if r.Header.Get("X-Cloudless-Action") != "app-public-exposure" {
		s.auditMutation(r, "exposure", "app-public", app.ID, "denied", "confirmation header required", http.StatusForbidden)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "public exposure confirmation header required"})
		return
	}
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.auditMutation(r, "exposure", "app-public", app.ID, "failed", "invalid request", http.StatusBadRequest)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	name := app.TunnelName()
	action := "disable"
	if body.Enable {
		action = "enable"
	}
	s.auditMutation(r, "exposure", "app-public-"+action, app.ID, "started", "", http.StatusOK)

	if !body.Enable {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		_ = s.eng.Remove(ctx, name)
		s.auditMutation(r, "exposure", "app-public-disable", app.ID, "succeeded", "", http.StatusOK)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "url": ""})
		return
	}
	if !s.appPublicAuthReady(app) {
		s.auditMutation(r, "exposure", "app-public-enable", app.ID, "failed", "application authentication not configured", http.StatusConflict)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "enable and configure this application's authentication before sharing it publicly"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	_ = s.eng.Remove(ctx, name) // clear any stale tunnel first
	img := s.infraImage(ctx, "cloudflared", catalog.CloudflaredImage)
	if err := s.eng.Pull(ctx, img); err != nil {
		s.auditMutation(r, "exposure", "app-public-enable", app.ID, "failed", err.Error(), http.StatusInternalServerError)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not fetch cloudflared: " + err.Error()})
		return
	}
	if _, err := s.eng.Run(ctx, engine.RunSpec{
		Name:    name,
		Image:   img,
		Network: "host", // share host netns so localhost:<hostPort> reaches the app (works for host-networked apps too)
		Args:    []string{"tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://localhost:%d", app.PrimaryHostPort())},
	}); err != nil {
		s.auditMutation(r, "exposure", "app-public-enable", app.ID, "failed", err.Error(), http.StatusInternalServerError)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Wait for cloudflared to register and print its public URL.
	url := ""
	for url == "" {
		if url = s.tunnelURL(ctx, name); url != "" {
			break
		}
		select {
		case <-ctx.Done():
			s.auditMutation(r, "exposure", "app-public-enable", app.ID, "succeeded", "public URL pending", http.StatusOK)
			writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "url": "", "pending": true})
			return
		case <-time.After(600 * time.Millisecond):
		}
	}
	// The public URL is returned to the local UI but deliberately omitted from
	// the security log; only the exposed application identity is retained.
	s.auditMutation(r, "exposure", "app-public-enable", app.ID, "succeeded", "", http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "url": url})
}

func (s *Server) appPublicAuthReady(app catalog.App) bool {
	switch app.ID {
	case "open-webui":
		value := apps.EnvOverrides(s.appConfigDir(app.ID), app)["WEBUI_AUTH"]
		return strings.EqualFold(value, "true")
	default:
		if app.Admin == nil || app.Admin.ManagedPassword == "" {
			return false
		}
		value, err := apps.ManagedSecret(s.appConfigDir(app.ID), app.ID, app.Admin.ManagedPassword)
		return err == nil && value != ""
	}
}
