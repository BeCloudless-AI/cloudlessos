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
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
)

//go:embed all:web
var webFS embed.FS

// Server wires the container engine and job manager to HTTP handlers.
type Server struct {
	eng  engine.Engine
	jobs *jobs.Manager
}

// NewServer constructs a Server backed by the given engine.
func NewServer(eng engine.Engine) *Server {
	return &Server{eng: eng, jobs: jobs.NewManager()}
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
	mux.HandleFunc("GET /api/jobs/{id}", s.jobState)
	mux.HandleFunc("GET /api/jobs/{id}/events", s.jobEvents)

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

	info, err := s.eng.GPUInfo(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "raw": info})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, catalog.All())
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
