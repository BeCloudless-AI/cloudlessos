package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSupportBundleRequiresExplicitActionHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/system/doctor/bundle", nil)
	(&Server{}).systemDoctorBundle(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestPackUninstallRequiresExplicitActionHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/packs/search/uninstall", nil)
	request.SetPathValue("id", "search")
	(&Server{}).packUninstall(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
