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
	"path/filepath"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/api"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/power"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/state"
	"github.com/cloudless/orchestrator/internal/usage"
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

	us := usage.Open(filepath.Join(st.Dir(), "usage.json"))
	pw := power.Open(filepath.Join(st.Dir(), "power.json"))
	srv := api.NewServer(eng, st, mf, mfModels, mfDiff, us, pw)

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

	// Cloudless Proxy: the OpenAI-compatible, key-authenticated gateway on its own
	// port (separate from the no-auth dashboard so it can be exposed independently).
	gwAddr := envOr("CLOUDLESS_GATEWAY_ADDR", "127.0.0.1:8766")
	gatewayServer := &http.Server{Addr: gwAddr, Handler: srv.GatewayHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("cloudless proxy (API gateway) listening on http://%s", gwAddr)
		if err := gatewayServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("gateway error: %v", err)
		}
	}()

	// Flush gateway key-usage + the usage analytics store to disk periodically
	// (per-request updates are debounced in memory).
	flush := time.NewTicker(20 * time.Second)
	defer flush.Stop()
	go func() {
		for range flush.C {
			st.PersistIfDirty()
			us.Flush()
			pw.Flush()
		}
	}()

	// Sample the engine's cumulative counters into usage buckets, and the GPUs'
	// board power into the electricity log, on one ticker.
	sampler := time.NewTicker(30 * time.Second)
	defer sampler.Stop()
	go func() {
		srv.SampleUsage() // an initial sample to set the usage baseline
		srv.SamplePower() // sets the power-integration clock
		for range sampler.C {
			srv.SampleUsage()
			srv.SamplePower()
		}
	}()

	// Pre-install the bundled apps (vLLM engine, Open WebUI, ComfyUI) in the
	// background. The served model is set via CLOUDLESS_DEFAULT_MODEL (catalog).
	// Set CLOUDLESS_NO_PROVISION=1 to skip this (e.g. a second daemon on another
	// port for testing — it won't touch the primary daemon's containers).
	if os.Getenv("CLOUDLESS_NO_PROVISION") == "" {
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 8*time.Second)
		gpus, gpuErr := hardware.GPUs(probeCtx)
		probeCancel()
		if gpuErr != nil || len(gpus) == 0 {
			log.Printf("provisioning skipped: no usable NVIDIA GPU detected (%v)", gpuErr)
		} else {
			log.Printf("provisioning enabled: %d NVIDIA GPU(s) detected", len(gpus))
			go func() {
				waitingLogged := false
				for {
					engineCtx, engineCancel := context.WithTimeout(context.Background(), 3*time.Second)
					err := eng.Available(engineCtx)
					engineCancel()
					if err == nil {
						break
					}
					if !waitingLogged {
						log.Printf("provisioning waiting for Docker: %v", err)
						waitingLogged = true
					}
					time.Sleep(2 * time.Second)
				}
				log.Printf("provisioning Docker engine ready")
				provision.Run(context.Background(), eng, st, mf, func(m string) {
					log.Printf("[provision] %s", m)
				})
			}()
		}
	} else {
		log.Printf("provisioning skipped (CLOUDLESS_NO_PROVISION set)")
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	st.PersistIfDirty() // save any unflushed gateway usage
	us.Flush()
	pw.Flush()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = gatewayServer.Shutdown(ctx)
	_ = httpServer.Shutdown(ctx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
