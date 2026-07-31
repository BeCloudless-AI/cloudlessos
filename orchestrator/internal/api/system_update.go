package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/nvidiaupdate"
	"github.com/cloudless/orchestrator/internal/osupdate"
	"github.com/cloudless/orchestrator/internal/privileged"
)

func (s *Server) systemUpdateGet(w http.ResponseWriter, _ *http.Request) {
	status, err := osupdate.Read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) systemUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if err := s.runPrivileged(r.Context(), privileged.ActionSystemUpdateCheck); err != nil {
		s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "system-check", Outcome: "failed", Actor: "local-ui", Detail: err.Error()})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "system-check", Outcome: "started", Actor: "local-ui", Target: "cloudless-update-check.service"})
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "check"})
}

func (s *Server) systemUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "update" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "update confirmation required"})
		return
	}
	if err := s.runPrivileged(r.Context(), privileged.ActionSystemUpdateApply); err != nil {
		s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "system-apply", Outcome: "failed", Actor: "local-ui", Detail: err.Error()})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "system-apply", Outcome: "started", Actor: "local-ui", Target: "cloudless-update-apply.service"})
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "apply"})
}

func (s *Server) runPrivileged(ctx context.Context, action privileged.Action) error {
	if s.privilegedAction == nil {
		return errors.New("privileged broker is not configured")
	}
	return s.privilegedAction(ctx, action)
}

func (s *Server) runPrivilegedValue(ctx context.Context, action privileged.Action, value string) error {
	if s.privilegedValue == nil {
		return errors.New("privileged broker is not configured")
	}
	return s.privilegedValue(ctx, action, value)
}

func (s *Server) nvidiaDriverGet(w http.ResponseWriter, _ *http.Request) {
	status, err := nvidiaupdate.Read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) nvidiaDriverCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.GenericDriverUpdates) {
		return
	}
	if err := s.runPrivileged(r.Context(), privileged.ActionNVIDIAUpdateCheck); err != nil {
		s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "nvidia-check", Outcome: "failed", Actor: "local-ui", Detail: err.Error()})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "nvidia-check", Outcome: "started", Actor: "local-ui", Target: "cloudless-nvidia-check.service"})
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "nvidia-check"})
}

func (s *Server) nvidiaDriverApply(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, capabilities.GenericDriverUpdates) {
		return
	}
	if r.Header.Get("X-Cloudless-Action") != "nvidia-driver" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "driver update confirmation required"})
		return
	}
	if err := s.runPrivileged(r.Context(), privileged.ActionNVIDIAUpdateApply); err != nil {
		s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "nvidia-apply", Outcome: "failed", Actor: "local-ui", Detail: err.Error()})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "update", Event: "nvidia-apply", Outcome: "started", Actor: "local-ui", Target: "cloudless-nvidia-apply.service"})
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "nvidia-apply"})
}
