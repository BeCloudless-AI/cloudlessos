package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func readJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func (s *Server) tailscaleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.remoteAccess.Status(ctx))
}

func (s *Server) tailscaleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "tailscale-install" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation confirmation header required"})
		return
	}
	if err := s.remoteAccess.Install(r.Context()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "installing"})
}

func (s *Server) tailscaleConnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	url, err := s.remoteAccess.Connect(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "connecting", "authURL": url})
}

func (s *Server) tailscaleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "tailscale-logout" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "logout confirmation header required"})
		return
	}
	if err := s.remoteAccess.Logout(r.Context()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged-out"})
}

type tailscaleToggleRequest struct {
	Enabled bool `json:"enabled"`
}

func decodeTailscaleToggle(w http.ResponseWriter, r *http.Request) (tailscaleToggleRequest, bool) {
	var input tailscaleToggleRequest
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid toggle request"})
		return input, false
	}
	return input, true
}

func (s *Server) tailscaleSSH(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeTailscaleToggle(w, r)
	if !ok {
		return
	}
	if err := s.remoteAccess.SetSSH(r.Context(), input.Enabled); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": input.Enabled})
}

func (s *Server) tailscaleServe(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeTailscaleToggle(w, r)
	if !ok {
		return
	}
	if err := s.remoteAccess.SetServe(r.Context(), input.Enabled); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": input.Enabled})
}
