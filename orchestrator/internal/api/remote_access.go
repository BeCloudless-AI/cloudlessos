package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/cloudless/orchestrator/internal/remoteaccess"
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
		s.auditMutation(r, "remote-access", "tailscale-install", "tailscale", "denied", "confirmation header required", http.StatusForbidden)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation confirmation header required"})
		return
	}
	s.auditMutation(r, "remote-access", "tailscale-install", "tailscale", "started", "", http.StatusAccepted)
	if err := s.remoteAccess.Install(r.Context()); err != nil {
		s.auditMutation(r, "remote-access", "tailscale-install", "tailscale", "failed", err.Error(), http.StatusBadGateway)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.auditMutation(r, "remote-access", "tailscale-install", "tailscale", "queued", "", http.StatusAccepted)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "installing"})
}

func (s *Server) tailscaleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "tailscale-connect" {
		s.auditMutation(r, "remote-access", "tailscale-connect", "tailscale", "denied", "confirmation header required", http.StatusForbidden)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "connection confirmation header required"})
		return
	}
	s.auditMutation(r, "remote-access", "tailscale-connect", "tailscale", "started", "", http.StatusOK)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	url, err := s.remoteAccess.Connect(ctx)
	if err != nil {
		s.auditMutation(r, "remote-access", "tailscale-connect", "tailscale", "failed", err.Error(), http.StatusBadGateway)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// The one-time Tailscale authorization URL is intentionally never written
	// to the security audit log.
	s.auditMutation(r, "remote-access", "tailscale-connect", "tailscale", "succeeded", "", http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]string{"status": "connecting", "authURL": url})
}

func (s *Server) tailscaleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "tailscale-logout" {
		s.auditMutation(r, "remote-access", "tailscale-logout", "tailscale", "denied", "confirmation header required", http.StatusForbidden)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "logout confirmation header required"})
		return
	}
	s.auditMutation(r, "remote-access", "tailscale-logout", "tailscale", "started", "", http.StatusOK)
	if err := s.remoteAccess.Logout(r.Context()); err != nil {
		s.auditMutation(r, "remote-access", "tailscale-logout", "tailscale", "failed", err.Error(), http.StatusBadGateway)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.auditMutation(r, "remote-access", "tailscale-logout", "tailscale", "succeeded", "", http.StatusOK)
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
	if r.Header.Get("X-Cloudless-Action") != "tailscale-ssh" {
		s.auditMutation(r, "remote-access", "tailscale-ssh", "tailscale", "denied", "confirmation header required", http.StatusForbidden)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "SSH change confirmation header required"})
		return
	}
	input, ok := decodeTailscaleToggle(w, r)
	if !ok {
		s.auditMutation(r, "remote-access", "tailscale-ssh", "tailscale", "failed", "invalid toggle request", http.StatusBadRequest)
		return
	}
	target := "disabled"
	if input.Enabled {
		target = "enabled"
	}
	s.auditMutation(r, "remote-access", "tailscale-ssh", target, "started", "", http.StatusOK)
	if err := s.remoteAccess.SetSSH(r.Context(), input.Enabled); err != nil {
		s.auditMutation(r, "remote-access", "tailscale-ssh", target, "failed", err.Error(), http.StatusBadGateway)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.auditMutation(r, "remote-access", "tailscale-ssh", target, "succeeded", "", http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": input.Enabled})
}

func (s *Server) tailscaleServe(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "tailscale-serve" {
		s.auditMutation(r, "remote-access", "tailscale-serve", "tailscale", "denied", "confirmation header required", http.StatusForbidden)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "serve change confirmation header required"})
		return
	}
	input, ok := decodeTailscaleToggle(w, r)
	if !ok {
		s.auditMutation(r, "remote-access", "tailscale-serve", "tailscale", "failed", "invalid toggle request", http.StatusBadRequest)
		return
	}
	target := "disabled"
	if input.Enabled {
		target = "enabled"
	}
	s.auditMutation(r, "remote-access", "tailscale-serve", target, "started", "", http.StatusOK)
	if err := s.remoteAccess.SetServe(r.Context(), input.Enabled); err != nil {
		if approvalURL := remoteaccess.ServeApprovalURL(err); input.Enabled && approvalURL != "" {
			s.auditMutation(r, "remote-access", "tailscale-serve", target, "approval-required", "tailnet administrator approval required", http.StatusAccepted)
			writeJSON(w, http.StatusAccepted, map[string]any{
				"enabled": false, "activationRequired": true, "authURL": approvalURL,
			})
			return
		}
		s.auditMutation(r, "remote-access", "tailscale-serve", target, "failed", err.Error(), http.StatusBadGateway)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.auditMutation(r, "remote-access", "tailscale-serve", target, "succeeded", "", http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": input.Enabled})
}
