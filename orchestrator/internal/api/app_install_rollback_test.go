package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/state"
)

type appInstallRollbackEngine struct {
	engine.Engine
	containers    map[string]*engine.Container
	removed       []string
	removedImages []string
	failRun       string
}

type appUninstallCleanupEngine struct {
	engine.Engine
	containers map[string]*engine.Container
	removed    []string
	images     []string
}

func (e *appUninstallCleanupEngine) Find(_ context.Context, name string) (*engine.Container, error) {
	return e.containers[name], nil
}

func (e *appUninstallCleanupEngine) Remove(_ context.Context, name string) error {
	e.removed = append(e.removed, name)
	delete(e.containers, name)
	return nil
}

func (e *appUninstallCleanupEngine) RemoveImage(_ context.Context, image string) error {
	e.images = append(e.images, image)
	return nil
}

func (e *appInstallRollbackEngine) Find(_ context.Context, name string) (*engine.Container, error) {
	if container := e.containers[name]; container != nil {
		copy := *container
		return &copy, nil
	}
	return nil, nil
}

func (e *appInstallRollbackEngine) PullStream(_ context.Context, _ string, onLine func(string)) error {
	onLine("Status: Image is up to date")
	return nil
}

func (e *appInstallRollbackEngine) Run(_ context.Context, spec engine.RunSpec) (string, error) {
	if spec.Name == e.failRun {
		return "", errors.New("injected target failure")
	}
	e.containers[spec.Name] = &engine.Container{Name: spec.Name, Image: spec.Image, State: "running"}
	return "container-" + spec.Name, nil
}

func (e *appInstallRollbackEngine) Remove(_ context.Context, name string) error {
	e.removed = append(e.removed, name)
	delete(e.containers, name)
	return nil
}

func (e *appInstallRollbackEngine) RemoveImage(_ context.Context, image string) error {
	e.removedImages = append(e.removedImages, image)
	return nil
}

func waitForAppJob(t *testing.T, job *jobs.Job) jobs.Update {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := job.Snapshot()
		if snapshot.Done {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("app operation did not finish")
	return jobs.Update{}
}

func TestFailedAppInstallRollsBackOnlyNewDependencies(t *testing.T) {
	app, ok := catalog.Get("perplexica")
	if !ok {
		t.Fatal("perplexica missing from catalog")
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng := &appInstallRollbackEngine{
		containers: map[string]*engine.Container{},
		failRun:    app.ContainerName(),
	}
	server := &Server{eng: eng, state: store, appHealthCheck: func(context.Context, catalog.App) error { return nil }}
	job := jobs.NewManager().Create("app:perplexica:install")

	server.runInstall(job, app)
	snapshot := waitForAppJob(t, job)
	if snapshot.Error == "" {
		t.Fatal("target install unexpectedly succeeded")
	}
	if !slices.Contains(eng.removed, "cloudless-searxng") {
		t.Fatalf("new dependency was not rolled back: removed=%v", eng.removed)
	}
	searxng, _ := catalog.Get("searxng")
	if !slices.Contains(eng.removedImages, searxng.Image) {
		t.Fatalf("new dependency image was not rolled back: removed=%v", eng.removedImages)
	}
}

func TestFailedAppInstallPreservesPreexistingDependency(t *testing.T) {
	app, _ := catalog.Get("perplexica")
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng := &appInstallRollbackEngine{
		containers: map[string]*engine.Container{
			"cloudless-searxng": {Name: "cloudless-searxng", State: "running"},
		},
		failRun: app.ContainerName(),
	}
	server := &Server{eng: eng, state: store, appHealthCheck: func(context.Context, catalog.App) error { return nil }}
	job := jobs.NewManager().Create("app:perplexica:install")

	server.runInstall(job, app)
	_ = waitForAppJob(t, job)
	if slices.Contains(eng.removed, "cloudless-searxng") {
		t.Fatalf("pre-existing dependency was removed: %v", eng.removed)
	}
}

func TestAppUninstallRemovesSidecarsAndPreservesPersistentData(t *testing.T) {
	app, ok := catalog.Get("perplexica")
	if !ok {
		t.Fatal("perplexica missing from catalog")
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	marker := store.Dir() + "/apps/perplexica/user-data"
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := &appUninstallCleanupEngine{containers: map[string]*engine.Container{
		app.ContainerName(): {Name: app.ContainerName(), State: "running"},
		app.LanName():       {Name: app.LanName(), State: "running"},
		app.TunnelName():    {Name: app.TunnelName(), State: "running"},
	}}
	server := &Server{eng: eng, state: store}
	job := jobs.NewManager().Create("app:perplexica:uninstall")

	server.runAppUninstall(job, app)
	snapshot := waitForAppJob(t, job)
	if snapshot.Error != "" {
		t.Fatalf("uninstall failed: %s", snapshot.Error)
	}
	for _, name := range []string{app.ContainerName(), app.LanName(), app.TunnelName()} {
		if !slices.Contains(eng.removed, name) {
			t.Errorf("%s was left behind: %v", name, eng.removed)
		}
	}
	if !slices.Contains(eng.images, app.Image) {
		t.Errorf("downloaded image was left behind: %v", eng.images)
	}
	if payload, err := os.ReadFile(marker); err != nil || string(payload) != "keep" {
		t.Fatalf("persistent data was deleted: payload=%q err=%v", payload, err)
	}
}
