package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSystemShutdownRequiresConfirmationHeader(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/system/shutdown", nil)
	rec := httptest.NewRecorder()

	s.systemShutdown(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestSystemShutdownQueuesPowerOff(t *testing.T) {
	called := make(chan struct{}, 1)
	s := &Server{
		shutdown:      func() error { called <- struct{}{}; return nil },
		shutdownDelay: 0,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/system/shutdown", nil)
	req.Header.Set("X-Cloudless-Action", "shutdown")
	rec := httptest.NewRecorder()

	s.systemShutdown(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("shutdown command was not called")
	}
}
