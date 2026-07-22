package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
)

// shortDigest trims "sha256:" and shortens for display.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// updateGet reports whether a newer image is available for an app, by comparing
// the locally-pulled digest to the digest the registry currently serves. The
// catalog's image ref is the validated "manifest" entry for now (D30).
func (s *Server) updateGet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || app.Service {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()

	// Locally-built apps (currently OpenClaw) have no upstream digest to compare;
	// "update" for them means rebuilding (the Reset action).
	if app.Build != "" {
		c, _ := s.eng.Find(ctx, app.ContainerName())
		writeJSON(w, http.StatusOK, map[string]any{"installed": c != nil, "updatable": false, "build": true})
		return
	}

	// What's installed = the digest the container is actually running (falls back to
	// the pulled tag image if it's installed-but-stopped).
	installed, _ := s.eng.ContainerImageDigest(ctx, app.ContainerName())
	if installed == "" {
		installed, _ = s.eng.ImageDigest(ctx, app.Image)
	}
	if installed == "" {
		writeJSON(w, http.StatusOK, map[string]any{"installed": false}) // not pulled → "Install", not "Update"
		return
	}

	// Prefer the Cloudless validated manifest's pinned digest; else compare to the live upstream tag.
	if pin, ok := s.manifest.Pin(ctx, app.ID); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"installed": true, "updatable": true, "source": "cloudless",
			"channel": s.manifest.Channel(ctx), "verified": pin.Verified, "notes": pin.Notes,
			"hasUpdate": installed != pin.Digest,
			"current":   shortDigest(installed), "latest": shortDigest(pin.Digest),
		})
		return
	}
	remote, err := s.eng.RemoteDigest(ctx, app.Image)
	if err != nil || remote == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"installed": true, "updatable": true, "source": "upstream", "hasUpdate": false,
			"current": shortDigest(installed), "checkError": "couldn't reach the registry — are you online?",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": true, "updatable": true, "source": "upstream", "hasUpdate": installed != remote,
		"current": shortDigest(installed), "latest": shortDigest(remote),
	})
}

// updateApply pulls the latest image (or rebuilds) and recreates the container with
// the SAME spec — same name, env, ports and volumes — so config/data are preserved.
// This is exactly runInstall (which removes the old container, pulls/builds, then runs
// app.Spec() + mounted config), so an update is non-destructive (D30).
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || app.Service {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	if app.Image == "" && app.Build == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": app.Name + " is not installable"})
		return
	}
	job := s.jobs.Create("update:" + app.ID)
	go s.runInstall(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}
