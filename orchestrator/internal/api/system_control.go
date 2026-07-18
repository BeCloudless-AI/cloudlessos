package api

import (
	"log"
	"net/http"
	"os/exec"
	"time"
)

// systemShutdown asks systemd to power off the host. Cloudless is installed as
// a privileged local service on CloudlessOS, so this fixed command needs no
// user input or shell interpolation.
func systemShutdown() error {
	return exec.Command("systemctl", "poweroff", "--no-wall").Run()
}

func (s *Server) systemShutdown(w http.ResponseWriter, r *http.Request) {
	// A custom header makes this destructive endpoint unavailable to ordinary
	// cross-origin form posts while keeping local API/CLI use straightforward.
	if r.Header.Get("X-Cloudless-Action") != "shutdown" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "shutdown confirmation header required"})
		return
	}

	s.shutdownMu.Lock()
	if s.shutdownQueued {
		s.shutdownMu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting-down"})
		return
	}
	s.shutdownQueued = true
	delay := s.shutdownDelay
	shutdown := s.shutdown
	s.shutdownMu.Unlock()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting-down"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		if s.state != nil {
			s.state.PersistIfDirty()
		}
		if s.usage != nil {
			s.usage.Flush()
		}
		if s.power != nil {
			s.power.Flush()
		}
		if shutdown == nil {
			shutdown = systemShutdown
		}
		if err := shutdown(); err != nil {
			log.Printf("system shutdown failed: %v", err)
			s.shutdownMu.Lock()
			s.shutdownQueued = false
			s.shutdownMu.Unlock()
		}
	}()
}
