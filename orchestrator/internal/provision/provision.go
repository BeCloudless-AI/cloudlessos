// Package provision auto-installs the apps that should ship "pre-installed" on a
// Cloudless machine (Ollama, Open WebUI, ComfyUI) on daemon startup, wires them
// onto a shared network, and optionally pulls a default chat model so
// "Chat with your Cloudless AI" works out of the box.
package provision

import (
	"context"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
)

// Network is the shared docker network for inter-app DNS.
const Network = "cloudless"

// Run provisions preinstalled apps. It is best-effort and idempotent: already
// running apps are skipped, failures are logged and do not abort the rest.
// Intended to run in a background goroutine. defaultModel "" skips the model pull.
func Run(ctx context.Context, eng engine.Engine, defaultModel string, logf func(string)) {
	if err := eng.EnsureNetwork(ctx, Network); err != nil {
		logf("network: " + err.Error())
	} else {
		logf("network " + Network + " ready")
	}

	ollamaReady := false
	for _, app := range catalog.Preinstalled() {
		if c, _ := eng.Find(ctx, app.ContainerName()); c != nil && c.State == "running" {
			logf(app.ID + ": already running")
			if app.Network != "" { // ensure it's reachable by name even if started earlier
				if err := eng.ConnectNetwork(ctx, app.Network, app.ContainerName()); err != nil {
					logf(app.ID + ": network attach: " + err.Error())
				}
			}
			if app.ID == "ollama" {
				ollamaReady = true
			}
			continue
		}
		logf(app.ID + ": pulling " + app.Image + " …")
		if err := eng.Pull(ctx, app.Image); err != nil {
			logf(app.ID + ": pull failed: " + err.Error())
			continue
		}
		_ = eng.Remove(ctx, app.ContainerName()) // clear any stale stopped container
		if _, err := eng.Run(ctx, app.Spec()); err != nil {
			logf(app.ID + ": start failed: " + err.Error())
			continue
		}
		logf(app.ID + ": started")
		if app.ID == "ollama" {
			ollamaReady = true
		}
	}

	if ollamaReady && defaultModel != "" {
		logf("model: pulling " + defaultModel + " (first boot may take a while) …")
		time.Sleep(3 * time.Second) // let the ollama server finish coming up
		if err := eng.Exec(ctx, "cloudless-ollama", "ollama", "pull", defaultModel); err != nil {
			logf("model: pull failed: " + err.Error())
		} else {
			logf("model: " + defaultModel + " ready")
		}
	}
	logf("done")
}
