package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddedInterfaceIsNeverReusedAcrossUpdates(t *testing.T) {
	s := &Server{}
	response := httptest.NewRecorder()
	s.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("interface response = %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("interface cache policy = %q", response.Header().Get("Cache-Control"))
	}
}
