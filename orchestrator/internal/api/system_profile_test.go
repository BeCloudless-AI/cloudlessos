package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/privileged"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestProfileTimezoneUsesTypedPrivilegedAction(t *testing.T) {
	t.Setenv("TZ", "UTC")
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var gotAction privileged.Action
	var gotValue string
	server := &Server{
		state: store,
		privilegedValue: func(_ context.Context, action privileged.Action, value string) error {
			gotAction, gotValue = action, value
			return nil
		},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/profile", strings.NewReader(`{"name":"Cloudless","region":"AE"}`))
	rec := httptest.NewRecorder()
	server.profileSet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotAction != privileged.ActionTimezoneSet || gotValue != "Asia/Dubai" {
		t.Fatalf("action=%q value=%q", gotAction, gotValue)
	}
	if profile := store.Profile(); profile.Region != "AE" {
		t.Fatalf("profile=%#v", profile)
	}
}
