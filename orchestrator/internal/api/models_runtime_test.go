package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewedSingleNodeModelRejectsClusterLaunch(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/settings/model",
		strings.NewReader(`{"model":"nvidia/LocateAnything-3B","mode":"cluster"}`))
	rec := httptest.NewRecorder()
	(&Server{}).settingsModel(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "single-Spark") {
		t.Fatalf("cluster launch was not rejected: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
