package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/state"
)

type lifecycleMatrixEngine struct {
	engine.Engine
	containers map[string]*engine.Container
	images     map[string]bool
}

func (e *lifecycleMatrixEngine) PullStream(_ context.Context, image string, onLine func(string)) error {
	e.images[image] = true
	onLine("Status: Downloaded newer image")
	return nil
}

func (e *lifecycleMatrixEngine) Build(_ context.Context, image, _ string, onLine func(string)) error {
	e.images[image] = true
	onLine("build complete")
	return nil
}

func (e *lifecycleMatrixEngine) Run(_ context.Context, spec engine.RunSpec) (string, error) {
	e.containers[spec.Name] = &engine.Container{
		ID: "id-" + spec.Name, Name: spec.Name, Image: spec.Image, State: "running", Status: "Up",
	}
	return "id-" + spec.Name, nil
}

func (e *lifecycleMatrixEngine) Find(_ context.Context, name string) (*engine.Container, error) {
	container := e.containers[name]
	if container == nil {
		return nil, nil
	}
	copy := *container
	return &copy, nil
}

func (e *lifecycleMatrixEngine) Remove(_ context.Context, name string) error {
	delete(e.containers, name)
	return nil
}

func (e *lifecycleMatrixEngine) RemoveImage(_ context.Context, image string) error {
	delete(e.images, image)
	return nil
}

func TestPublishedApplicationLifecycleMatrix(t *testing.T) {
	for _, app := range catalog.All() {
		if app.Engine {
			continue
		}
		app := app
		t.Run(app.ID, func(t *testing.T) {
			store, err := state.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			eng := &lifecycleMatrixEngine{
				containers: map[string]*engine.Container{},
				images:     map[string]bool{},
			}
			manager := jobs.NewManager()
			configured := map[string]int{}
			server := &Server{
				eng: eng, state: store, jobs: manager,
				appHealthCheck: func(_ context.Context, checked catalog.App) error {
					if checked.Health.Kind == "" {
						return fmt.Errorf("%s has no health contract", checked.ID)
					}
					return nil
				},
				appConfigure: func(_ context.Context, _ *jobs.Job, configuredApp catalog.App) error {
					configured[configuredApp.ID]++
					return nil
				},
			}

			install := manager.Create("app:" + app.ID + ":install")
			server.runInstall(install, app)
			if snapshot := waitForAppJob(t, install); snapshot.Error != "" {
				t.Fatalf("install failed: %s", snapshot.Error)
			}
			if container := eng.containers[app.ContainerName()]; container == nil || container.State != "running" {
				t.Fatalf("install did not launch %s: %#v", app.ContainerName(), container)
			}
			if configured[app.ID] != 1 {
				t.Fatalf("post-install integration count = %d, want 1", configured[app.ID])
			}
			if app.Launchable() {
				launchURL := fmt.Sprintf("http://127.0.0.1:%d%s", app.PrimaryHostPort(), app.OpenPath)
				if app.PrimaryHostPort() == 0 || launchURL == "http://127.0.0.1:0" {
					t.Fatalf("invalid launch URL %q", launchURL)
				}
			}

			update := manager.Create("app:" + app.ID + ":update")
			server.runInstall(update, app)
			if snapshot := waitForAppJob(t, update); snapshot.Error != "" {
				t.Fatalf("update failed: %s", snapshot.Error)
			}
			if configured[app.ID] != 2 {
				t.Fatalf("update did not reapply integration: count=%d", configured[app.ID])
			}

			response := httptest.NewRecorder()
			server.restartApp(response, app)
			if response.Code != http.StatusAccepted {
				t.Fatalf("restart status=%d body=%s", response.Code, response.Body.String())
			}
			var restarted map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &restarted); err != nil {
				t.Fatal(err)
			}
			restart, ok := manager.Get(restarted["jobId"])
			if !ok {
				t.Fatal("restart job missing")
			}
			if snapshot := waitForAppJob(t, restart); snapshot.Error != "" {
				t.Fatalf("restart failed: %s", snapshot.Error)
			}

			marker := filepath.Join(store.Dir(), "apps", app.ID, "lifecycle-user-data")
			if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(marker, []byte("persistent"), 0o600); err != nil {
				t.Fatal(err)
			}
			eng.containers[app.LanName()] = &engine.Container{Name: app.LanName(), State: "running"}
			eng.containers[app.TunnelName()] = &engine.Container{Name: app.TunnelName(), State: "running"}
			uninstall := manager.Create("app:" + app.ID + ":uninstall")
			server.runAppUninstall(uninstall, app)
			if snapshot := waitForAppJob(t, uninstall); snapshot.Error != "" {
				t.Fatalf("uninstall failed: %s", snapshot.Error)
			}
			for _, name := range []string{app.ContainerName(), app.LanName(), app.TunnelName()} {
				if eng.containers[name] != nil {
					t.Errorf("uninstall left runtime %s", name)
				}
			}
			if payload, err := os.ReadFile(marker); err != nil || string(payload) != "persistent" {
				t.Fatalf("uninstall destroyed persistent state: payload=%q err=%v", payload, err)
			}
		})
	}
}
