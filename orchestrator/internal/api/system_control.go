package api

import (
	"log"
	"net/http"
	"time"
)

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
			log.Printf("system shutdown failed: privileged broker is not configured")
			s.shutdownMu.Lock()
			s.shutdownQueued = false
			s.shutdownMu.Unlock()
			return
		}
		if err := shutdown(); err != nil {
			log.Printf("system shutdown failed: %v", err)
			s.shutdownMu.Lock()
			s.shutdownQueued = false
			s.shutdownMu.Unlock()
		}
	}()
}

func (s *Server) systemReboot(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "reboot" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "reboot confirmation header required"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(time.Second)
		if s.state != nil {
			s.state.PersistIfDirty()
		}
		if s.usage != nil {
			s.usage.Flush()
		}
		if s.power != nil {
			s.power.Flush()
		}
		reboot := s.reboot
		if reboot == nil {
			log.Printf("system reboot failed: privileged broker is not configured")
			return
		}
		if err := reboot(); err != nil {
			log.Printf("system reboot failed: %v", err)
		}
	}()
}
