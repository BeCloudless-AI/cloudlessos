package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestParseXrandrConnectedOutputsAndModes(t *testing.T) {
	snapshot, err := parseXrandr(`Screen 0: minimum 8 x 8, current 3840 x 2160, maximum 32767 x 32767
DP-0 connected primary 3840x2160+0+0 (normal left inverted right x axis y axis)
   3840x2160     60.00*+  59.94
   2560x1440     59.95
HDMI-0 disconnected (normal left inverted right x axis y axis)
DP-1 connected 1920x1080+3840+0 (normal left inverted right x axis y axis)
   1920x1080     60.00*+
`)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Available || snapshot.Output != "DP-0" || snapshot.Width != 3840 || snapshot.Height != 2160 {
		t.Fatalf("unexpected selected display: %+v", snapshot)
	}
	if len(snapshot.Outputs) != 2 {
		t.Fatalf("connected outputs = %d, want 2", len(snapshot.Outputs))
	}
	if len(snapshot.Outputs[0].Modes) != 2 || !snapshot.Outputs[0].Modes[0].Current || !snapshot.Outputs[0].Modes[0].Preferred {
		t.Fatalf("unexpected primary modes: %+v", snapshot.Outputs[0].Modes)
	}
}

type displayApplications struct {
	mu     sync.Mutex
	values []state.DisplayPreference
}

func (a *displayApplications) add(value state.DisplayPreference) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.values = append(a.values, value)
}

func (a *displayApplications) snapshot() []state.DisplayPreference {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]state.DisplayPreference(nil), a.values...)
}

func testDisplayServer(t *testing.T, delay time.Duration) (*Server, *displayApplications) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	applied := &displayApplications{}
	server := &Server{
		state: store, displayDelay: delay,
		displayQuery: func(context.Context) (displaySnapshot, error) {
			return displaySnapshot{Available: true, Output: "DP-0", Width: 1920, Height: 1080, Outputs: []displayOutput{{
				Name: "DP-0", Width: 1920, Height: 1080,
				Modes: []displayMode{{Width: 1920, Height: 1080, Current: true}, {Width: 2560, Height: 1440}},
			}}}, nil
		},
		displayApply: func(_ context.Context, layout, output string, width, height int) error {
			applied.add(state.DisplayPreference{Layout: layout, Output: output, Width: width, Height: height})
			return nil
		},
	}
	return server, applied
}

func applyTestDisplay(t *testing.T, server *Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/system/display", strings.NewReader(`{"output":"DP-0","width":2560,"height":1440}`))
	req.Header.Set("X-Cloudless-Action", "display")
	rec := httptest.NewRecorder()
	server.displaySet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Token == "" {
		t.Fatalf("apply response = %q (%v)", rec.Body.String(), err)
	}
	return result.Token
}

func TestDisplayChangeIsPersistedOnlyAfterConfirmation(t *testing.T) {
	server, applied := testDisplayServer(t, time.Minute)
	token := applyTestDisplay(t, server)
	if _, err := os.Stat(filepath.Join(server.state.Dir(), "display-change-pending.json")); err != nil {
		t.Fatalf("durable display rollback was not written: %v", err)
	}
	if got := server.state.DisplayPreference(); got.Width != 0 {
		t.Fatalf("temporary display mode was persisted: %+v", got)
	}
	applications := applied.snapshot()
	if len(applications) != 1 || applications[0].Width != 2560 {
		t.Fatalf("temporary mode applications = %+v", applications)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/system/display/confirm", strings.NewReader(`{"token":"`+token+`"}`))
	req.Header.Set("X-Cloudless-Action", "display-confirm")
	rec := httptest.NewRecorder()
	server.displayConfirm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := server.state.DisplayPreference(); got.Width != 2560 || got.Height != 1440 {
		t.Fatalf("confirmed display preference = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(server.state.Dir(), "display-change-pending.json")); !os.IsNotExist(err) {
		t.Fatalf("confirmed display rollback record still exists: %v", err)
	}
}

func TestPendingDisplayChangeRollsBackAfterDaemonRestart(t *testing.T) {
	server, _ := testDisplayServer(t, time.Minute)
	applyTestDisplay(t, server)
	server.displayMu.Lock()
	server.displayChange.Timer.Stop() // simulate the old daemon exiting
	server.displayMu.Unlock()

	recovered := &displayApplications{}
	restarted := &Server{
		state: server.state, displayRecoveryDelay: time.Millisecond,
		displayApply: func(_ context.Context, layout, output string, width, height int) error {
			recovered.add(state.DisplayPreference{Layout: layout, Output: output, Width: width, Height: height})
			return nil
		},
	}
	restarted.recoverPendingDisplayChange()
	deadline := time.Now().Add(time.Second)
	for len(recovered.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	applications := recovered.snapshot()
	if len(applications) != 1 || applications[0].Width != 1920 || applications[0].Height != 1080 {
		t.Fatalf("restart recovery applications = %+v", applications)
	}
	if _, err := os.Stat(filepath.Join(server.state.Dir(), "display-change-pending.json")); !os.IsNotExist(err) {
		t.Fatalf("restart recovery record still exists: %v", err)
	}
}

func TestUnconfirmedDisplayChangeAutomaticallyRollsBack(t *testing.T) {
	server, applied := testDisplayServer(t, 15*time.Millisecond)
	applyTestDisplay(t, server)
	deadline := time.Now().Add(time.Second)
	for len(applied.snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	applications := applied.snapshot()
	if len(applications) != 2 || applications[1].Width != 1920 || applications[1].Height != 1080 {
		t.Fatalf("display mode applications = %+v", applications)
	}
	if got := server.state.DisplayPreference(); got.Width != 0 {
		t.Fatalf("rolled-back mode was persisted: %+v", got)
	}
}

func TestDisplaySetRequiresConfirmationHeader(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/system/display", strings.NewReader(`{"output":"DP-0","width":1920,"height":1080}`))
	rec := httptest.NewRecorder()
	s.displaySet(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestDisplayNormalizeMirrorsUnknownMultipleOutputs(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	applied := &displayApplications{}
	server := &Server{
		state: store,
		displayQuery: func(context.Context) (displaySnapshot, error) {
			return parseXrandr(`USB-C-2 connected primary 3840x2160+3840+0
   3840x2160 60.00*+
HDMI-0 connected 3840x2160+0+0
   3840x2160 60.00*+
`)
		},
		displayApply: func(_ context.Context, layout, output string, width, height int) error {
			applied.add(state.DisplayPreference{Layout: layout, Output: output, Width: width, Height: height})
			return nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/system/display/normalize", nil)
	req.Header.Set("X-Cloudless-Action", "display-normalize")
	rec := httptest.NewRecorder()
	server.displayNormalize(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("normalize status = %d: %s", rec.Code, rec.Body.String())
	}
	applications := applied.snapshot()
	if len(applications) != 1 || applications[0].Layout != "mirror" || applications[0].Width != 3840 {
		t.Fatalf("normalization applications = %+v", applications)
	}
}

func TestDisplayNormalizeDoesNotExtendWhenSavedOutputIsMissing(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDisplayPreference(state.DisplayPreference{Layout: "single", Output: "DP-OLD", Width: 1920, Height: 1080}); err != nil {
		t.Fatal(err)
	}
	applied := &displayApplications{}
	server := &Server{
		state: store,
		displayQuery: func(context.Context) (displaySnapshot, error) {
			return parseXrandr(`DP-1 connected primary 1920x1080+0+0
   1920x1080 60.00*+
HDMI-1 connected 1920x1080+1920+0
   1920x1080 60.00*+
`)
		},
		displayApply: func(_ context.Context, layout, output string, width, height int) error {
			applied.add(state.DisplayPreference{Layout: layout, Output: output, Width: width, Height: height})
			return nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/system/display/normalize", nil)
	req.Header.Set("X-Cloudless-Action", "display-normalize")
	rec := httptest.NewRecorder()
	server.displayNormalize(rec, req)
	applications := applied.snapshot()
	if rec.Code != http.StatusOK || len(applications) != 1 || applications[0].Layout != "mirror" {
		t.Fatalf("missing-output normalization = %d %+v: %s", rec.Code, applications, rec.Body.String())
	}
	if got := store.DisplayPreference(); got.Output != "DP-OLD" {
		t.Fatalf("fallback overwrote saved preference: %+v", got)
	}
}
