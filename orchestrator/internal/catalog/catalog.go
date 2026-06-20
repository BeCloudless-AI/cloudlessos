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
	Preinstall  bool              `json:"preinstall"` // pulled AND run automatically on first boot
	Prefetch    bool              `json:"-"`          // image pulled on boot but not run (ready alternative)
	Service     bool              `json:"service"`    // infrastructure (engine), hidden from the launcher
	Engine      bool              `json:"engine"`     // switchable inference engine (carries the stable alias)
	Network     string            `json:"-"`          // docker network to join (for inter-app DNS)
	Command     []string          `json:"-"`          // container command/args
	Volumes     map[string]string `json:"-"`          // host-or-named-volume -> containerPath
	Build       string            `json:"-"`          // embedded build-context name (build instead of pull)
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
	rs := engine.RunSpec{
		Name:    a.ContainerName(),
		Image:   a.Image,
		Ports:   a.Ports,
		Env:     a.Env,
		GPUs:    a.GPUs,
		Network: a.Network,
		Args:    a.Command,
		Volumes: a.Volumes,
	}
	// Engines carry the stable alias so clients reach whichever one is active.
	if a.Engine {
		rs.NetworkAlias = EngineAlias
	}
	return rs
}

// Engines returns the switchable inference engines.
func Engines() []App {
	var out []App
	for _, a := range apps {
		if a.Engine {
			out = append(out, a)
		}
	}
	return out
}

// DefaultModel returns the built-in default model (also the substitution sentinel).
func DefaultModel() string { return defaultLLM }

// EngineSpec builds an engine's run spec, substituting the chosen model for the
// default. model "" (or the default) leaves the catalog command unchanged.
func EngineSpec(a App, model string) engine.RunSpec {
	rs := a.Spec()
	if model == "" || model == defaultLLM {
		return rs
	}
	args := make([]string, len(rs.Args)) // copy: don't mutate the shared catalog slice
	copy(args, rs.Args)
	for i := range args {
		if args[i] == defaultLLM {
			args[i] = model
		}
	}
	rs.Args = args
	return rs
}

// DefaultEngine returns the id of the default engine (the preinstalled one).
func DefaultEngine() string {
	for _, a := range apps {
		if a.Engine && a.Preinstall {
			return a.ID
		}
	}
	for _, a := range apps {
		if a.Engine {
			return a.ID
		}
	}
	return ""
}

// Bundled returns apps whose images should be present on boot: Preinstall apps
// are also started; Prefetch apps are pulled only (ready to start on demand).
func Bundled() []App {
	var out []App
	for _, a := range apps {
		if (a.Preinstall || a.Prefetch) && a.Image != "" {
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

// EngineAlias is the stable DNS name all clients use for the active inference
// engine; switching engines just moves this alias (see D15). EnginePort is the
// fixed port every engine listens on (so the endpoint never changes).
const (
	EngineAlias = "cloudless-ai"
	EnginePort  = 8000
)

// EngineEndpoint is the stable OpenAI base URL clients are configured with.
const EngineEndpoint = "http://cloudless-ai:8000/v1"

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
		// Tool calling enabled (agents like OpenClaw send tools); Qwen2.5 -> hermes parser.
		Command: []string{
			defaultLLM,
			"--served-model-name", "cloudless",
			"--gpu-memory-utilization", "0.5",
			"--max-model-len", "32768", // Qwen2.5 native context; agents send big prompts
			"--enable-auto-tool-choice",
			"--tool-call-parser", "hermes",
		},
		Volumes:    map[string]string{"cloudless-hf": "/root/.cache/huggingface"}, // persist model cache
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  6,
		Verified:   false, // pending Blackwell (RTX 50xx / sm_120) validation — see D12
		Preinstall: true,
		Service:    true,
		Engine:     true,
		Network:    cloudlessNet,
	},
	{
		// Alternative engine (D13): pre-fetched (image ready) but not run by
		// default — switch to it instead of vLLM. RadixAttention; OpenAI-compatible.
		// Entrypoint is the NVIDIA wrapper, so the launch command is passed as args.
		ID:          "sglang",
		Name:        "SGLang Engine",
		Description: "Alternative high-performance inference engine (OpenAI-compatible).",
		Image:       "lmsysorg/sglang:latest",
		Ports:       map[int]int{8000: 8000}, // same fixed port as vLLM (one engine runs at a time)
		Command: []string{
			"python3", "-m", "sglang.launch_server",
			"--model-path", defaultLLM,
			"--served-model-name", "cloudless",
			"--host", "0.0.0.0", "--port", "8000",
			"--mem-fraction-static", "0.5",
			"--context-length", "32768", // match vLLM; agents send big prompts
			"--tool-call-parser", "qwen25", // agents need tool calling
		},
		Volumes:   map[string]string{"cloudless-hf": "/root/.cache/huggingface"},
		GPUs:      "all",
		OpenPath:  "/",
		MinVRAMGB: 6,
		Verified:  false,
		Prefetch:  true,
		Service:   true,
		Engine:    true,
		Network:   cloudlessNet,
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
			"OPENAI_API_BASE_URL": EngineEndpoint, // stable alias -> active engine (D15)
			"OPENAI_API_KEY":      "cloudless",    // engines ignore it unless --api-key is set
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
		// Agent (D13/D14). No upstream image; built locally from an embedded
		// Dockerfile (internal/apps/openclaw) that npm-installs OpenClaw and bakes
		// in the Cloudless AI provider config.
		ID:          "openclaw",
		Name:        "OpenClaw",
		Description: "Open-source personal AI agent that takes actions on your machine. Pre-wired to Cloudless AI.",
		Image:       "cloudless/openclaw:local",
		Build:       "openclaw",
		Ports:       map[int]int{18789: 18789}, // for the UI link; host networking binds it directly
		Env:         map[string]string{"CUSTOM_API_KEY": "cloudless"},
		OpenPath:    "/",
		MinVRAMGB:   0,
		Verified:    false,
		// Host networking: the gateway binds host 127.0.0.1 so it can run with no
		// auth, and reaches the active engine via the host-published :8000 (D16).
		Network: "host",
	},
	{
		// Agent (D13/D14). Built locally from an embedded Dockerfile
		// (internal/apps/hermes) that runs the Hermes installer and bakes in the
		// Cloudless AI endpoint config. Installer-in-container is unverified.
		ID:          "hermes",
		Name:        "Hermes",
		Description: "Nous Research self-improving agent with persistent memory. Pre-wired to Cloudless AI.",
		Image:       "cloudless/hermes:local",
		Build:       "hermes",
		Env:         map[string]string{"OPENAI_API_KEY": "cloudless"},
		OpenPath:    "/",
		MinVRAMGB:   0,
		Verified:    false,
		Network:     cloudlessNet,
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
