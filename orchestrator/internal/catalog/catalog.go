// Package catalog defines the curated set of installable AI apps (container
// recipes). Curation — not "install anything" — is the reliability bet (see
// docs/ARCHITECTURE.md, Layer 3).
package catalog

import (
	"os"

	"github.com/cloudless/orchestrator/internal/engine"
)

// defaultLLM is the model vLLM serves as "Cloudless AI". Override with
// CLOUDLESS_DEFAULT_MODEL (any Hugging Face model id vLLM supports).
var defaultLLM = envOr("CLOUDLESS_DEFAULT_MODEL", "Qwen/Qwen2.5-1.5B-Instruct")

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// App is a curated, installable AI application backed by a container image.
type App struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Image       string            `json:"image"` // empty = recipe not yet available
	Ports       map[int]int       `json:"ports"` // hostPort -> containerPort
	Env         map[string]string `json:"env,omitempty"`
	GPUs        string            `json:"gpus"`       // "all", "0", ... or "" for none
	OpenPath    string            `json:"openPath"`   // URL path to open once running
	MinVRAMGB   int               `json:"minVramGB"`  // rough VRAM floor for usefulness
	Verified    bool              `json:"verified"`   // recipe validated on Cloudless dev hardware
	Preinstall  bool              `json:"preinstall"` // provisioned automatically on first boot
	Service     bool              `json:"service"`    // infrastructure (engine), hidden from the launcher
	Network     string            `json:"-"`          // docker network to join (for inter-app DNS)
	Command     []string          `json:"-"`          // container command/args
	Volumes     map[string]string `json:"-"`          // host-or-named-volume -> containerPath
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
		Name:    a.ContainerName(),
		Image:   a.Image,
		Ports:   a.Ports,
		Env:     a.Env,
		GPUs:    a.GPUs,
		Network: a.Network,
		Args:    a.Command,
		Volumes: a.Volumes,
	}
}

// Preinstalled returns the apps that should be provisioned on first boot.
func Preinstalled() []App {
	var out []App
	for _, a := range apps {
		if a.Preinstall && a.Image != "" {
			out = append(out, a)
		}
	}
	return out
}

// apps is the Phase 0 starter catalog. Images marked Verified=false still need a
// validated recipe before we promise they "just work".
// cloudlessNet is the shared docker network so apps can reach each other by
// container name (e.g. Open WebUI -> cloudless-ollama:11434).
const cloudlessNet = "cloudless"

var apps = []App{
	{
		// vLLM is the default inference engine powering "Cloudless AI" (D12).
		// OpenAI-compatible server; Open WebUI talks to it over the OpenAI API.
		ID:          "vllm",
		Name:        "vLLM Engine",
		Description: "High-throughput inference engine powering Cloudless AI.",
		Image:       "vllm/vllm-openai:latest",
		Ports:       map[int]int{8000: 8000},
		// Image entrypoint is `vllm serve`; the model is the positional arg.
		Command: []string{
			defaultLLM,
			"--served-model-name", "cloudless",
			"--gpu-memory-utilization", "0.5",
			"--max-model-len", "8192",
		},
		Volumes:    map[string]string{"cloudless-hf": "/root/.cache/huggingface"}, // persist model cache
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  6,
		Verified:   false, // pending Blackwell (RTX 50xx / sm_120) validation — see D12
		Preinstall: true,
		Service:    true,
		Network:    cloudlessNet,
	},
	{
		ID:          "open-webui",
		Name:        "Open WebUI",
		Description: "Chat with your local LLMs — the face of Cloudless AI.",
		Image:       "ghcr.io/open-webui/open-webui:main",
		Ports:       map[int]int{3000: 8080},
		Env: map[string]string{
			"WEBUI_NAME":          "Cloudless AI",
			"WEBUI_AUTH":          "False", // local appliance: no login wall
			"ENABLE_OLLAMA_API":   "False",
			"OPENAI_API_BASE_URL": "http://cloudless-vllm:8000/v1",
			"OPENAI_API_KEY":      "cloudless", // vLLM ignores it unless --api-key is set
		},
		GPUs:       "",
		OpenPath:   "/",
		MinVRAMGB:  0,
		Verified:   true,
		Preinstall: true,
		Network:    cloudlessNet,
	},
	{
		ID:          "comfyui",
		Name:        "ComfyUI",
		Description: "Node-based image/video generation (Stable Diffusion, Flux, …).",
		// Community image; NOT yet validated on Blackwell (RTX 50xx) — see DECISIONS D11.
		Image:      "mmartial/comfyui-nvidia-docker:latest",
		Ports:      map[int]int{8188: 8188},
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  6,
		Verified:   false,
		Preinstall: true,
		Network:    cloudlessNet,
	},
	{
		// Kept as an optional alternative engine, not the default (see D12).
		ID:          "ollama",
		Name:        "Ollama",
		Description: "Alternative LLM runtime (llama.cpp-based). Optional; not the default engine.",
		Image:       "ollama/ollama",
		Ports:       map[int]int{11434: 11434},
		GPUs:        "all",
		OpenPath:    "/",
		MinVRAMGB:   4,
		Verified:    true,
		Preinstall:  false,
		Service:     true,
		Network:     cloudlessNet,
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
