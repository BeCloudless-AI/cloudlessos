// Package catalog defines the curated set of installable AI apps (container
// recipes). Curation — not "install anything" — is the reliability bet (see
// docs/ARCHITECTURE.md, Layer 3).
package catalog

import (
	"fmt"
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

// ConfigFile is an editable config file for an app, mounted into the container
// from the state dir (seeded from the app's embedded default).
type ConfigFile struct {
	File string `json:"file"` // filename in the app's embedded context + state dir
	Path string `json:"-"`    // mount path inside the container
	Lang string `json:"lang"` // json5 | yaml | env (UI hint)
}

// Field is an intuitive (form) config parameter, mapped into a config file.
type Field struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Help    string   `json:"help,omitempty"`
	Type    string   `json:"type"` // text | password | number | toggle | select
	Options []string `json:"options,omitempty"`
	Default string   `json:"default"`
	File    string   `json:"-"` // a ConfigFile.File this value lives in
	Path    string   `json:"-"` // JSON dot-path (json file) or env key (env file)
}

// App is a curated, installable AI application backed by a container image.
type App struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category,omitempty"` // launcher grouping (user-facing apps)
	Tagline     string            `json:"tagline,omitempty"`  // short "what it's for" line for the launcher
	Long        string            `json:"long,omitempty"`     // full paragraph for the app's launcher page
	Examples    []string          `json:"examples,omitempty"` // example "what you can do" bullets
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
	Hidden      bool              `json:"hidden,omitempty"` // installed/usable but not shown as a launcher tile
	Engine      bool              `json:"engine"`     // switchable inference engine (carries the stable alias)
	Network     string            `json:"-"`          // docker network to join (for inter-app DNS)
	Command     []string          `json:"-"`          // container command/args
	Volumes     map[string]string `json:"-"`          // host-or-named-volume -> containerPath
	Build       string            `json:"-"`          // embedded build-context name (build instead of pull)
	Config      []ConfigFile      `json:"config,omitempty"` // editable config files (mounted)
	Settings    []Field           `json:"-"`                // form fields (via /api/apps/{id}/settings)
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

// CloudflaredImage is the Cloudflare Tunnel client used to expose an app online
// via a zero-config quick tunnel (no account/domain needed).
const CloudflaredImage = "cloudflare/cloudflared:latest"

// SocatImage is the tiny TCP forwarder used to re-serve an app on the LAN IP.
const SocatImage = "alpine/socat:latest"

// TunnelName is the orchestrator-managed cloudflared container name for this app.
func (a App) TunnelName() string { return "cloudless-tunnel-" + a.ID }

// LanName is the orchestrator-managed LAN-forwarder container name for this app.
func (a App) LanName() string { return "cloudless-lan-" + a.ID }

// HasWebPort reports whether an app serves a web port that can be exposed
// (on the LAN or online). Engines/services are excluded.
func (a App) HasWebPort() bool { return a.Launchable() && a.PrimaryHostPort() > 0 }

// Tunnelable reports whether an app can be exposed online.
func (a App) Tunnelable() bool { return a.HasWebPort() }

// LanSidecarSpec builds the host-networked socat forwarder that re-serves this app
// on <ip>:<port> (same port as localhost; binding the specific LAN IP avoids a
// conflict with the app's own 127.0.0.1:<port> binding).
func (a App) LanSidecarSpec(ip string) engine.RunSpec {
	port := a.PrimaryHostPort()
	return engine.RunSpec{
		Name:    a.LanName(),
		Image:   SocatImage,
		Network: "host",
		Args: []string{
			fmt.Sprintf("TCP-LISTEN:%d,bind=%s,fork,reuseaddr", port, ip),
			fmt.Sprintf("TCP:127.0.0.1:%d", port),
		},
	}
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
// container name (e.g. Open WebUI -> the engine alias cloudless-ai:8000).
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
		Category:    "Chat & interfaces",
		Tagline:     "Full chat interface for your local model.",
		Long:        "Open WebUI is a full-featured chat interface for your local models — the friendly face of Cloudless AI. Hold long private conversations, upload documents to ask about, tune system prompts, and switch models, all running on your own GPU with nothing sent to the cloud.",
		Examples: []string{
			"Have long, private conversations with your local LLM",
			"Upload a document and ask questions about its contents",
			"Tune system prompts and switch between models",
			"Revisit and continue your past chats",
		},
		Image:       "ghcr.io/open-webui/open-webui:main",
		Ports:       map[int]int{3000: 8080},
		Env: map[string]string{
			"WEBUI_NAME":          "Cloudless AI",
			"WEBUI_AUTH":          "False", // local appliance: no login wall
			"ENABLE_OLLAMA_API":   "False",
			"OPENAI_API_BASE_URL": EngineEndpoint, // stable alias -> active engine (D15)
			"OPENAI_API_KEY":      "cloudless",    // engines ignore it unless --api-key is set
		},
		// Persist chats/settings/accounts so updates (new image, same volume) don't reset them.
		Volumes:    map[string]string{"cloudless-open-webui": "/app/backend/data"},
		GPUs:       "",
		OpenPath:   "/",
		MinVRAMGB:  0,
		Verified:   true,
		Preinstall: true,
		Hidden:     true, // reached via the hero "Chat with your Cloudless AI" button, not a tile
		Network:    cloudlessNet,
	},
	{
		ID:          "comfyui",
		Name:        "ComfyUI",
		Description: "Node-based image/video generation (Stable Diffusion, Flux, …).",
		Category:    "Image & video",
		Tagline:     "Generate and edit images locally.",
		Long:        "ComfyUI is a powerful node-based studio for image and video generation. Build repeatable pipelines with Stable Diffusion and Flux — text-to-image, inpainting, upscaling and more — entirely on your own hardware, with full control over every step.",
		Examples: []string{
			"Generate images from a text prompt",
			"Edit or restyle an existing photo with inpainting",
			"Upscale images to high resolution",
			"Design reusable generation workflows as node graphs",
		},
		// Community image; NOT yet validated on Blackwell (RTX 50xx) — see DECISIONS D11.
		Image: "mmartial/comfyui-nvidia-docker:latest",
		Ports: map[int]int{8188: 8188},
		// /comfy/mnt holds the ComfyUI install, models, outputs and custom nodes —
		// persist it so installs survive updates (the image seeds it on first run).
		Volumes:    map[string]string{"cloudless-comfyui": "/comfy/mnt"},
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  6,
		Verified:   false,
		Preinstall: true,
		Network:    cloudlessNet,
	},
	{
		// Ostris AI Toolkit — diffusion-model trainer with a web UI (official image
		// ostris/aitoolkit, UI on :8675). NOT yet validated on Blackwell.
		ID:          "ai-toolkit",
		Name:        "AI Toolkit",
		Description: "Ostris AI Toolkit — train your own image-model LoRAs (FLUX, SDXL).",
		Category:    "Training & fine-tuning",
		Tagline:     "Train image-model LoRAs — by Ostris.",
		Long:        "Ostris AI Toolkit is the ultimate trainer for fine-tuning diffusion models. From its web UI you can train your own LoRAs for FLUX, SDXL and more, on your own images — entirely on your GPU. The UI is protected by a password (default: cloudless).",
		Examples: []string{
			"Train a FLUX LoRA on your own photos",
			"Fine-tune SDXL on a custom style",
			"Prepare datasets and run training jobs from a web UI",
			"Export trained models to use in ComfyUI",
		},
		Image: "ostris/aitoolkit:latest",
		Ports: map[int]int{8675: 8675},
		Env: map[string]string{
			"AI_TOOLKIT_AUTH": "cloudless", // UI login (change in the app); keeps LAN/online sharing gated
			"NODE_ENV":        "production",
			"TZ":              "UTC",
		},
		Volumes: map[string]string{
			"cloudless-aitk-hf":     "/root/.cache/huggingface/hub", // shared model cache
			"cloudless-aitk-out":    "/app/ai-toolkit/output",       // trained models
			"cloudless-aitk-data":   "/app/ai-toolkit/datasets",     // training images
			"cloudless-aitk-config": "/app/ai-toolkit/config",       // job configs
		},
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  24, // FLUX LoRA training is VRAM-hungry; smaller models work with less
		Verified:   false,
		Preinstall: false,
		Network:    cloudlessNet,
	},
	{
		// Unsloth — fast, low-VRAM LLM fine-tuning. Official image bundles Jupyter
		// Lab + ready-made notebooks (UI on :8888). We only publish 8888 (the
		// image's :8000 secondary port would collide with the engine). Unverified
		// on Blackwell.
		ID:          "unsloth",
		Name:        "Unsloth",
		Description: "Fast, low-VRAM fine-tuning for LLMs (Llama, Qwen, Gemma) in a notebook.",
		Category:    "Training & fine-tuning",
		Tagline:     "Fine-tune LLMs 2× faster, with less VRAM.",
		Long:        "Unsloth makes fine-tuning large language models fast and memory-efficient — often 2× faster and with far less VRAM. This image bundles Jupyter Lab with Unsloth and ready-to-run notebooks, so you can fine-tune models like Llama, Qwen and Gemma on your own data, then export to GGUF or your inference engine. The notebook is protected by a password (default: cloudless).",
		Examples: []string{
			"Fine-tune Llama or Qwen on your own dataset",
			"Train QLoRA adapters that fit in low VRAM",
			"Run ready-made fine-tuning notebooks",
			"Export trained models to GGUF for local inference",
		},
		Image: "unsloth/unsloth:latest",
		Ports: map[int]int{8888: 8888}, // Jupyter Lab (image's :8000/:22 left unpublished)
		Env: map[string]string{
			"JUPYTER_PASSWORD": "cloudless", // notebook login (change in the app)
		},
		Volumes: map[string]string{
			"cloudless-unsloth-work": "/workspace/work", // notebooks, datasets, outputs
		},
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  8, // Unsloth's whole point is low-VRAM fine-tuning
		Verified:   false,
		Preinstall: false,
		Network:    cloudlessNet,
	},
	{
		// Agent (D13/D14). No upstream image; built locally from an embedded
		// Dockerfile (internal/apps/openclaw) that npm-installs OpenClaw and bakes
		// in the Cloudless AI provider config.
		ID:          "openclaw",
		Name:        "OpenClaw",
		Description: "Open-source personal AI agent that takes actions on your machine. Pre-wired to Cloudless AI.",
		Category:    "Agents & automation",
		Tagline:     "Autonomous agent that uses tools.",
		Long:        "OpenClaw is an open-source autonomous agent that takes real actions on your machine — reading files, running tools, and chaining steps to finish a task. It comes pre-wired to Cloudless AI, so there's nothing to set up: just tell it what you need.",
		Examples: []string{
			"Ask it to explain or refactor code in a project",
			"Automate multi-step tasks using tools",
			"Have it read files and answer questions about them",
			"Let it draft and run commands on your behalf",
		},
		// Pulled from the Cloudless Docker Hub space (built from internal/apps/openclaw,
		// which stays the source of truth for rebuilding + re-pushing). Manifest-pinnable.
		Image:       "samuelcardillo/cloudless-openclaw:v1",
		Ports:       map[int]int{18789: 18789}, // for the UI link; host networking binds it directly
		Env:         map[string]string{"CUSTOM_API_KEY": "cloudless"},
		Config:      []ConfigFile{{File: "openclaw.json", Path: "/root/.openclaw/openclaw.json", Lang: "json"}},
		Settings: []Field{
			{Key: "baseUrl", Label: "AI endpoint", Help: "OpenAI-compatible URL OpenClaw sends requests to.",
				Type: "text", Default: "http://127.0.0.1:8000/v1", File: "openclaw.json", Path: "models.providers.custom.baseUrl"},
			{Key: "alias", Label: "Model name", Help: "Display name shown for the model.",
				Type: "text", Default: "Cloudless AI", File: "openclaw.json", Path: "agents.defaults.models.custom/cloudless.alias"},
		},
		OpenPath:  "/",
		MinVRAMGB: 0,
		Verified:  false,
		// Host networking: the gateway binds host 127.0.0.1 so it can run with no
		// auth, and reaches the active engine via the host-published :8000 (D16).
		Network: "host",
	},
	{
		// Agent (D13/D14). Pulled from the Cloudless Docker Hub space (built from
		// internal/apps/hermes, which stays the source of truth for rebuilds).
		ID:          "hermes",
		Name:        "Hermes",
		Description: "Nous Research self-improving agent with persistent memory. Pre-wired to Cloudless AI.",
		Category:    "Agents & automation",
		Tagline:     "Personal agent on Telegram / Discord.",
		Long:        "Hermes is a personal AI agent with persistent memory from Nous Research. Reach it from Telegram or Discord and it keeps context across conversations — an always-available assistant that runs entirely on your own hardware.",
		Examples: []string{
			"Chat with your AI from Telegram or Discord",
			"Keep memory and context across sessions",
			"Set an allowlist of who is allowed to talk to it",
			"Use it as a personal assistant on the go",
		},
		Image:       "samuelcardillo/cloudless-hermes:v1",
		Env:         map[string]string{"OPENAI_API_KEY": "cloudless"},
		Config: []ConfigFile{
			{File: "config.yaml", Path: "/root/.hermes/config.yaml", Lang: "yaml"},
			{File: "hermes.env", Path: "/root/.hermes/.env", Lang: "env"},
		},
		Settings: []Field{
			{Key: "allowAll", Label: "Allow all users", Help: "Let anyone message the agent (otherwise use the allowlist below).",
				Type: "toggle", Default: "false", File: "hermes.env", Path: "GATEWAY_ALLOW_ALL_USERS"},
			{Key: "tgToken", Label: "Telegram bot token", Type: "password", Default: "",
				File: "hermes.env", Path: "TELEGRAM_BOT_TOKEN"},
			{Key: "tgUsers", Label: "Telegram allowed users", Help: "Comma-separated Telegram user IDs.",
				Type: "text", Default: "", File: "hermes.env", Path: "TELEGRAM_ALLOWED_USERS"},
			{Key: "discordToken", Label: "Discord bot token", Type: "password", Default: "",
				File: "hermes.env", Path: "DISCORD_BOT_TOKEN"},
		},
		OpenPath:  "/",
		MinVRAMGB: 0,
		Verified:  false,
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

// DefaultPins are the apps pinned to the dashboard "fast launch" before the user
// customizes. Empty by design — the user pins what they want from the launcher.
func DefaultPins() []string { return nil }

// Launchable reports whether an app belongs in the App Launcher (user-facing,
// not an inference engine / infrastructure service).
func (a App) Launchable() bool { return !a.Service }
