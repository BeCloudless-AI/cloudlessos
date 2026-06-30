package api

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudless/orchestrator/internal/hardware"
)

// SamplePower reads the GPUs' board power and folds it into the electricity log.
// Called on the same ticker as SampleUsage so consumption accrues continuously
// (including idle draw), independent of which engine — or none — is running.
func (s *Server) SamplePower() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	gpus, err := hardware.GPUs(ctx)
	if err != nil || len(gpus) == 0 {
		return // no reading → don't advance the clock, so the next interval is correct
	}
	total := 0.0
	for _, g := range gpus {
		if g.PowerW > 0 {
			total += g.PowerW
		}
	}
	s.power.Sample(total, time.Now())
}

// enginePower returns the electricity report for the requested range
// (?range=hour|day|month|year; default day).
func (s *Server) enginePower(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.power.Snapshot(r.URL.Query().Get("range")))
}

// enginePowerClear erases logged electricity. ?key= empty wipes the whole log;
// otherwise it deletes the bucket (range,key) and returns the updated report.
func (s *Server) enginePowerClear(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	s.power.Clear(rng, r.URL.Query().Get("key"))
	s.power.Flush()
	writeJSON(w, http.StatusOK, s.power.Snapshot(rng))
}
