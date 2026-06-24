package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "app cannot be shared online"})
		return
	}
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	name := app.TunnelName()

	if !body.Enable {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		_ = s.eng.Remove(ctx, name)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "url": ""})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	_ = s.eng.Remove(ctx, name) // clear any stale tunnel first
	img := s.infraImage(ctx, "cloudflared", catalog.CloudflaredImage)
	if err := s.eng.Pull(ctx, img); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not fetch cloudflared: " + err.Error()})
		return
	}
	if _, err := s.eng.Run(ctx, engine.RunSpec{
		Name:    name,
		Image:   img,
		Network: "host", // share host netns so localhost:<hostPort> reaches the app (works for host-networked apps too)
		Args:    []string{"tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://localhost:%d", app.PrimaryHostPort())},
	}); err != nil {
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
			writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "url": "", "pending": true})
			return
		case <-time.After(600 * time.Millisecond):
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "url": url})
}
