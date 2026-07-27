package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemInputKeyEmitsValidatedKey(t *testing.T) {
	var action, value string
	s := &Server{virtualKey: func(_ context.Context, gotAction, gotValue string) error {
		action, value = gotAction, gotValue
		return nil
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/system/input/key", strings.NewReader(`{"action":"text","value":"@"}`))
	req.Header.Set("X-Cloudless-Action", virtualKeyboardAction)
	rec := httptest.NewRecorder()
	s.systemInputKey(rec, req)
	if rec.Code != http.StatusOK || action != "text" || value != "@" {
		t.Fatalf("status/action/value = %d/%q/%q", rec.Code, action, value)
	}
}

func TestSystemInputKeyRejectsUntrustedOrInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		header, body string
		status       int
	}{
		{"", `{"action":"enter"}`, http.StatusForbidden},
		{virtualKeyboardAction, `{"action":"text","value":"too much"}`, http.StatusBadRequest},
		{virtualKeyboardAction, `{"action":"shell","value":"reboot"}`, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/system/input/key", strings.NewReader(tc.body))
		req.Header.Set("X-Cloudless-Action", tc.header)
		rec := httptest.NewRecorder()
		(&Server{virtualKey: func(context.Context, string, string) error { t.Fatal("invalid key was emitted"); return nil }}).systemInputKey(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("status = %d, want %d", rec.Code, tc.status)
		}
	}
}
