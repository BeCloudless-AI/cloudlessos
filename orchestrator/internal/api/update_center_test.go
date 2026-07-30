package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/osupdate"
)

type updateInventoryEngine struct {
	engine.Engine
	containers map[string]string
	remote     map[string]string
}

func (e *updateInventoryEngine) ContainerImageDigest(_ context.Context, name string) (string, error) {
	return e.containers[name], nil
}
func (e *updateInventoryEngine) ImageDigest(context.Context, string) (string, error) { return "", nil }
func (e *updateInventoryEngine) RemoteDigest(_ context.Context, image string) (string, error) {
	return e.remote[image], nil
}
func (e *updateInventoryEngine) Find(context.Context, string) (*engine.Container, error) {
	return nil, nil
}

func TestUpdateCenterAggregatesSystemAndInstalledAppUpdates(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	t.Setenv("CLOUDLESS_UPDATE_STATUS", filepath.Join(t.TempDir(), "status.json"))
	status := osupdate.DefaultStatus()
	status.State, status.CurrentVersion, status.AvailableVersion = "available", "0.2.4", "0.2.5"
	if err := osupdate.Write(status); err != nil {
		t.Fatal(err)
	}
	eng := &updateInventoryEngine{
		containers: map[string]string{"cloudless-open-webui": "sha256:old"},
		remote:     map[string]string{"ghcr.io/open-webui/open-webui:main": "sha256:new"},
	}
	server := &Server{eng: eng, jobs: jobs.NewManager()}
	recorder := httptest.NewRecorder()
	server.updateCenterGet(recorder, httptest.NewRequest(http.MethodGet, "/api/updates", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response updateCenterStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.System.AvailableVersion != "0.2.5" || response.DriverOwner != "nvidia-dgx" {
		t.Fatalf("unexpected system aggregate: %#v", response)
	}
	if response.UpdateCount != 2 {
		t.Fatalf("update count=%d, want system + app", response.UpdateCount)
	}
	if len(response.Apps) != 1 || response.Apps[0].ID != "open-webui" || !response.Apps[0].HasUpdate {
		t.Fatalf("unexpected app inventory: %#v", response.Apps)
	}
}

func TestUpdateCenterBulkApplyRequiresExplicitConfirmation(t *testing.T) {
	server := &Server{jobs: jobs.NewManager()}
	recorder := httptest.NewRecorder()
	server.updateCenterAppsApply(recorder, httptest.NewRequest(http.MethodPost, "/api/updates/apps/apply", bytes.NewBufferString(`{"ids":["open-webui"]}`)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "confirmation") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateCenterBulkApplyReconnectsToSelectedBackgroundJobs(t *testing.T) {
	manager := jobs.NewManager()
	existing := manager.Create("app:open-webui:update")
	server := &Server{jobs: manager, eng: &updateInventoryEngine{containers: map[string]string{"cloudless-open-webui": "sha256:old"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/updates/apps/apply", bytes.NewBufferString(`{"ids":["open-webui"]}`))
	request.Header.Set("X-Cloudless-Action", "update-apps")
	recorder := httptest.NewRecorder()
	server.updateCenterAppsApply(recorder, request)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), existing.ID) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAppUpdateRejectsConflictingSameAppOperation(t *testing.T) {
	manager := jobs.NewManager()
	manager.Create("app:open-webui:install")
	server := &Server{jobs: manager, eng: &updateInventoryEngine{containers: map[string]string{"cloudless-open-webui": "sha256:old"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/apps/open-webui/update", nil)
	request.SetPathValue("id", "open-webui")
	recorder := httptest.NewRecorder()
	server.updateApply(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "background operation") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAppUpdateCannotInstallAnUninstalledApp(t *testing.T) {
	server := &Server{jobs: jobs.NewManager(), eng: &updateInventoryEngine{containers: map[string]string{}}}
	request := httptest.NewRequest(http.MethodPost, "/api/apps/open-webui/update", nil)
	request.SetPathValue("id", "open-webui")
	recorder := httptest.NewRecorder()
	server.updateApply(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "not installed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
