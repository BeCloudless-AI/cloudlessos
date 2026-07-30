package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
)

func TestAppOperationLocksAllowUnrelatedAppsInParallel(t *testing.T) {
	var locks appOperationLocks
	releaseA := locks.lock("app-a")
	acquiredB := make(chan func(), 1)
	go func() { acquiredB <- locks.lock("app-b") }()
	select {
	case releaseB := <-acquiredB:
		releaseB()
	case <-time.After(250 * time.Millisecond):
		t.Fatal("an unrelated app operation was serialized")
	}
	releaseA()
}

func TestAppOperationLocksSerializeOverlappingPacksWithoutDeadlock(t *testing.T) {
	var locks appOperationLocks
	releaseFirst := locks.lock("shared", "alpha")
	acquired := make(chan func(), 1)
	go func() { acquired <- locks.lock("beta", "shared") }()
	select {
	case release := <-acquired:
		release()
		t.Fatal("overlapping operation acquired the shared app lock")
	case <-time.After(40 * time.Millisecond):
	}
	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("overlapping operation did not resume after the shared lock was released")
	}
}

type parallelRemovalEngine struct {
	engine.Engine
	entered chan string
	release chan struct{}
}

func (e *parallelRemovalEngine) Remove(ctx context.Context, name string) error {
	e.entered <- name
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *parallelRemovalEngine) RemoveImage(context.Context, string) error { return nil }

func TestUnrelatedAppRemovalsRunConcurrently(t *testing.T) {
	first, ok := catalog.Get("open-webui")
	if !ok {
		t.Fatal("open-webui missing from catalog")
	}
	second, ok := catalog.Get("searxng")
	if !ok {
		t.Fatal("searxng missing from catalog")
	}
	eng := &parallelRemovalEngine{entered: make(chan string, 2), release: make(chan struct{})}
	server := &Server{eng: eng}
	manager := jobs.NewManager()
	jobA := manager.Create("app:open-webui:uninstall")
	jobB := manager.Create("app:searxng:uninstall")
	go server.runAppUninstall(jobA, first)
	go server.runAppUninstall(jobB, second)

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case name := <-eng.entered:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatalf("only %d unrelated removals started; operations were serialized", len(seen))
		}
	}
	close(eng.release)
	deadline := time.Now().Add(time.Second)
	for (!jobA.Snapshot().Done || !jobB.Snapshot().Done) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !jobA.Snapshot().Done || !jobB.Snapshot().Done {
		t.Fatal("parallel removal jobs did not finish")
	}
}

func TestAppUninstallOutlivesRequestContext(t *testing.T) {
	eng := &parallelRemovalEngine{entered: make(chan string, 1), release: make(chan struct{})}
	close(eng.release)
	server := &Server{eng: eng, jobs: jobs.NewManager()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/apps/open-webui/uninstall", nil).WithContext(ctx)
	request.SetPathValue("id", "open-webui")
	response := httptest.NewRecorder()
	server.appUninstall(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("uninstall status = %d, want %d", response.Code, http.StatusAccepted)
	}
	select {
	case <-eng.entered:
	case <-time.After(time.Second):
		t.Fatal("background uninstall was canceled with its HTTP request")
	}
}

func TestJobListLetsInterfaceReconnectByPrefix(t *testing.T) {
	manager := jobs.NewManager()
	appJob := manager.Create("app:open-webui:install")
	appJob.ProgressOperation("pulling", "Downloading Open WebUI", "Open WebUI", 42, 0, 1)
	manager.Create("model-dl:Qwen/Test")
	server := &Server{jobs: manager}
	request := httptest.NewRequest(http.MethodGet, "/api/jobs?prefix=app:", nil)
	response := httptest.NewRecorder()
	server.jobList(response, request)
	var listed []jobs.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != appJob.ID || listed[0].Percent != 42 || listed[0].CurrentItem != "Open WebUI" {
		t.Fatalf("job list = %#v", listed)
	}
}

func TestConflictingOperationOnSameAppIsRejected(t *testing.T) {
	manager := jobs.NewManager()
	manager.Create("app:open-webui:install")
	server := &Server{jobs: manager}
	request := httptest.NewRequest(http.MethodPost, "/api/apps/open-webui/uninstall", nil)
	request.SetPathValue("id", "open-webui")
	response := httptest.NewRecorder()
	server.appUninstall(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("conflicting uninstall status = %d, want %d", response.Code, http.StatusConflict)
	}
}
