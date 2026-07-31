package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/nvidiaupdate"
	"github.com/cloudless/orchestrator/internal/osupdate"
	"github.com/cloudless/orchestrator/internal/privileged"
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

func TestSystemUpdateActionsUsePrivilegedBroker(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		header string
		action privileged.Action
		call   func(*Server, http.ResponseWriter, *http.Request)
	}{
		{"check", "/api/system/update/check", "", privileged.ActionSystemUpdateCheck, (*Server).systemUpdateCheck},
		{"apply", "/api/system/update/apply", "update", privileged.ActionSystemUpdateApply, (*Server).systemUpdateApply},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := make(chan privileged.Action, 1)
			server := &Server{privilegedAction: func(_ context.Context, action privileged.Action) error {
				called <- action
				return nil
			}}
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			if tt.header != "" {
				req.Header.Set("X-Cloudless-Action", tt.header)
			}
			rec := httptest.NewRecorder()
			tt.call(server, rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if action := <-called; action != tt.action {
				t.Fatalf("action=%q want=%q", action, tt.action)
			}
		})
	}
}

func TestEmbeddedSystemUpdateUIKeepsTruthfulLifecycleStates(t *testing.T) {
	payload, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(payload)
	for _, contract := range []string{
		"const showProgress = state === 'installing'",
		"state === 'available' && changelog.length",
		"state === 'reboot_required'",
		"sys-update-error",
		"Installing verified update",
		"openCloudlessDecision({",
	} {
		if !strings.Contains(source, contract) {
			t.Fatalf("embedded update UI is missing contract %q", contract)
		}
	}
}

func TestNVIDIADriverGet(t *testing.T) {
	t.Setenv("CLOUDLESS_NVIDIA_STATUS", filepath.Join(t.TempDir(), "nvidia.json"))
	status := nvidiaupdate.DefaultStatus()
	status.State = "available"
	status.HardwareDetected = true
	status.RecommendedPackage = "nvidia-driver-580"
	if err := nvidiaupdate.Write(status); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&Server{}).nvidiaDriverGet(rec, httptest.NewRequest(http.MethodGet, "/api/system/nvidia-driver", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"recommendedPackage":"nvidia-driver-580"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNVIDIADriverApplyRequiresConfirmation(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).nvidiaDriverApply(rec, httptest.NewRequest(http.MethodPost, "/api/system/nvidia-driver/apply", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDGXSparkCannotBypassDriverOwnershipThroughAPI(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	req := httptest.NewRequest(http.MethodPost, "/api/system/nvidia-driver/apply", nil)
	req.Header.Set("X-Cloudless-Action", "nvidia-driver")
	rec := httptest.NewRecorder()
	(&Server{}).nvidiaDriverApply(rec, req)
	if rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), capabilities.GenericDriverUpdates) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
