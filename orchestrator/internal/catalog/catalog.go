// Package catalog defines the curated set of installable AI apps (container
// recipes). Curation — not "install anything" — is the reliability bet (see
// docs/ARCHITECTURE.md, Layer 3).
package catalog

import (
	"fmt"
	"os"
	"strings"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/models"
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
// panel"), optionally with administrator onboarding information.
type AdminInfo struct {
	Note            string `json:"note"`                      // where/how to manage the app (its own admin UI)
	User            string `json:"user,omitempty"`            // administrator username/email
	Pass            string `json:"pass,omitempty"`            // fixed password (discouraged; rejected for published apps)
	ManagedPassword string `json:"managedPassword,omitempty"` // daemon-owned secret name, resolved only by local settings
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
	SupportLevel        string              `json:"supportLevel,omitempty"` // experimental | preview | supported
	RequiredFeatures    []string            `json:"requiredFeatures,omitempty"`
	Ports               map[int]int         `json:"ports"` // hostPort -> containerPort
	Env                 map[string]string   `json:"env,omitempty"`
	GPUs                string              `json:"gpus"`                   // "all", "0", ... or "" for none
	OpenPath            string              `json:"openPath"`               // URL path to open once running
	EmbeddedPath        string              `json:"embeddedPath,omitempty"` // same-origin path used by the Cloudless app viewer
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
	if runtime, ok := models.Get(model); ok && runtime.PreferredEngine == a.ID && runtime.RuntimeImage != "" {
		rs.Image = runtime.RuntimeImage
		rs.EntryPoint = runtime.RuntimeEntry
		rs.Args = append([]string(nil), runtime.RuntimeCommand...)
		return enforcePrivateEngineContract(rs)
	}
	args := make([]string, len(rs.Args)) // copy: don't mutate the shared catalog slice
	copy(args, rs.Args)
	for i := range args {
		if args[i] == defaultModelSentinel {
			args[i] = model
		}
	}
	rs.Args = args
	return enforcePrivateEngineContract(rs)
}

// EngineSpecOverride is EngineSpec with the container command replaced by a
// user-saved override (the args after the image) when non-empty — so a launch
// command edited in the Model Manager is honored everywhere the engine starts.
func EngineSpecOverride(a App, model string, override []string) engine.RunSpec {
	rs := EngineSpec(a, model)
	if len(override) > 0 {
		rs.Args = append([]string(nil), override...)
	}
	return enforcePrivateEngineContract(rs)
}

// enforcePrivateEngineContract is the final launch-time guardrail for every
// bundled and user-built inference image. Saved command overrides may tune an
// engine, but cannot move its private socket or change its internal model name.
// Client-facing customization belongs to the authenticated gateway instead.
func enforcePrivateEngineContract(spec engine.RunSpec) engine.RunSpec {
	spec.Ports = map[int]int{EnginePort: EnginePort}
	args := append([]string(nil), spec.Args...)
	setFlag := func(flags []string, value string) bool {
		for i := 0; i < len(args); i++ {
			for _, flag := range flags {
				if args[i] == flag {
					if i+1 < len(args) {
						args[i+1] = value
					} else {
						args = append(args, value)
					}
					return true
				}
				if strings.HasPrefix(args[i], flag+"=") {
					args[i] = flag + "=" + value
					return true
				}
			}
		}
		return false
	}
	setFlag([]string{"--port"}, fmt.Sprintf("%d", EnginePort))
	setFlag([]string{"--served-model-name", "--alias"}, "cloudless")
	spec.Args = args
	return spec
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

var manifestDocument = mustLoadManifest()
var apps = manifestDocument.Apps

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
func (a App) Launchable() bool { return !a.Service && !a.Hidden }

// Pinnable includes ordinary launcher apps plus hidden recipes that are the
// launch target of a user-facing capability. Hidden prevents duplicate entries
// in the launcher; it must not prevent the installed product becoming a
// dashboard shortcut.
func (a App) Pinnable() bool {
	if a.Service {
		return false
	}
	if !a.Hidden {
		return true
	}
	for _, pack := range Packs() {
		if pack.LaunchApp == a.ID {
			return true
		}
	}
	return false
}
