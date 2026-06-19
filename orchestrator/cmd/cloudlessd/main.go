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

	srv := api.NewServer(eng, st)

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
