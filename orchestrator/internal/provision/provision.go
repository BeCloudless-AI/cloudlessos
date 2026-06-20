// Package provision auto-installs the apps that should ship "pre-installed" on a
// Cloudless machine (Ollama, Open WebUI, ComfyUI) on daemon startup, wires them
// onto a shared network, and optionally pulls a default chat model so
// "Chat with your Cloudless AI" works out of the box.
package provision

import (
	"context"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
)

// Network is the shared docker network for inter-app DNS.
const Network = "cloudless"

// Run provisions preinstalled apps (vLLM engine, Open WebUI, ComfyUI). It is
// best-effort and idempotent: already-running apps are skipped, failures are
// logged and do not abort the rest. Intended to run in a background goroutine.
// (vLLM downloads its model itself from --model, so there is no separate pull.)
func Run(ctx context.Context, eng engine.Engine, logf func(string)) {
	if err := eng.EnsureNetwork(ctx, Network); err != nil {
		logf("network: " + err.Error())
	} else {
		logf("network " + Network + " ready")
	}

	for _, app := range catalog.Bundled() {
		if c, _ := eng.Find(ctx, app.ContainerName()); c != nil && c.State == "running" {
			logf(app.ID + ": already running")
			if app.Network != "" { // ensure reachable by name even if started earlier
				if err := eng.ConnectNetwork(ctx, app.Network, app.ContainerName()); err != nil {
					logf(app.ID + ": network attach: " + err.Error())
				}
			}
			continue
		}
		logf(app.ID + ": pulling " + app.Image + " …")
		if err := eng.Pull(ctx, app.Image); err != nil {
			logf(app.ID + ": pull failed: " + err.Error())
			continue
		}
		if !app.Preinstall { // prefetch-only: image is ready, don't start it
			logf(app.ID + ": fetched (ready to start)")
			continue
		}
		_ = eng.Remove(ctx, app.ContainerName()) // clear any stale stopped container
		if _, err := eng.Run(ctx, app.Spec()); err != nil {
			logf(app.ID + ": start failed: " + err.Error())
			continue
		}
		logf(app.ID + ": started")
	}
	logf("done")
}
