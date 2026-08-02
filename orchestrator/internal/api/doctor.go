package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudless/orchestrator/internal/doctor"
)

func (s *Server) systemDoctor(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	report := doctor.Run(ctx, s.eng, s.state)
	report = s.addDoctorRuntimeReconciliation(ctx, report)
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) systemDoctorBundle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "support-bundle" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "support bundle confirmation header required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	report := doctor.Run(ctx, s.eng, s.state)
	report = s.addDoctorRuntimeReconciliation(ctx, report)
	bundle, err := doctor.BuildBundle(ctx, s.eng, s.state, report)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not build support bundle"})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cloudless-support-%s.zip"`, time.Now().UTC().Format("20060102-150405")))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(len(bundle)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bundle)
}

func (s *Server) addDoctorRuntimeReconciliation(ctx context.Context, report doctor.Report) doctor.Report {
	if s.eng == nil || s.state == nil {
		return report
	}
	current := s.state.Get()
	active := s.activeEngine(ctx) != ""
	report = doctor.ReconcileInferenceEndpoint(report, current, active, engineEndpointError(ctx))
	if s.recipes == nil {
		return report
	}
	containers, containerErr := s.eng.List(ctx)
	recipes, recipeErr := s.recipes.List()
	if containerErr != nil || recipeErr != nil {
		return report
	}
	return doctor.AddOrphanedRecipeChecks(report, current, containers, recipes)
}
