package api

import (
	"fmt"
	"net/http"
	"os/exec"

	"github.com/cloudless/orchestrator/internal/nvidiaupdate"
	"github.com/cloudless/orchestrator/internal/osupdate"
)

func (s *Server) systemUpdateGet(w http.ResponseWriter, _ *http.Request) {
	status, err := osupdate.Read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) systemUpdateCheck(w http.ResponseWriter, _ *http.Request) {
	if err := startUpdateUnit("cloudless-update-check.service"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "check"})
}

func (s *Server) systemUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "update" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "update confirmation required"})
		return
	}
	if err := startUpdateUnit("cloudless-update-apply.service"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "apply"})
}

func startUpdateUnit(unit string) error {
	out, err := exec.Command("systemctl", "start", "--no-block", unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("start %s: %w: %s", unit, err, out)
	}
	return nil
}

func (s *Server) nvidiaDriverGet(w http.ResponseWriter, _ *http.Request) {
	status, err := nvidiaupdate.Read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) nvidiaDriverCheck(w http.ResponseWriter, _ *http.Request) {
	if err := startUpdateUnit("cloudless-nvidia-check.service"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "nvidia-check"})
}

func (s *Server) nvidiaDriverApply(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "nvidia-driver" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "driver update confirmation required"})
		return
	}
	if err := startUpdateUnit("cloudless-nvidia-apply.service"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "operation": "nvidia-apply"})
}
