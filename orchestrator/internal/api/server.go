// Package api exposes the orchestrator's local HTTP API and serves the web UI.
package api

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/places"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/state"
)

//go:embed all:web
var webFS embed.FS

// Server wires the container engine, job manager, and state store to HTTP handlers.
type Server struct {
	eng   engine.Engine
	jobs  *jobs.Manager
	state *state.Store
}

// NewServer constructs a Server backed by the given engine and state store.
func NewServer(eng engine.Engine, st *state.Store) *Server {
	return &Server{eng: eng, jobs: jobs.NewManager(), state: st}
}

// Routes returns the configured HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/gpu", s.gpu)
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("GET /api/apps", s.apps)
	mux.HandleFunc("POST /api/apps/{id}/start", s.start)
	mux.HandleFunc("POST /api/apps/{id}/stop", s.stop)
	mux.HandleFunc("POST /api/apps/{id}/remove", s.remove)
	mux.HandleFunc("POST /api/apps/{id}/reset", s.appReset)
	mux.HandleFunc("POST /api/apps/{id}/uninstall", s.appUninstall)
	mux.HandleFunc("GET /api/settings", s.settingsGet)
	mux.HandleFunc("POST /api/settings/model", s.settingsModel)
	mux.HandleFunc("POST /api/onboarding/reset", s.onboardingReset)
	mux.HandleFunc("GET /api/jobs/{id}", s.jobState)
	mux.HandleFunc("GET /api/jobs/{id}/events", s.jobEvents)
	mux.HandleFunc("GET /api/onboarding", s.onboardingGet)
	mux.HandleFunc("POST /api/onboarding/complete", s.onboardingComplete)
	mux.HandleFunc("GET /api/folders", s.folders)
	mux.HandleFunc("POST /api/folders/{id}/open", s.openFolder)
	mux.HandleFunc("GET /api/engine", s.engineState)
	mux.HandleFunc("POST /api/engine/{id}", s.engineSwitch)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed web assets: %v", err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	return logging(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	dockerErr := s.eng.Available(ctx)
	resp := map[string]any{"status": "ok", "dockerOK": dockerErr == nil}
	if dockerErr != nil {
		resp["dockerError"] = dockerErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) gpu(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	gpus, err := hardware.GPUs(ctx)
	resp := map[string]any{"available": len(gpus) > 0, "gpus": gpus}
	if err != nil && len(gpus) == 0 {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) folders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, places.List())
}

// activeEngine returns the id of the currently-running inference engine, or "".
func (s *Server) activeEngine(ctx context.Context) string {
	for _, e := range catalog.Engines() {
		if c, _ := s.eng.Find(ctx, e.ContainerName()); c != nil && c.State == "running" {
			return e.ID
		}
	}
	return ""
}

// engineReady reports whether the active engine is serving (model loaded).
func engineReady(ctx context.Context) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/models", catalog.EnginePort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *Server) engineState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	active := s.activeEngine(ctx)
	type eng struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}
	list := []eng{}
	for _, e := range catalog.Engines() {
		list = append(list, eng{ID: e.ID, Name: e.Name, Active: e.ID == active})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active":  active,
		"ready":   active != "" && engineReady(ctx),
		"engines": list,
	})
}

// engineSwitch stops the current engine and starts the chosen one, which inherits
// the stable `cloudless-ai` alias — so every client follows automatically.
func (s *Server) engineSwitch(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || !app.Engine {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown engine"})
		return
	}
	job := s.jobs.Create("engine:" + app.ID)
	go s.runSwitch(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "engine": app.ID})
}

func (s *Server) runSwitch(job *jobs.Job, target catalog.App) {
	// Already the active, ready engine? No-op.
	check, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	if s.activeEngine(check) == target.ID && engineReady(check) {
		ccancel()
		job.Succeed("")
		return
	}
	ccancel()
	s.applyEngine(job, target)
}

// applyEngine makes `target` the only running engine, with the current model and
// the stable alias, then waits until it's serving. Used by switch + model change.
func (s *Server) applyEngine(job *jobs.Job, target catalog.App) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	model := s.state.Get().Model
	// Serialize with the startup provisioner so neither clobbers the other (D15).
	provision.EngineMu.Lock()
	_ = s.state.SetEngine(target.ID) // remember the choice across restarts
	for _, e := range catalog.Engines() {
		if e.ID != target.ID {
			job.Progress("switching", "Stopping "+e.Name+" …", -1, -1)
			_ = s.eng.Stop(ctx, e.ContainerName())
		}
	}
	job.Progress("switching", "Starting "+target.Name+" …", -1, -1)
	_ = s.eng.Remove(ctx, target.ContainerName())
	_, runErr := s.eng.Run(ctx, catalog.EngineSpec(target, model))
	provision.EngineMu.Unlock()
	if runErr != nil {
		job.Fail(runErr)
		return
	}

	job.Progress("loading", "Loading model …", -1, -1)
	for {
		if ctx.Err() != nil {
			job.Fail(fmt.Errorf("%s did not become ready in time", target.Name))
			return
		}
		pctx, pcancel := context.WithTimeout(context.Background(), 3*time.Second)
		ready := engineReady(pctx)
		pcancel()
		if ready {
			job.Succeed("")
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func (s *Server) openFolder(w http.ResponseWriter, r *http.Request) {
	p, ok := places.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown folder"})
		return
	}
	if err := places.Open(p); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"opened": false, "path": p.Path, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true, "path": p.Path})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, catalog.All())
}

// onboardingGet reports whether first-run onboarding has been completed for this
// install/user. `firstLaunch` is true when the daemon found no prior state file.
func (s *Server) onboardingGet(w http.ResponseWriter, r *http.Request) {
	st := s.state.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"completed":   st.Onboarded,
		"firstLaunch": s.state.FirstRun(),
		"firstSeen":   st.FirstSeen,
	})
}

// onboardingComplete marks first-run onboarding done and persists it.
func (s *Server) onboardingComplete(w http.ResponseWriter, r *http.Request) {
	if err := s.state.SetOnboarded(true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"completed": true})
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	all, err := s.eng.List(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	managed := []engine.Container{}
	for _, c := range all {
		if strings.HasPrefix(c.Name, "cloudless-") {
			managed = append(managed, c)
		}
	}
	writeJSON(w, http.StatusOK, managed)
}

// start kicks off an async install job and returns its id immediately.
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	if app.Image == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": app.Name + " recipe is not available yet",
		})
		return
	}
	job := s.jobs.Create(app.ID)
	go s.runInstall(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID})
}

// runInstall pulls the image (streaming progress into the job) and runs it.
func (s *Server) runInstall(job *jobs.Job, app catalog.App) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	_ = s.eng.Remove(ctx, app.ContainerName()) // clear any stale container

	if app.Build != "" {
		// Locally-built image (no upstream): materialize the embedded context and build.
		job.Progress("building", "Building "+app.Name+" …", -1, -1)
		dir, err := apps.Materialize(app.Build)
		if err != nil {
			job.Fail(err)
			return
		}
		defer os.RemoveAll(dir)
		if err := s.eng.Build(ctx, app.Image, dir, func(l string) {
			log.Printf("[build %s] %s", app.ID, l)
		}); err != nil {
			job.Fail(err)
			return
		}
	} else {
		job.Progress("pulling", "Pulling image…", 0, 0)
		layers := map[string]bool{} // layer id -> complete
		err := s.eng.PullStream(ctx, app.Image, func(line string) {
			id, status, ok := splitStatus(line)
			switch {
			case ok && strings.HasPrefix(status, "Pulling fs layer"):
				if _, seen := layers[id]; !seen {
					layers[id] = false
				}
			case ok && (status == "Pull complete" || status == "Already exists"):
				layers[id] = true
			case strings.HasPrefix(line, "Status:"):
				job.Progress("pulling", line, -1, -1)
				return
			default:
				return
			}
			done, total := countComplete(layers)
			job.Progress("pulling", fmt.Sprintf("Downloading layers %d/%d", done, total), done, total)
		})
		if err != nil {
			job.Fail(err)
			return
		}
	}

	job.Progress("starting", "Starting container…", -1, -1)
	id, err := s.eng.Run(ctx, app.Spec())
	if err != nil {
		job.Fail(err)
		return
	}
	job.Succeed(id)
}

func (s *Server) jobState(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown job"})
		return
	}
	writeJSON(w, http.StatusOK, job.Snapshot())
}

// jobEvents streams job updates as Server-Sent Events until the job is done.
func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := job.Subscribe()
	defer job.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case u, open := <-ch:
			if !open {
				return
			}
			b, _ := json.Marshal(u)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			if u.Done {
				return
			}
		}
	}
}

// splitStatus parses a "<id>: <status>" docker pull line.
func splitStatus(line string) (id, status string, ok bool) {
	i := strings.Index(line, ": ")
	if i <= 0 {
		return "", "", false
	}
	return line[:i], line[i+2:], true
}

func countComplete(m map[string]bool) (done, total int) {
	total = len(m)
	for _, complete := range m {
		if complete {
			done++
		}
	}
	return done, total
}

// appReset wipes an app to a clean state: remove its container (and image, so a
// build app rebuilds the recipe), then reinstall. Runs as an async job.
func (s *Server) appReset(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	job := s.jobs.Create("reset:" + app.ID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_ = s.eng.Remove(ctx, app.ContainerName())
		if app.Build != "" { // force a fresh rebuild of locally-built apps
			_ = s.eng.RemoveImage(ctx, app.Image)
		}
		s.runInstall(job, app)
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID})
}

// appUninstall removes an app's container and image.
func (s *Server) appUninstall(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	_ = s.eng.Remove(ctx, app.ContainerName())
	_ = s.eng.RemoveImage(ctx, app.Image)
	writeJSON(w, http.StatusOK, map[string]string{"status": "uninstalled"})
}

func (s *Server) settingsGet(w http.ResponseWriter, r *http.Request) {
	st := s.state.Get()
	model := st.Model
	if model == "" {
		model = catalog.DefaultModel()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model":        model,
		"defaultModel": catalog.DefaultModel(),
	})
}

// settingsModel sets the served model and restarts the active engine to apply it.
func (s *Server) settingsModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == catalog.DefaultModel() {
		model = "" // store empty to mean "default"
	}
	_ = s.state.SetModel(model)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	active := s.activeEngine(ctx)
	cancel()
	if active == "" {
		active = catalog.DefaultEngine()
	}
	app, _ := catalog.Get(active)
	job := s.jobs.Create("model:" + app.ID)
	go s.applyEngine(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func (s *Server) onboardingReset(w http.ResponseWriter, r *http.Request) {
	if err := s.state.SetOnboarded(false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.eng.Stop(ctx, app.ContainerName()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.eng.Remove(ctx, app.ContainerName()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}
