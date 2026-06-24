// Command cloudlessd is the Cloudless orchestrator daemon: a local service that
// installs, runs, and manages local-AI apps as GPU containers, and serves the
// web UI that drives it.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/api"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/state"
)

func main() {
	addr := envOr("CLOUDLESS_ADDR", "127.0.0.1:8765")

	eng := engine.NewDocker()

	st, err := state.Open(state.DefaultDir())
	if err != nil {
		log.Printf("state store unavailable (%v); onboarding will not persist", err)
	}
	log.Printf("state: %s (first launch: %v, onboarded: %v)", st.Path(), st.FirstRun(), st.Get().Onboarded)

	mf := manifest.New(envOr("CLOUDLESS_MANIFEST_URL", manifest.DefaultURL))
	mfModels := manifest.NewModels(envOr("CLOUDLESS_MODELS_URL", manifest.DefaultModelsURL))
	mfDiff := manifest.NewDiffusion(envOr("CLOUDLESS_DIFFUSION_URL", manifest.DefaultDiffusionURL))
	log.Printf("manifest: %s", envOr("CLOUDLESS_MANIFEST_URL", manifest.DefaultURL))
	log.Printf("models manifest: %s", envOr("CLOUDLESS_MODELS_URL", manifest.DefaultModelsURL))

	srv := api.NewServer(eng, st, mf, mfModels, mfDiff)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("cloudlessd listening on http://%s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Pre-install the bundled apps (vLLM engine, Open WebUI, ComfyUI) in the
	// background. The served model is set via CLOUDLESS_DEFAULT_MODEL (catalog).
	// Set CLOUDLESS_NO_PROVISION=1 to skip this (e.g. a second daemon on another
	// port for testing — it won't touch the primary daemon's containers).
	if os.Getenv("CLOUDLESS_NO_PROVISION") == "" {
		go provision.Run(context.Background(), eng, st, mf, func(m string) {
			log.Printf("[provision] %s", m)
		})
	} else {
		log.Printf("provisioning skipped (CLOUDLESS_NO_PROVISION set)")
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
