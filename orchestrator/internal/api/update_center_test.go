package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/osupdate"
	"github.com/cloudless/orchestrator/internal/state"
)

type updateInventoryEngine struct {
	engine.Engine
	mu         sync.Mutex
	containers map[string]string
	images     map[string]string
	remote     map[string]string
	found      map[string]*engine.Container
	imageRows  string
	pulled     []string
	pullDigest map[string]string
}

func (e *updateInventoryEngine) ContainerImageDigest(_ context.Context, name string) (string, error) {
	return e.containers[name], nil
}
func (e *updateInventoryEngine) ImageDigest(_ context.Context, image string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.images[image], nil
}
func (e *updateInventoryEngine) RemoteDigest(_ context.Context, image string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.remote[image], nil
}
func (e *updateInventoryEngine) Find(_ context.Context, name string) (*engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.found[name], nil
}
func (e *updateInventoryEngine) Output(context.Context, ...string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.imageRows, nil
}
func (e *updateInventoryEngine) ListImageDigests(context.Context) ([]engine.ImageDigestRef, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var result []engine.ImageDigestRef
	for _, line := range strings.Split(e.imageRows, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) == 3 {
			result = append(result, engine.ImageDigestRef{Repository: parts[0], Tag: parts[1], Digest: parts[2]})
		}
	}
	return result, nil
}
func (e *updateInventoryEngine) PullStream(_ context.Context, image string, onLine func(string)) error {
	e.mu.Lock()
	e.pulled = append(e.pulled, image)
	if e.images == nil {
		e.images = map[string]string{}
	}
	digest := e.pullDigest[image]
	if digest == "" {
		digest = e.remote[image]
	}
	if digest == "" {
		if at := strings.LastIndex(image, "@sha256:"); at >= 0 {
			digest = image[at+1:]
		}
	}
	if digest != "" {
		e.images[image] = digest
	}
	e.mu.Unlock()
	if onLine != nil {
		onLine("Status: Downloaded newer image")
	}
	return nil
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

func TestUpdateCenterIncludesInstalledManagedEngineUpdates(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	t.Setenv("CLOUDLESS_UPDATE_STATUS", filepath.Join(t.TempDir(), "status.json"))
	if err := osupdate.Write(osupdate.DefaultStatus()); err != nil {
		t.Fatal(err)
	}
	app, ok := catalog.Get("vllm")
	if !ok {
		t.Fatal("managed vLLM catalog entry is missing")
	}
	eng := &updateInventoryEngine{
		containers: map[string]string{},
		images:     map[string]string{app.Image: "sha256:old-engine"},
		remote:     map[string]string{app.Image: "sha256:new-engine"},
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
	if response.UpdateCount != 1 {
		t.Fatalf("update count=%d, want one managed engine update", response.UpdateCount)
	}
	if len(response.Engines) != 1 || response.Engines[0].ID != "vllm" || !response.Engines[0].HasUpdate {
		t.Fatalf("unexpected managed engine inventory: %#v", response.Engines)
	}
	if response.Engines[0].Current != "old-engine" || response.Engines[0].Latest != "new-engine" {
		t.Fatalf("unexpected engine versions: %#v", response.Engines[0])
	}
}

func TestManagedEngineUpdateRequiresExplicitConfirmation(t *testing.T) {
	server := &Server{jobs: jobs.NewManager()}
	request := httptest.NewRequest(http.MethodPost, "/api/updates/engines/vllm/apply", nil)
	request.SetPathValue("id", "vllm")
	recorder := httptest.NewRecorder()
	server.updateCenterEngineApply(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "confirmation") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestManagedEngineStatusOnlyRestartsTheActiveBaseRuntime(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	app, ok := catalog.Get("vllm")
	if !ok {
		t.Fatal("managed vLLM catalog entry is missing")
	}
	eng := &updateInventoryEngine{
		containers: map[string]string{app.ContainerName(): "sha256:old-engine"},
		images:     map[string]string{app.Image: "sha256:old-engine"},
		remote:     map[string]string{app.Image: "sha256:new-engine"},
		found: map[string]*engine.Container{
			app.ContainerName(): {Name: app.ContainerName(), Image: app.Image, State: "running"},
		},
	}
	status := (&Server{eng: eng}).managedEngineUpdate(context.Background(), app, app.ID)
	if !status.Active || !status.RestartOnUpdate || !status.HasUpdate {
		t.Fatalf("active managed base runtime was not recognized: %#v", status)
	}

	eng.found[app.ContainerName()].Image = "local/cloudless-model-runtime:test"
	status = (&Server{eng: eng}).managedEngineUpdate(context.Background(), app, app.ID)
	if !status.Active || status.RestartOnUpdate || !status.HasUpdate {
		t.Fatalf("model-specific runtime should be updated without replacement: %#v", status)
	}
}

func TestManagedEngineDigestPullDoesNotRepeatUpdate(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	app, ok := catalog.Get("sglang")
	if !ok {
		t.Fatal("managed SGLang catalog entry is missing")
	}
	const desired = "sha256:d4a984cdeb9846ef0d433d80e8fff55d527fa84f91c0eec2c62d9dea7ab26426"
	document := `{"manifestVersion":2,"channel":"stable","apps":{"sglang":{"image":"lmsysorg/sglang","digest":"` + desired + `","architectures":["amd64"],"verified":true}}}`
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(document))
	}))
	defer manifestServer.Close()
	reviewedRef := "lmsysorg/sglang@" + desired
	eng := &updateInventoryEngine{
		containers: map[string]string{},
		images: map[string]string{
			app.Image:   "sha256:old-engine",
			reviewedRef: desired,
		},
		remote: map[string]string{},
	}
	server := &Server{eng: eng, manifest: manifest.New(manifestServer.URL)}
	status := server.managedEngineUpdate(context.Background(), app, app.ID)
	if !status.Installed || status.HasUpdate || status.Current != shortDigest(desired) {
		t.Fatalf("downloaded reviewed digest was offered repeatedly: %#v", status)
	}
}

func TestManagedEngineUpdateRejectsCustomEngineIDs(t *testing.T) {
	server := &Server{jobs: jobs.NewManager()}
	request := httptest.NewRequest(http.MethodPost, "/api/updates/engines/custom-vllm/apply", nil)
	request.SetPathValue("id", "custom-vllm")
	request.Header.Set("X-Cloudless-Action", "update-engine")
	recorder := httptest.NewRecorder()
	server.updateCenterEngineApply(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("custom engine update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInactiveManagedEngineUpdatePullsInBackgroundWithoutRestart(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	app, ok := catalog.Get("sglang")
	if !ok {
		t.Fatal("managed SGLang catalog entry is missing")
	}
	eng := &updateInventoryEngine{
		containers: map[string]string{},
		images:     map[string]string{app.Image: "sha256:old-engine"},
		remote:     map[string]string{app.Image: "sha256:new-engine"},
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.NewManager()
	server := &Server{eng: eng, jobs: manager, state: store}
	request := httptest.NewRequest(http.MethodPost, "/api/updates/engines/sglang/apply", nil)
	request.SetPathValue("id", "sglang")
	request.Header.Set("X-Cloudless-Action", "update-engine")
	recorder := httptest.NewRecorder()
	server.updateCenterEngineApply(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	job, ok := manager.Get(response["jobId"])
	if !ok {
		t.Fatal("engine update did not create a background job")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !job.Snapshot().Done && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := job.Snapshot(); !snapshot.Done || snapshot.Phase != "running" || snapshot.Error != "" {
		t.Fatalf("engine update did not complete: %#v", snapshot)
	}
	eng.mu.Lock()
	pulled := append([]string(nil), eng.pulled...)
	eng.mu.Unlock()
	exactImage := imageRepository(app.Image) + "@sha256:new-engine"
	if len(pulled) != 1 || pulled[0] != exactImage {
		t.Fatalf("pulled images=%v, want %q", pulled, exactImage)
	}
	artifact, ok := store.ManagedEngineArtifact(app.ID)
	if !ok || artifact.Image != exactImage || artifact.DownloadedDigest != "sha256:new-engine" || artifact.ActiveDigest != "" {
		t.Fatalf("unexpected managed artifact: %#v, ok=%v", artifact, ok)
	}
	if status := server.managedEngineUpdate(context.Background(), app, app.ID); status.HasUpdate {
		t.Fatalf("downloaded exact engine target was offered again: %#v", status)
	}
}

func TestManagedEngineUpdateRejectsPulledDigestMismatch(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	app, ok := catalog.Get("sglang")
	if !ok {
		t.Fatal("managed SGLang catalog entry is missing")
	}
	exactImage := imageRepository(app.Image) + "@sha256:expected"
	eng := &updateInventoryEngine{
		containers: map[string]string{},
		images:     map[string]string{app.Image: "sha256:old"},
		remote:     map[string]string{app.Image: "sha256:expected"},
		pullDigest: map[string]string{exactImage: "sha256:unexpected"},
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.NewManager()
	server := &Server{eng: eng, jobs: manager, state: store}
	request := httptest.NewRequest(http.MethodPost, "/api/updates/engines/sglang/apply", nil)
	request.SetPathValue("id", "sglang")
	request.Header.Set("X-Cloudless-Action", "update-engine")
	recorder := httptest.NewRecorder()
	server.updateCenterEngineApply(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	job, _ := manager.Get(response["jobId"])
	deadline := time.Now().Add(2 * time.Second)
	for !job.Snapshot().Done && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := job.Snapshot(); snapshot.Error == "" || !strings.Contains(snapshot.Error, "does not match target") {
		t.Fatalf("digest mismatch was not rejected: %#v", snapshot)
	}
	if _, ok := store.ManagedEngineArtifact(app.ID); ok {
		t.Fatal("unverified engine artifact was persisted")
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
