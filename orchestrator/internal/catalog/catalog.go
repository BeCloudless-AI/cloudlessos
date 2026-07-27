// Package catalog defines the curated set of installable AI apps (container
// recipes). Curation — not "install anything" — is the reliability bet (see
// docs/ARCHITECTURE.md, Layer 3).
package catalog

import (
	"fmt"
	"os"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/platform"
)

// The generic model is also the substitution sentinel embedded in engine recipes.
// DefaultModel resolves the actual first-boot model at runtime so one package can
// safely serve both regular PCs and DGX Spark without architecture-specific forks.
const (
	defaultModelSentinel = "Qwen/Qwen2.5-1.5B-Instruct"
	dgxSparkDefaultModel = "Qwen/Qwen3.6-35B-A3B"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ConfigFile is an editable config file for an app, mounted into the container
// from the state dir (seeded from the app's embedded default).
type ConfigFile struct {
	File string `json:"file"`           // filename in the app's embedded context + state dir
	Path string `json:"path,omitempty"` // mount path inside the container ("" when Env)
	Lang string `json:"lang"`           // json5 | yaml | env (UI hint)
	Env  bool   `json:"env,omitempty"`  // inject this env file's KEY=VALUE into the container ENV instead of mounting it (for apps that read process env, e.g. Open WebUI)
}

// AdminInfo explains how an app is administered beyond Cloudless's own settings —
// shown as an info panel on the app's settings page (e.g. "use the app's own admin
// panel"), optionally with default admin credentials.
type AdminInfo struct {
	Note string `json:"note"`           // where/how to manage the app (its own admin UI)
	User string `json:"user,omitempty"` // default admin username/email
	Pass string `json:"pass,omitempty"` // default admin password
}

// Field is an intuitive (form) config parameter, mapped into a config file.
type Field struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Help    string   `json:"help,omitempty"`
	Type    string   `json:"type"` // text | password | number | toggle | select
	Options []string `json:"options,omitempty"`
	Default string   `json:"default"`
	File    string   `json:"file,omitempty"` // a ConfigFile.File this value lives in
	Path    string   `json:"path,omitempty"` // JSON dot-path (json file) or env key (env file)
}

// HealthContract is the declarative readiness contract for an application.
// The orchestrator uses it after installs, updates, dependency starts and model
// promotions instead of assuming that a running container is ready.
type HealthContract struct {
	Kind           string `json:"kind,omitempty"` // http | container | openai
	Path           string `json:"path,omitempty"`
	Port           int    `json:"port,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// LLMContract declares how an application consumes the active inference
// service. It lets model changes prove dependent applications before promotion.
type LLMContract struct {
	Consumes   bool   `json:"consumes"`
	Route      string `json:"route,omitempty"`      // gateway | direct
	Pinning    string `json:"pinning,omitempty"`    // none | dynamic
	MinContext int    `json:"minContext,omitempty"` // tokens
	ProbePath  string `json:"probePath,omitempty"`
}

// ResourceContract records the installation and runtime envelope used by the
// launcher, dependency resolver and model-fit guidance.
type ResourceContract struct {
	MemoryGB int `json:"memoryGB,omitempty"`
	DiskGB   int `json:"diskGB,omitempty"`
	VRAMGB   int `json:"vramGB,omitempty"`
}

// ExposureContract makes network risk reviewable data. Public and LAN access
// remain explicit user actions; RequireAuth is enforced before an app may be
// exposed outside loopback.
type ExposureContract struct {
	Risk        string `json:"risk,omitempty"`
	LAN         string `json:"lan,omitempty"`    // none | opt-in
	Public      string `json:"public,omitempty"` // none | opt-in
	RequireAuth bool   `json:"requireAuth,omitempty"`
}

// App is a curated, installable AI application backed by a container image.
type App struct {
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	Description         string              `json:"description"`
	Category            string              `json:"category,omitempty"`      // launcher grouping (user-facing apps)
	Tagline             string              `json:"tagline,omitempty"`       // short "what it's for" line for the launcher
	Long                string              `json:"long,omitempty"`          // full paragraph for the app's launcher page
	Examples            []string            `json:"examples,omitempty"`      // example "what you can do" bullets
	Image               string              `json:"image"`                   // empty = recipe not yet available
	ArchImages          map[string]string   `json:"archImages,omitempty"`    // architecture-specific image override
	Architectures       []string            `json:"architectures,omitempty"` // empty = image is expected to be multi-arch
	Platforms           []string            `json:"platforms,omitempty"`     // empty = every supported Cloudless platform
	ArchPlatforms       map[string][]string `json:"archPlatforms,omitempty"` // architecture-specific platform restriction
	MinCloudlessVersion string              `json:"minCloudlessVersion,omitempty"`
	MinDGXOSVersion     string              `json:"minDgxOsVersion,omitempty"`
	RequiredFeatures    []string            `json:"requiredFeatures,omitempty"`
	Ports               map[int]int         `json:"ports"` // hostPort -> containerPort
	Env                 map[string]string   `json:"env,omitempty"`
	GPUs                string              `json:"gpus"`                   // "all", "0", ... or "" for none
	OpenPath            string              `json:"openPath"`               // URL path to open once running
	MinVRAMGB           int                 `json:"minVramGB"`              // rough VRAM floor for usefulness
	Verified            bool                `json:"verified"`               // recipe validated on Cloudless dev hardware
	Preinstall          bool                `json:"preinstall"`             // pulled AND run automatically on first boot
	Prefetch            bool                `json:"prefetch,omitempty"`     // image pulled on boot but not run (ready alternative)
	Service             bool                `json:"service"`                // infrastructure (engine), hidden from the launcher
	Hidden              bool                `json:"hidden,omitempty"`       // installed/usable but not shown as a launcher tile
	LocalOnly           bool                `json:"localOnly,omitempty"`    // never create LAN or public tunnel sidecars
	Engine              bool                `json:"engine"`                 // switchable inference engine (carries the stable alias)
	NeedsEngine         bool                `json:"needsEngine"`            // depends on the LLM engine (gated until the model is served)
	Network             string              `json:"network,omitempty"`      // docker network to join (for inter-app DNS)
	IPC                 string              `json:"ipc,omitempty"`          // container IPC namespace mode
	Ulimits             []string            `json:"ulimits,omitempty"`      // container resource limits
	Command             []string            `json:"command,omitempty"`      // container command/args
	ArchCommands        map[string][]string `json:"archCommands,omitempty"` // architecture-specific command override
	Volumes             map[string]string   `json:"volumes,omitempty"`      // host-or-named-volume -> containerPath
	DataPath            string              `json:"dataPath,omitempty"`     // mount the app's complete Cloudless-managed state directory here
	DataUID             int                 `json:"dataUID,omitempty"`      // container UID that must own DataPath (0 = keep host ownership)
	Build               string              `json:"build,omitempty"`        // embedded build-context name (build instead of pull)
	Config              []ConfigFile        `json:"config,omitempty"`       // editable config files (mounted)
	Settings            []Field             `json:"settings,omitempty"`     // form fields (via /api/apps/{id}/settings)
	Admin               *AdminInfo          `json:"admin,omitempty"`        // how the app is administered beyond Cloudless settings
	Dependencies        []string            `json:"dependencies,omitempty"`
	Health              HealthContract      `json:"health,omitempty"`
	LLM                 *LLMContract        `json:"llm,omitempty"`
	Resources           ResourceContract    `json:"resources,omitempty"`
	Exposure            ExposureContract    `json:"exposure,omitempty"`
}

// ContainerName is the orchestrator-managed container name for this app.
func (a App) ContainerName() string { return "cloudless-" + a.ID }

// SupportsHost reports whether the curated recipe is available for this CPU
// architecture. Apps with no restriction are expected to publish multi-arch
// images.
func (a App) SupportsHost() bool {
	return a.Availability().Available
}

// Availability evaluates the same centralized requirements used by the
// capabilities API. Catalog.Get also uses this result, making direct API calls
// unable to bypass a platform-locked recipe.
func (a App) Availability() capabilities.Status {
	platforms := a.Platforms
	if restricted, ok := a.ArchPlatforms[platform.Architecture()]; ok {
		platforms = restricted
	}
	return capabilities.Check(capabilities.Current(), capabilities.Requirement{
		Platforms:           platforms,
		Architectures:       a.Architectures,
		MinCloudlessVersion: a.MinCloudlessVersion,
		MinDGXOSVersion:     a.MinDGXOSVersion,
		RequiredFeatures:    a.RequiredFeatures,
	})
}

// HostImage selects the image validated for the current architecture.
func (a App) HostImage() string {
	if image := a.ArchImages[platform.Architecture()]; image != "" {
		return image
	}
	return a.Image
}

func (a App) forHost() App {
	a.Image = a.HostImage()
	if command := a.ArchCommands[platform.Architecture()]; len(command) > 0 {
		a.Command = append([]string(nil), command...)
	}
	return a
}

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

// HasWebPort reports whether an app serves a web port that its signed exposure
// contract permits CloudlessOS to publish outside loopback.
func (a App) HasWebPort() bool {
	return a.Launchable() && !a.LocalOnly && a.PrimaryHostPort() > 0 &&
		(a.Exposure.LAN == "opt-in" || a.Exposure.Public == "opt-in")
}

func (a App) LanShareable() bool { return a.HasWebPort() && a.Exposure.LAN == "opt-in" }

// Tunnelable reports whether an app can be exposed online.
func (a App) Tunnelable() bool {
	return a.HasWebPort() && a.Exposure.Public == "opt-in" && a.Exposure.RequireAuth
}

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
		IPC:     a.IPC,
		Ulimits: append([]string(nil), a.Ulimits...),
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
	for _, a := range All() {
		if a.Engine {
			out = append(out, a)
		}
	}
	return out
}

// DefaultModel returns the model used when the user has not selected one.
// An explicit environment override remains authoritative on every platform.
func DefaultModel() string {
	return envOr("CLOUDLESS_DEFAULT_MODEL", func() string {
		if platform.IsDGXSpark() {
			return dgxSparkDefaultModel
		}
		return defaultModelSentinel
	}())
}

// EngineSpec builds an engine's run spec, substituting the chosen model for the
// default. model "" (or the default) leaves the catalog command unchanged.
func EngineSpec(a App, model string) engine.RunSpec {
	rs := a.Spec()
	if model == "" {
		model = DefaultModel()
	}
	args := make([]string, len(rs.Args)) // copy: don't mutate the shared catalog slice
	copy(args, rs.Args)
	for i := range args {
		if args[i] == defaultModelSentinel {
			args[i] = model
		}
	}
	rs.Args = args
	return rs
}

// EngineSpecOverride is EngineSpec with the container command replaced by a
// user-saved override (the args after the image) when non-empty — so a launch
// command edited in the Model Manager is honored everywhere the engine starts.
func EngineSpecOverride(a App, model string, override []string) engine.RunSpec {
	rs := EngineSpec(a, model)
	if len(override) > 0 {
		rs.Args = append([]string(nil), override...)
	}
	return rs
}

// DefaultEngine returns the id of the default engine (the preinstalled one).
func DefaultEngine() string {
	for _, a := range All() {
		if a.Engine && a.Preinstall {
			return a.ID
		}
	}
	for _, a := range All() {
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
	for _, a := range All() {
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
	EngineAlias         = "cloudless-ai"
	EnginePort          = 8000
	HermesAPIPort       = 8642
	HermesDashboardPort = 9119
)

// EngineEndpoint is the stable OpenAI base URL clients are configured with.
const EngineEndpoint = "http://cloudless-ai:8000/v1"

// legacyApps is retained temporarily as migration evidence. Runtime authority
// comes from the signed embedded App Manifest v2 below.
var legacyApps = []App{
	{
		// vLLM is the default inference engine powering "Cloudless AI" (D12).
		// OpenAI-compatible server; Open WebUI talks to it over the OpenAI API.
		ID:          "vllm",
		Name:        "vLLM Engine",
		Description: "High-throughput inference engine powering Cloudless AI.",
		Image:       "vllm/vllm-openai:latest",
		ArchImages: map[string]string{
			"arm64": "nvcr.io/nvidia/vllm:26.05.post1-py3",
		},
		ArchPlatforms: map[string][]string{"arm64": {platform.DGXSpark}},
		Ports:         map[int]int{8000: 8000},
		// Image entrypoint is `vllm serve`; the model is the positional arg.
		// Tool calling enabled (agents like Hermes send tools). The generic model
		// uses the Hermes parser; the Spark command uses Qwen3.6's native parsers.
		Command: []string{
			defaultModelSentinel,
			"--served-model-name", "cloudless",
			"--gpu-memory-utilization", "0.5",
			"--max-model-len", "32768", // Qwen2.5 native context; agents send big prompts
			"--enable-auto-tool-choice",
			"--tool-call-parser", "hermes",
		},
		// NVIDIA's Grace Blackwell image does not use the upstream image's
		// `vllm serve` entrypoint, so include it explicitly on ARM64.
		ArchCommands: map[string][]string{
			"arm64": {
				"vllm", "serve", defaultModelSentinel,
				"--served-model-name", "cloudless",
				"--gpu-memory-utilization", "0.75",
				"--max-model-len", "32768",
				"--reasoning-parser", "qwen3",
				"--enable-auto-tool-choice",
				"--tool-call-parser", "qwen3_coder",
			},
		},
		Volumes:    map[string]string{"cloudless-hf": "/root/.cache/huggingface"}, // persist model cache
		IPC:        "host",
		Ulimits:    []string{"memlock=-1", "stack=67108864"},
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  6,
		Verified:   true, // validated on RTX Blackwell and DGX Spark GB10 (sm_121)
		Preinstall: true,
		Service:    true,
		Engine:     true,
		Network:    cloudlessNet,
		Health:     HealthContract{Kind: "openai", Path: "/v1/models", TimeoutSeconds: 900},
		Resources:  ResourceContract{MemoryGB: 8, DiskGB: 12, VRAMGB: 6},
		Exposure:   ExposureContract{Risk: "inference-control-plane", LAN: "none", Public: "none", RequireAuth: true},
	},
	{
		// Alternative engine (D13): pre-fetched (image ready) but not run by
		// default — switch to it instead of vLLM. RadixAttention; OpenAI-compatible.
		// Entrypoint is the NVIDIA wrapper, so the launch command is passed as args.
		ID:          "sglang",
		Name:        "SGLang Engine",
		Description: "Alternative high-performance inference engine (OpenAI-compatible).",
		Image:       "lmsysorg/sglang:latest",
		ArchImages: map[string]string{
			"arm64": "lmsysorg/sglang:latest-cu130",
		},
		ArchPlatforms: map[string][]string{"arm64": {platform.DGXSpark}},
		Ports:         map[int]int{8000: 8000}, // same fixed port as vLLM (one engine runs at a time)
		Command: []string{
			"python3", "-m", "sglang.launch_server",
			"--model-path", defaultModelSentinel,
			"--served-model-name", "cloudless",
			"--host", "0.0.0.0", "--port", "8000",
			"--mem-fraction-static", "0.5",
			"--context-length", "32768", // match vLLM; agents send big prompts
			"--tool-call-parser", "qwen25", // agents need tool calling
			"--enable-metrics", // expose Prometheus /metrics for the inference dashboard (vLLM has it on by default)
		},
		ArchCommands: map[string][]string{
			"arm64": {
				"python3", "-m", "sglang.launch_server",
				"--model-path", defaultModelSentinel,
				"--served-model-name", "cloudless",
				"--host", "0.0.0.0", "--port", "8000",
				"--mem-fraction-static", "0.75",
				"--context-length", "32768",
				"--reasoning-parser", "qwen3",
				"--tool-call-parser", "qwen3_coder",
				"--enable-metrics",
			},
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
		Health:    HealthContract{Kind: "openai", Path: "/v1/models", TimeoutSeconds: 900},
		Resources: ResourceContract{MemoryGB: 8, DiskGB: 45, VRAMGB: 6},
		Exposure:  ExposureContract{Risk: "inference-control-plane", LAN: "none", Public: "none", RequireAuth: true},
	},
	{
		// Lightweight engine: runs on CPU+GPU and can offload layers to system RAM, so
		// it can serve models that don't fit in VRAM. Uses compact GGUF model files.
		// The server image's entrypoint IS llama-server, so Command holds its flags.
		// Note: it serves its bundled GGUF; the Model Manager's model pick (HF safetensors)
		// applies to vLLM/SGLang, not here — GGUF model switching is a follow-up.
		ID:          "llamacpp",
		Name:        "llama.cpp Engine",
		Description: "Lightweight engine — runs on CPU+GPU and offloads to system RAM (GGUF models).",
		Image:       "ghcr.io/ggml-org/llama.cpp:server-cuda",
		Ports:       map[int]int{8000: 8000},
		Command: []string{
			"-hf", "bartowski/Qwen2.5-1.5B-Instruct-GGUF:Q4_K_M", // compact GGUF default
			"--alias", "cloudless",
			"--host", "0.0.0.0", "--port", "8000",
			"-ngl", "999", // offload all layers to GPU when they fit; lower this to spill into RAM
			"--jinja",   // chat template incl. tool calling (for agents)
			"--metrics", // expose Prometheus /metrics for the inference dashboard
		},
		Volumes:   map[string]string{"cloudless-llamacpp": "/root/.cache/llama.cpp"},
		GPUs:      "all",
		OpenPath:  "/",
		MinVRAMGB: 0, // can run with little/no VRAM (CPU + RAM offload)
		Verified:  false,
		Prefetch:  true,
		Service:   true,
		Engine:    true,
		Network:   cloudlessNet,
		Health:    HealthContract{Kind: "openai", Path: "/v1/models", TimeoutSeconds: 600},
		Resources: ResourceContract{MemoryGB: 4, DiskGB: 8},
		Exposure:  ExposureContract{Risk: "inference-control-plane", LAN: "none", Public: "none", RequireAuth: true},
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
		Image: "ghcr.io/open-webui/open-webui:main",
		Ports: map[int]int{3000: 8080},
		Env: map[string]string{
			"WEBUI_NAME": "Cloudless AI",
			// Auth defaults (overridable in Settings, which writes webui.env and is
			// injected as container env): off by default = no login wall on a personal
			// appliance; sign-ups allowed so the first account can be created.
			"WEBUI_AUTH":          "False",
			"ENABLE_SIGNUP":       "True",
			"ENABLE_OLLAMA_API":   "False",
			"OPENAI_API_BASE_URL": EngineEndpoint, // stable alias -> active engine (D15)
			"OPENAI_API_KEY":      "cloudless",    // engines ignore it unless --api-key is set
		},
		NeedsEngine: true, // chat UI for the local model — useless until the engine serves it
		// webui.env is injected into the container ENV (Open WebUI reads process env),
		// not mounted. The Settings toggles write WEBUI_AUTH / ENABLE_SIGNUP here.
		Config: []ConfigFile{{File: "webui.env", Lang: "env", Env: true}},
		Settings: []Field{
			{Key: "requireAuth", Label: "Require an account to use it", Type: "toggle", Default: "false",
				Help: "Off: anyone who opens Open WebUI can use it, no login. On: people must sign in with an account — the first account created becomes the administrator.",
				File: "webui.env", Path: "WEBUI_AUTH"},
			{Key: "allowSignup", Label: "Allow new sign-ups", Type: "toggle", Default: "true",
				Help: "Let people create their own accounts. Turn off once your accounts exist to lock new sign-ups.",
				File: "webui.env", Path: "ENABLE_SIGNUP"},
		},
		Admin: &AdminInfo{
			// Open WebUI auto-creates this admin account the first time it runs with login
			// off (its built-in default); enabling login lets you sign in with it. If the
			// app instead asks you to create the admin, use the same details. These are
			// well-known defaults — change the password immediately in the Admin Panel.
			Note: "Everything else about Open WebUI — users, model access, permissions, document/RAG settings and more — is managed inside Open WebUI's own Admin Panel (open the app, then top-right menu → Admin Panel). When you turn on account login, sign in with the built-in admin below (if Open WebUI asks you to create the admin instead, use the same details), then change the password right away — these are well-known defaults.",
			User: "admin@localhost",
			Pass: "admin",
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
		Health:     HealthContract{Kind: "http", Path: "/health", TimeoutSeconds: 180},
		LLM:        &LLMContract{Consumes: true, Route: "gateway", Pinning: "dynamic", MinContext: 8192, ProbePath: "/api/models"},
		Resources:  ResourceContract{MemoryGB: 4, DiskGB: 5},
		Exposure:   ExposureContract{Risk: "chat-ui", LAN: "opt-in", Public: "opt-in", RequireAuth: true},
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
		ArchImages: map[string]string{
			"arm64": "mmartial/comfyui-nvidia-docker:ubuntu24_cuda13.2-dgx-latest",
		},
		ArchPlatforms: map[string][]string{"arm64": {platform.DGXSpark}},
		Architectures: []string{"amd64", "arm64"},
		Ports:         map[int]int{8188: 8188},
		// /comfy/mnt holds the ComfyUI install, models, outputs and custom nodes —
		// persist it so installs survive updates (the image seeds it on first run).
		Volumes:    map[string]string{"cloudless-comfyui": "/comfy/mnt"},
		GPUs:       "all",
		OpenPath:   "/",
		MinVRAMGB:  6,
		Verified:   false,
		Preinstall: false,
		Network:    cloudlessNet,
		Health:     HealthContract{Kind: "http", Path: "/system_stats", TimeoutSeconds: 600},
		Resources:  ResourceContract{MemoryGB: 8, DiskGB: 20, VRAMGB: 6},
		Exposure:   ExposureContract{Risk: "gpu-workload-ui", LAN: "opt-in", Public: "none"},
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
		Image:         "ostris/aitoolkit:latest",
		Architectures: []string{"amd64"},
		Ports:         map[int]int{8675: 8675},
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
		Health:     HealthContract{Kind: "http", Path: "/", TimeoutSeconds: 300},
		Resources:  ResourceContract{MemoryGB: 12, DiskGB: 30, VRAMGB: 24},
		Exposure:   ExposureContract{Risk: "model-training-ui", LAN: "opt-in", Public: "opt-in", RequireAuth: true},
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
		Image:         "unsloth/unsloth:latest",
		Architectures: []string{"amd64"},
		Ports:         map[int]int{8888: 8888}, // Jupyter Lab (image's :8000/:22 left unpublished)
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
		Health:     HealthContract{Kind: "http", Path: "/lab", TimeoutSeconds: 300},
		Resources:  ResourceContract{MemoryGB: 16, DiskGB: 30, VRAMGB: 8},
		Exposure:   ExposureContract{Risk: "code-execution-ui", LAN: "opt-in", Public: "opt-in", RequireAuth: true},
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
		Image:         "samuelcardillo/cloudless-openclaw:v1",
		Architectures: []string{"amd64"},
		Ports:         map[int]int{18789: 18789}, // for the UI link; host networking binds it directly
		NeedsEngine:   true,                      // agent that drives the local model
		Env:           map[string]string{"CUSTOM_API_KEY": "cloudless"},
		Config:        []ConfigFile{{File: "openclaw.json", Path: "/root/.openclaw/openclaw.json", Lang: "json"}},
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
		Network:   "host",
		Health:    HealthContract{Kind: "http", Path: "/", TimeoutSeconds: 180},
		LLM:       &LLMContract{Consumes: true, Route: "gateway", Pinning: "dynamic", MinContext: 32768, ProbePath: "/"},
		Resources: ResourceContract{MemoryGB: 4, DiskGB: 5},
		Exposure:  ExposureContract{Risk: "autonomous-agent", LAN: "none", Public: "none", RequireAuth: true},
	},
	{
		// The official Hermes container includes its gateway and browser dashboard.
		// One private state directory preserves configuration, sessions and memory.
		ID:          "hermes",
		Name:        "Hermes",
		Description: "Nous Research's personal agent and dashboard, integrated with Cloudless AI.",
		Category:    "Agents & automation",
		Tagline:     "A persistent local agent with tools and memory.",
		Long:        "Hermes is a personal AI agent from Nous Research with persistent memory, tools, scheduled tasks and messaging integrations. CloudlessOS opens its local dashboard directly and connects it to the active Cloudless AI engine, so chats and model inference stay on this machine.",
		Examples: []string{
			"Use the local dashboard for persistent agent conversations",
			"Chat with your AI from Telegram or Discord",
			"Run tool-assisted tasks and scheduled automations",
			"Keep memory and context across sessions and upgrades",
		},
		Image:       "nousresearch/hermes-agent:v2026.7.20",
		Ports:       map[int]int{HermesDashboardPort: HermesDashboardPort},
		NeedsEngine: true, // agent that drives the local model
		Env: map[string]string{
			"HERMES_DASHBOARD":            "1",
			"HERMES_DASHBOARD_HOST":       "127.0.0.1",
			"HERMES_DASHBOARD_PORT":       "9119",
			"HERMES_DASHBOARD_FILES_ROOT": "/opt/data/workspace",
			"API_SERVER_ENABLED":          "true",
			"API_SERVER_HOST":             "127.0.0.1",
			"API_SERVER_PORT":             "8642",
			"API_SERVER_MODEL_NAME":       "hermes-agent",
			"HERMES_MAX_TOKENS":           "4096",
			"OPENAI_API_KEY":              "cloudless",
		},
		Config: []ConfigFile{
			{File: "config.yaml", Lang: "yaml"},
			{File: "hermes.env", Lang: "env", Env: true},
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
		OpenPath: "/",
		// The official image runs its services as the unprivileged hermes user.
		DataPath:   "/opt/data",
		DataUID:    10000,
		Command:    []string{"gateway", "run"},
		MinVRAMGB:  0,
		Verified:   false,
		Preinstall: true,
		LocalOnly:  true, // dashboard can read/write credentials; never expose it implicitly
		// Loopback dashboard + direct access to the host-published Cloudless engine.
		Network:   "host",
		Health:    HealthContract{Kind: "http", Path: "/api/status", TimeoutSeconds: 180},
		LLM:       &LLMContract{Consumes: true, Route: "gateway", Pinning: "dynamic", MinContext: 32768, ProbePath: "/api/talk/message/stream"},
		Resources: ResourceContract{MemoryGB: 6, DiskGB: 8},
		Exposure:  ExposureContract{Risk: "autonomous-agent", LAN: "none", Public: "none", RequireAuth: true},
	},
}

var apps = mustLoadManifest()

// All returns recipes supported by this host. Unsupported architecture-specific
// apps are omitted instead of presenting an Install action that can never work.
func All() []App {
	out := make([]App, 0, len(apps))
	for _, a := range apps {
		if a.SupportsHost() {
			out = append(out, a.forHost())
		}
	}
	return out
}

// Get returns the app with the given id.
func Get(id string) (App, bool) {
	for _, a := range apps {
		if a.ID == id && a.SupportsHost() {
			return a.forHost(), true
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
