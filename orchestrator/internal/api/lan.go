package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/provision"
)

// lanGet reports whether an app is served on the local network, with the address.
func (s *Server) lanGet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	if !app.HasWebPort() {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	c, _ := s.eng.Find(ctx, app.LanName())
	enabled := c != nil && c.State == "running"
	ip := provision.PrimaryLANIP()
	port := app.PrimaryHostPort()
	url := ""
	if enabled && ip != "" {
		url = fmt.Sprintf("http://%s:%d%s", ip, port, app.OpenPath)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"supported": true, "enabled": enabled, "ip": ip, "port": port, "url": url,
	})
}

// lanSet turns local-network serving on or off for a single app.
func (s *Server) lanSet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || !app.HasWebPort() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "app cannot be served on the LAN"})
		return
	}
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	name := app.LanName()

	if !body.Enable {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		_ = s.eng.Remove(ctx, name)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "url": ""})
		return
	}

	ip := provision.PrimaryLANIP()
	if ip == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not detect this machine's network address"})
		return
	}
	port := app.PrimaryHostPort()
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	_ = s.eng.Remove(ctx, name) // clear any stale forwarder (e.g. old IP)
	img := s.infraImage(ctx, "socat", catalog.SocatImage)
	if err := s.eng.Pull(ctx, img); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not fetch forwarder: " + err.Error()})
		return
	}
	spec := app.LanSidecarSpec(ip)
	spec.Image = img
	if _, err := s.eng.Run(ctx, spec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "ip": ip, "port": port,
		"url": fmt.Sprintf("http://%s:%d%s", ip, port, app.OpenPath),
	})
}

// localNetGet returns the machine-wide local-network preference.
func (s *Server) localNetGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": s.state.LocalNetwork(),
		"ip":      provision.PrimaryLANIP(),
	})
}

// localNetSet records the machine-wide preference and applies it to all running
// web apps (start/stop their LAN forwarders).
func (s *Server) localNetSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	_ = s.state.SetLocalNetwork(body.Enable)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		provision.EnsureLAN(ctx, s.eng, s.manifest, provision.PrimaryLANIP(), body.Enable, func(string) {})
	}()
	writeJSON(w, http.StatusOK, map[string]any{"enabled": body.Enable, "ip": provision.PrimaryLANIP()})
}
