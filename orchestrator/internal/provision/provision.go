// Package provision auto-installs the apps that should ship "pre-installed" on a
// Cloudless machine (Ollama, Open WebUI, ComfyUI) on daemon startup, wires them
// onto a shared network, and optionally pulls a default chat model so
// "Chat with your Cloudless AI" works out of the box.
package provision

import (
	"context"
	"sync"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/state"
)

// Network is the shared docker network for inter-app DNS.
const Network = "cloudless"

// EngineMu serializes engine start/stop between the startup provisioner and the
// engine-switch API so they can't clobber each other (D15).
var EngineMu sync.Mutex

// Run provisions the bundled apps. Both engine images are pulled (so either is
// ready), but only the selected engine runs (others are stopped) — exactly one
// engine holds the stable alias at a time. Best-effort and idempotent; intended
// for a background goroutine.
func Run(ctx context.Context, eng engine.Engine, st *state.Store, logf func(string)) {
	if err := eng.EnsureNetwork(ctx, Network); err != nil {
		logf("network: " + err.Error())
	} else {
		logf("network " + Network + " ready")
	}

	// Engines: pull all images (either is ready); the slow pulls need no lock.
	for _, e := range catalog.Engines() {
		logf(e.ID + ": pulling " + e.Image + " …")
		if err := eng.Pull(ctx, e.Image); err != nil {
			logf(e.ID + ": pull failed: " + err.Error())
		}
	}

	// Start/stop under the lock and re-read the desired engine inside it, so a
	// concurrent switch (which sets it + holds the same lock) isn't clobbered.
	EngineMu.Lock()
	desired := st.Get().Engine
	if desired == "" {
		desired = catalog.DefaultEngine()
	}
	model := st.Get().Model
	// Stop any non-selected engine first, so the shared port/alias is free.
	for _, e := range catalog.Engines() {
		if e.ID == desired {
			continue
		}
		if c, _ := eng.Find(ctx, e.ContainerName()); c != nil && c.State == "running" {
			logf(e.ID + ": stopping (not the active engine)")
			_ = eng.Stop(ctx, e.ContainerName())
		} else {
			logf(e.ID + ": ready (inactive)")
		}
	}
	// Ensure the selected engine is running with the stable alias.
	for _, e := range catalog.Engines() {
		if e.ID != desired {
			continue
		}
		running, hasAlias := false, false
		if c, _ := eng.Find(ctx, e.ContainerName()); c != nil && c.State == "running" {
			running = true
			hasAlias, _ = eng.HasAlias(ctx, e.ContainerName(), catalog.EngineAlias)
		}
		if running && hasAlias {
			logf(e.ID + ": active engine running")
		} else {
			_ = eng.Remove(ctx, e.ContainerName())
			if _, err := eng.Run(ctx, catalog.EngineSpec(e, model)); err != nil {
				logf(e.ID + ": start failed: " + err.Error())
			} else {
				logf(e.ID + ": started (active engine)")
			}
		}
	}
	EngineMu.Unlock()

	// Non-engine bundled apps (Open WebUI, ComfyUI): pull and run.
	for _, app := range catalog.Bundled() {
		if app.Engine {
			continue
		}
		if c, _ := eng.Find(ctx, app.ContainerName()); c != nil && c.State == "running" {
			logf(app.ID + ": already running")
			continue
		}
		logf(app.ID + ": pulling " + app.Image + " …")
		if err := eng.Pull(ctx, app.Image); err != nil {
			logf(app.ID + ": pull failed: " + err.Error())
			continue
		}
		if !app.Preinstall { // prefetch-only: image ready, don't start
			logf(app.ID + ": fetched (ready to start)")
			continue
		}
		_ = eng.Remove(ctx, app.ContainerName())
		if _, err := eng.Run(ctx, app.Spec()); err != nil {
			logf(app.ID + ": start failed: " + err.Error())
			continue
		}
		logf(app.ID + ": started")
	}
	logf("done")
}
