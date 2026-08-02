package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/locale"
	"github.com/cloudless/orchestrator/internal/privileged"
	"github.com/cloudless/orchestrator/internal/state"
)

// effectiveCountry resolves the machine's country: the user's profile override if
// set, otherwise the OS-detected country. Returns the code, its display name, the
// raw detection info, and the source ("override" | detection source | "").
func (s *Server) effectiveCountry() (code, name string, info locale.Info, source string) {
	info = locale.Detect()
	code, source = info.Country, info.Source
	if ov := strings.ToUpper(strings.TrimSpace(s.state.Profile().Region)); ov != "" {
		code, source = ov, "override"
	}
	return code, locale.CountryName(code), info, source
}

// inFrance reports whether the machine's effective country is France.
func (s *Server) inFrance() bool {
	code, _, _, _ := s.effectiveCountry()
	return code == "FR"
}

// system reports host hardware + OS facts and the detected locale/timezone.
func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	gpus, _ := hardware.GPUs(ctx)
	memoryGB, _ := hardware.AcceleratorMemoryGB(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"system":       hardware.Sys(),
		"locale":       locale.Detect(),
		"gpuCount":     len(gpus),
		"capabilities": capabilities.Current(),
		"cluster":      s.cachedClusterCompute(ctx, memoryGB),
	})
}

// systemInput reports hot-pluggable physical input classes independently from
// the heavier system endpoint so the kiosk can react quickly to USB changes.
func (s *Server) systemInput(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, hardware.Inputs())
}

// sysload reports live host utilization and capacity for the dashboard.
func (s *Server) sysload(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, hardware.Load())
}

// profileGet returns the user profile plus the resolved locale/region picture so
// the UI can show the detected timezone/country and the effective (overridable) one.
func (s *Server) profileGet(w http.ResponseWriter, r *http.Request) {
	p := s.state.Profile()
	code, name, info, source := s.effectiveCountry()
	writeJSON(w, http.StatusOK, map[string]any{
		"name":          p.Name,
		"region":        p.Region, // the override ("" = auto)
		"regions":       locale.Regions(),
		"detected":      info, // OS-detected timezone/locale/country
		"country":       code, // effective country (override or detected)
		"countryName":   name,
		"countrySource": source, // "override" | "locale" | "timezone" | ""
		"inFrance":      code == "FR",
	})
}

// profileSet updates the user profile (name + region override).
func (s *Server) profileSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Region string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	region := strings.ToUpper(strings.TrimSpace(body.Region))
	if zone := locale.DefaultTimezone(region); zone != "" && zone != locale.Detect().Timezone {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := s.runPrivilegedValue(ctx, privileged.ActionTimezoneSet, zone); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	p := state.Profile{
		Name:   strings.TrimSpace(body.Name),
		Region: region,
	}
	if err := s.state.SetProfile(p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Echo back the resolved picture so the client can refresh in one round-trip.
	s.profileGet(w, r)
}
