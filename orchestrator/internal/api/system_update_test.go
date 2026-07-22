package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/osupdate"
)

func TestSystemUpdateGet(t *testing.T) {
	t.Setenv("CLOUDLESS_UPDATE_STATUS", filepath.Join(t.TempDir(), "status.json"))
	status := osupdate.DefaultStatus()
	status.State = "available"
	status.CurrentVersion = "0.1.0"
	status.AvailableVersion = "0.1.1"
	if err := osupdate.Write(status); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	(&Server{}).systemUpdateGet(rec, httptest.NewRequest(http.MethodGet, "/api/system/update", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"availableVersion":"0.1.1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSystemUpdateApplyRequiresConfirmation(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).systemUpdateApply(rec, httptest.NewRequest(http.MethodPost, "/api/system/update/apply", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
