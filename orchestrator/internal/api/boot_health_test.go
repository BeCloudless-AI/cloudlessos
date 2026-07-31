package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestBootHealthReportsMissingAndValidAudit(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}

	req := httptest.NewRequest(http.MethodGet, "/api/system/boot-health", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"available\":false}\n" {
		t.Fatalf("missing audit response = %d %s", rec.Code, rec.Body.String())
	}

	audit := `{"schema":"cloudless.boot-health.v1","healthy":true,"consecutiveHealthyBoots":4,"reason":"ready"}`
	if err := os.WriteFile(filepath.Join(store.Dir(), "boot-health.json"), []byte(audit), 0o640); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid audit status = %d: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["available"] != true || result["healthy"] != true || result["consecutiveHealthyBoots"] != float64(4) {
		t.Fatalf("valid audit response = %#v", result)
	}
}

func TestBootHealthRejectsMalformedAudit(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir(), "boot-health.json"), []byte(`{"schema":"wrong"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	rec := httptest.NewRecorder()
	server.bootHealth(rec, httptest.NewRequest(http.MethodGet, "/api/system/boot-health", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("malformed audit status = %d: %s", rec.Code, rec.Body.String())
	}
}
