// Package catalog defines the curated set of installable AI apps (container
// recipes). Curation — not "install anything" — is the reliability bet (see
// docs/ARCHITECTURE.md, Layer 3).
package catalog

import "github.com/cloudless/orchestrator/internal/engine"

// App is a curated, installable AI application backed by a container image.
type App struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Image       string            `json:"image"` // empty = recipe not yet available
	Ports       map[int]int       `json:"ports"` // hostPort -> containerPort
	Env         map[string]string `json:"env,omitempty"`
	GPUs        string            `json:"gpus"`      // "all", "0", ... or "" for none
	OpenPath    string            `json:"openPath"`  // URL path to open once running
	MinVRAMGB   int               `json:"minVramGB"` // rough VRAM floor for usefulness
	Verified    bool              `json:"verified"`  // recipe validated on Cloudless dev hardware
}

// ContainerName is the orchestrator-managed container name for this app.
func (a App) ContainerName() string { return "cloudless-" + a.ID }

// PrimaryHostPort returns a host port to build the "open" URL from (0 if none).
func (a App) PrimaryHostPort() int {
	for host := range a.Ports {
		return host
	}
	return 0
}

// Spec converts a catalog app into an engine.RunSpec.
func (a App) Spec() engine.RunSpec {
	return engine.RunSpec{
		Name:  a.ContainerName(),
		Image: a.Image,
		Ports: a.Ports,
		Env:   a.Env,
		GPUs:  a.GPUs,
	}
}

// apps is the Phase 0 starter catalog. Images marked Verified=false still need a
// validated recipe before we promise they "just work".
var apps = []App{
	{
		ID:          "ollama",
		Name:        "Ollama",
		Description: "Run local LLMs (Llama, Mistral, Qwen, …) behind a simple API.",
		Image:       "ollama/ollama",
		Ports:       map[int]int{11434: 11434},
		GPUs:        "all",
		OpenPath:    "/",
		MinVRAMGB:   4,
		Verified:    true,
	},
	{
		ID:          "open-webui",
		Name:        "Open WebUI",
		Description: "A friendly chat interface for local LLMs (pairs with Ollama).",
		Image:       "ghcr.io/open-webui/open-webui:main",
		Ports:       map[int]int{3000: 8080},
		GPUs:        "",
		OpenPath:    "/",
		MinVRAMGB:   0,
		Verified:    false,
	},
	{
		ID:          "comfyui",
		Name:        "ComfyUI",
		Description: "Node-based image/video generation (Stable Diffusion, Flux, …).",
		Image:       "", // TODO: pin a validated ComfyUI image recipe
		Ports:       map[int]int{8188: 8188},
		GPUs:        "all",
		OpenPath:    "/",
		MinVRAMGB:   6,
		Verified:    false,
	},
}

// All returns the full catalog.
func All() []App { return apps }

// Get returns the app with the given id.
func Get(id string) (App, bool) {
	for _, a := range apps {
		if a.ID == id {
			return a, true
		}
	}
	return App{}, false
}
