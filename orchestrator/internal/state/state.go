// Package state is a small JSON-file-backed store for per-user/per-install daemon
// state — currently just first-run / onboarding status. It is the source of truth
// for "has this CloudlessOS install (user account) been set up before?".
package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/modelstorage"
)

// Profile is the user-controlled profile for this CloudlessOS install.
type Profile struct {
	Name   string `json:"name,omitempty"`   // display name (greeting / personalization)
	Region string `json:"region,omitempty"` // ISO-3166 alpha-2 override, e.g. "FR"; "" = auto-detect
}

// DisplayPreference is the validated X11 topology and primary output mode
// CloudlessOS should restore when its kiosk session starts. Layout is empty for
// legacy preferences and is normalized by the display policy on use.
type DisplayPreference struct {
	Layout string `json:"layout,omitempty"`
	Output string `json:"output,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

const (
	DefaultInferenceAPIPort = 8766
	DefaultInferenceAlias   = "cloudless"
)

// InferenceContract is the one client-facing OpenAI-compatible identity for
// every managed engine and recipe. Backend ports and native model names remain
// private implementation details behind the Cloudless gateway.
type InferenceContract struct {
	Port       int    `json:"port,omitempty"`
	ModelAlias string `json:"modelAlias,omitempty"`
}

// Normalized fills legacy/empty values with the stable Cloudless defaults.
func (c InferenceContract) Normalized() InferenceContract {
	if c.Port == 0 {
		c.Port = DefaultInferenceAPIPort
	}
	if strings.TrimSpace(c.ModelAlias) == "" {
		c.ModelAlias = DefaultInferenceAlias
	}
	c.ModelAlias = strings.TrimSpace(c.ModelAlias)
	return c
}

// CustomEngine is a locally-built, OpenAI-compatible inference image registered
// by an advanced user. Base selects the signed Cloudless launch contract whose
// command, volumes, ports and safety defaults the custom image inherits.
type CustomEngine struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Image            string `json:"image"`
	ResolvedImage    string `json:"resolvedImage,omitempty"`
	ImageDigest      string `json:"imageDigest,omitempty"`
	Architecture     string `json:"architecture,omitempty"`
	Base             string `json:"base"`
	CommandMode      string `json:"commandMode,omitempty"`
	ProfileVersion   int    `json:"profileVersion,omitempty"`
	ContractVersion  string `json:"contractVersion,omitempty"`
	ValidationStatus string `json:"validationStatus,omitempty"` // registered | validating | compatible | failed
	LastValidated    string `json:"lastValidated,omitempty"`
	LastError        string `json:"lastError,omitempty"`
	Created          string `json:"created"`
}

// ModelPromotion records the durable bootstrap-to-target model transition. It
// intentionally lives in the install state (rather than only in process memory)
// so an interrupted download or verification can be resumed safely after boot.
type ModelPromotion struct {
	Phase       string `json:"phase,omitempty"` // idle | waiting-hardware | bootstrap | downloading | switching | verifying | ready | rollback | canceled | error
	Bootstrap   string `json:"bootstrap,omitempty"`
	Target      string `json:"target,omitempty"`
	Active      string `json:"active,omitempty"`
	Rollback    string `json:"rollback,omitempty"`
	Message     string `json:"message,omitempty"`
	BytesDone   int64  `json:"bytesDone,omitempty"`
	BytesTotal  int64  `json:"bytesTotal,omitempty"`
	Started     string `json:"started,omitempty"`
	Updated     string `json:"updated,omitempty"`
	Error       string `json:"error,omitempty"`
	TargetReady bool   `json:"targetReady,omitempty"`
	Attempts    int    `json:"attempts,omitempty"`
}

// ModelDownload is the durable portion of a Hugging Face cache operation.
// Secrets and process-local cancellation handles are deliberately excluded.
// Partial blobs remain in the shared cache and snapshot_download resumes them.
type ModelDownload struct {
	ModelID    string `json:"modelId"`
	Revision   string `json:"revision,omitempty"`
	Phase      string `json:"phase"`
	Message    string `json:"message,omitempty"`
	BytesDone  int64  `json:"bytesDone,omitempty"`
	BytesTotal int64  `json:"bytesTotal,omitempty"`
	Started    string `json:"started,omitempty"`
	Updated    string `json:"updated,omitempty"`
	Error      string `json:"error,omitempty"`
}

// InferenceOperation journals a user-visible runtime transition. The desired
// InferenceRuntime is already stored in State; Previous preserves a verified
// rollback target when launch validation fails.
type InferenceOperation struct {
	ID           string                  `json:"id"`
	Action       string                  `json:"action"` // load | unload | switch | restart | model | abort | cluster-fallback
	TargetEngine string                  `json:"targetEngine,omitempty"`
	TargetModel  string                  `json:"targetModel,omitempty"`
	TargetMode   string                  `json:"targetMode,omitempty"`
	Previous     InferenceRuntime        `json:"previous"`
	Phase        string                  `json:"phase"`
	Message      string                  `json:"message,omitempty"`
	Percent      int                     `json:"percent,omitempty"`
	ItemsDone    int                     `json:"itemsDone,omitempty"`
	ItemsTotal   int                     `json:"itemsTotal,omitempty"`
	BytesDone    int64                   `json:"bytesDone,omitempty"`
	BytesTotal   int64                   `json:"bytesTotal,omitempty"`
	Started      string                  `json:"started,omitempty"`
	Updated      string                  `json:"updated,omitempty"`
	Error        string                  `json:"error,omitempty"`
	Nodes        []InferenceNodeProgress `json:"nodes,omitempty"`
}

type InferenceNodeProgress struct {
	Node       string `json:"node"`
	Phase      string `json:"phase"`
	Message    string `json:"message,omitempty"`
	BytesDone  int64  `json:"bytesDone,omitempty"`
	BytesTotal int64  `json:"bytesTotal,omitempty"`
	Percent    int    `json:"percent,omitempty"`
	ETASecs    int64  `json:"etaSeconds,omitempty"`
}

// RuntimeRestartOffer remembers the exact inference runtime that CloudlessOS
// believed was active when the daemon restarted. Automatic records whether
// this is an informational notice or an explicit restart decision.
type RuntimeRestartOffer struct {
	InstanceID string           `json:"instanceId"`
	Runtime    InferenceRuntime `json:"runtime"`
	Created    string           `json:"created"`
	Automatic  bool             `json:"automatic,omitempty"`
}

type ManagedEngineArtifact struct {
	EngineID         string `json:"engineId"`
	Image            string `json:"image"`
	DownloadedDigest string `json:"downloadedDigest,omitempty"`
	ActiveDigest     string `json:"activeDigest,omitempty"`
	Source           string `json:"source,omitempty"`
	Channel          string `json:"channel,omitempty"`
	Verified         bool   `json:"verified,omitempty"`
	Updated          string `json:"updated"`
}

// APIKey is a user-generated credential for the Cloudless Proxy (the OpenAI-compatible
// gateway). The full key is shown ONCE at creation; only its SHA-256 hash is stored.
type APIKey struct {
	ID               string `json:"id"`      // short opaque id (for revoke)
	Name             string `json:"name"`    // user label, e.g. "My website"
	Prefix           string `json:"prefix"`  // first chars, shown to identify the key (e.g. "sk-cloudless-ab12cd")
	Hash             string `json:"hash"`    // sha256(fullKey) hex — the secret is never stored
	Created          string `json:"created"` // RFC3339
	LastUsed         string `json:"lastUsed,omitempty"`
	Requests         int64  `json:"requests"`         // lifetime request count through the gateway
	Successes        int64  `json:"successes"`        // successful (< 400) gateway responses
	Failures         int64  `json:"failures"`         // failed (>= 400) gateway responses
	PromptTokens     int64  `json:"promptTokens"`     // input tokens reported by the engine
	CompletionTokens int64  `json:"completionTokens"` // output tokens reported by the engine
	Scope            string `json:"scope,omitempty"`  // model | agent | both; empty legacy keys are model-only
	ModelRequests    int64  `json:"modelRequests,omitempty"`
	AgentRequests    int64  `json:"agentRequests,omitempty"`
}

const (
	APIKeyScopeModel = "model"
	APIKeyScopeAgent = "agent"
	APIKeyScopeBoth  = "both"
)

// NormalizeAPIKeyScope keeps persisted and request-provided values on the
// intentionally small permission surface. Empty legacy keys remain model-only.
func NormalizeAPIKeyScope(scope string) string {
	switch scope {
	case APIKeyScopeAgent, APIKeyScopeBoth:
		return scope
	default:
		return APIKeyScopeModel
	}
}

// State is the persisted state.
type State struct {
	FirstSeen               string                           `json:"firstSeen"`                         // RFC3339; when the daemon first initialized this store
	Onboarded               bool                             `json:"onboarded"`                         // user has completed first-run onboarding
	FirstLaunchSetup        string                           `json:"firstLaunchSetup,omitempty"`        // pending | install | manual; empty is a legacy installation
	RecipeLaunchGuidanceAck bool                             `json:"recipeLaunchGuidanceAck,omitempty"` // user acknowledged the one-time post-recipe connection guide
	Engine                  string                           `json:"engine,omitempty"`                  // selected inference engine ("" = default)
	Model                   string                           `json:"model,omitempty"`                   // selected model ("" = catalog default)
	EngineUnloaded          bool                             `json:"engineUnloaded,omitempty"`          // selected model stays cached but no inference engine holds accelerator memory
	AutomaticRuntimeRestart bool                             `json:"automaticRuntimeRestart,omitempty"` // explicitly restart the last model or recipe after CloudlessOS starts
	ExecutionMode           string                           `json:"executionMode,omitempty"`           // local | cluster; empty is local
	LocalRecipeID           string                           `json:"localRecipeId,omitempty"`           // reviewed local recipe owning the active engine
	Pinned                  []string                         `json:"pinned,omitempty"`                  // app ids pinned to the dashboard "fast launch"
	PinnedSet               bool                             `json:"pinnedSet,omitempty"`               // user has customized pins (else use catalog default)
	LocalNet                bool                             `json:"localNet"`                          // serve apps on the local network (LAN)
	LocalNetSet             bool                             `json:"localNetSet,omitempty"`             // user has chosen (else default ON)
	Profile                 Profile                          `json:"profile"`                           // user-controlled profile
	APIKeys                 []APIKey                         `json:"apiKeys,omitempty"`                 // Cloudless Proxy credentials
	Display                 DisplayPreference                `json:"display,omitempty"`                 // preferred display output and mode
	InferenceAPI            InferenceContract                `json:"inferenceApi,omitempty"`            // stable client-facing API port and model alias
	CustomModels            map[string]models.Model          `json:"customModels,omitempty"`            // user-imported Hugging Face repositories
	ModelPromotion          ModelPromotion                   `json:"modelPromotion,omitempty"`          // verified bootstrap/full-model lifecycle
	ModelDownloads          map[string]ModelDownload         `json:"modelDownloads,omitempty"`          // resumable Hugging Face cache operations
	InferenceOperation      InferenceOperation               `json:"inferenceOperation,omitempty"`      // durable engine/model transition
	RuntimeRestartOffer     RuntimeRestartOffer              `json:"runtimeRestartOffer,omitempty"`     // one explicit resume decision after daemon restart
	ManagedEngineArtifacts  map[string]ManagedEngineArtifact `json:"managedEngineArtifacts,omitempty"`  // downloaded and activated runtime identity
	InstalledPacks          []string                         `json:"installedPacks,omitempty"`          // optional capability packs installed by Cloudless
	CustomEngines           []CustomEngine                   `json:"customEngines,omitempty"`           // user-built inference images
	ModelStorage            modelstorage.Config              `json:"modelStorage,omitempty"`            // local cache or validated shared NFS export

	// EngineCmds holds user-edited launch commands, keyed "engineID\x00modelID".
	// The value is the container command (args after the image) to use when that
	// model is launched on that engine, overriding the catalog default.
	EngineCmds map[string][]string `json:"engineCmds,omitempty"`
}

func (s *Store) ModelStorageConfig() modelstorage.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.ModelStorage.Normalized()
}

func (s *Store) SetModelStorageConfig(config modelstorage.Config) error {
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.st.ModelStorage
	s.st.ModelStorage = config
	if err := s.save(); err != nil {
		s.st.ModelStorage = previous
		return err
	}
	return nil
}

const (
	FirstLaunchSetupPending = "pending"
	FirstLaunchSetupInstall = "install"
	FirstLaunchSetupManual  = "manual"
)

// InferenceRuntime is the subset of state that must change atomically when an
// engine is promoted or rolled back. Keeping it as one transaction prevents a
// crash from persisting a new model with an old engine or a half-set recipe.
type InferenceRuntime struct {
	Engine         string `json:"engine,omitempty"`
	Model          string `json:"model,omitempty"`
	EngineUnloaded bool   `json:"engineUnloaded,omitempty"`
	ExecutionMode  string `json:"executionMode,omitempty"`
	LocalRecipeID  string `json:"localRecipeId,omitempty"`
}

func (s State) InferenceRuntime() InferenceRuntime {
	return InferenceRuntime{Engine: s.Engine, Model: s.Model, EngineUnloaded: s.EngineUnloaded, ExecutionMode: s.ExecutionMode, LocalRecipeID: s.LocalRecipeID}
}

// RuntimeRestartAutomatic reports the explicit opt-in for unattended model or
// recipe recovery. The zero value is deliberately false.
func (s *Store) RuntimeRestartAutomatic() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.AutomaticRuntimeRestart
}

// SetRuntimeRestartAutomatic persists the unattended restart preference.
func (s *Store) SetRuntimeRestartAutomatic(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.st.AutomaticRuntimeRestart
	s.st.AutomaticRuntimeRestart = enabled
	if err := s.save(); err != nil {
		s.st.AutomaticRuntimeRestart = previous
		return err
	}
	return nil
}

// PrepareRuntimeRestartOffer snapshots a previously active runtime once for a
// new daemon instance. An unloaded runtime was intentionally stopped and must
// never produce a restart prompt.
func (s *Store) PrepareRuntimeRestartOffer(instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return nil
	}
	if s.st.EngineUnloaded || s.st.InferenceOperation.ID != "" {
		return nil
	}
	runtime := s.st.InferenceRuntime()
	previousOffer, previousUnloaded := s.st.RuntimeRestartOffer, s.st.EngineUnloaded
	s.st.RuntimeRestartOffer = RuntimeRestartOffer{
		InstanceID: instanceID, Runtime: runtime, Created: time.Now().UTC().Format(time.RFC3339),
		Automatic: s.st.AutomaticRuntimeRestart,
	}
	if !s.st.AutomaticRuntimeRestart {
		// Prevent provisioners and Docker recovery from treating the remembered
		// runtime as approved. The offer retains the exact launch identity.
		s.st.EngineUnloaded = true
	}
	if err := s.save(); err != nil {
		s.st.RuntimeRestartOffer, s.st.EngineUnloaded = previousOffer, previousUnloaded
		return err
	}
	return nil
}

// ClearRuntimeRestartOffer acknowledges only the currently presented offer so
// a stale browser cannot clear a newer restart decision after another reboot.
func (s *Store) ClearRuntimeRestartOffer(instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.RuntimeRestartOffer.InstanceID == "" || s.st.RuntimeRestartOffer.InstanceID != strings.TrimSpace(instanceID) {
		return nil
	}
	previous := s.st.RuntimeRestartOffer
	s.st.RuntimeRestartOffer = RuntimeRestartOffer{}
	if err := s.save(); err != nil {
		s.st.RuntimeRestartOffer = previous
		return err
	}
	return nil
}

// CustomEngineList returns a detached snapshot of registered local builds.
func (s *Store) CustomEngineList() []CustomEngine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CustomEngine(nil), s.st.CustomEngines...)
}

// UpsertCustomEngine persists a validated custom engine definition.
func (s *Store) UpsertCustomEngine(def CustomEngine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.CustomEngines {
		if s.st.CustomEngines[i].ID == def.ID {
			s.st.CustomEngines[i] = def
			return s.save()
		}
	}
	s.st.CustomEngines = append(s.st.CustomEngines, def)
	return s.save()
}

// SetCustomEngineValidation records the result of the runtime contract probe
// without changing the immutable image/profile identity.
func (s *Store) SetCustomEngineValidation(id, status, validationError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.CustomEngines {
		if s.st.CustomEngines[i].ID != id {
			continue
		}
		s.st.CustomEngines[i].ValidationStatus = status
		s.st.CustomEngines[i].LastError = validationError
		if status == "compatible" || status == "failed" {
			s.st.CustomEngines[i].LastValidated = now()
		}
		return s.save()
	}
	return fmt.Errorf("unknown custom engine %q", id)
}

// DeleteCustomEngine removes a registered build without deleting its Docker
// image, source checkout, model cache, or any other user-owned artifact.
func (s *Store) DeleteCustomEngine(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.st.CustomEngines[:0]
	for _, def := range s.st.CustomEngines {
		if def.ID != id {
			next = append(next, def)
		}
	}
	s.st.CustomEngines = next
	return s.save()
}

// Store is a file-backed state store, safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	path     string
	st       State
	persist  bool
	firstRun bool
	dirty    bool // usage counters changed in memory, awaiting a lazy flush
}

// DefaultDir resolves the per-user state directory:
// CLOUDLESS_STATE_DIR, else $XDG_STATE_HOME/cloudless, else ~/.local/state/cloudless.
// Point CLOUDLESS_STATE_DIR at a system path (e.g. /var/lib/cloudless) for an
// install-wide flag instead of per-user.
func DefaultDir() string {
	if d := os.Getenv("CLOUDLESS_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "cloudless")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "cloudless")
	}
	return filepath.Join(os.TempDir(), "cloudless")
}

// Open loads dir/state.json. If the file is absent, this is treated as a first
// run: FirstSeen is stamped and the file is written so subsequent launches are
// recognized. If dir can't be created, returns an in-memory store (persistence
// disabled) plus the error.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "state.json"), persist: true}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.persist = false
		s.firstRun = true
		s.st = State{FirstSeen: now(), FirstLaunchSetup: FirstLaunchSetupPending, EngineUnloaded: true}
		return s, err
	}

	data, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if json.Unmarshal(data, &s.st) == nil {
			return s, nil
		}
		// corrupt file: fall through and re-initialize
	case !os.IsNotExist(err):
		return s, err
	}

	s.firstRun = true
	s.st = State{FirstSeen: now(), FirstLaunchSetup: FirstLaunchSetupPending, EngineUnloaded: true}
	_ = s.save() // best-effort; subsequent launches will read this back
	return s, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// save atomically writes the state file (temp + rename).
func (s *Store) save() error {
	if !s.persist {
		return nil
	}
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Get returns a copy of the current state.
func (s *Store) Get() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// SetOnboarded records onboarding completion and persists.
func (s *Store) SetOnboarded(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Onboarded = v
	return s.save()
}

// AcknowledgeRecipeLaunchGuidance permanently dismisses the connection help
// offered after the first successful recipe launch.
func (s *Store) AcknowledgeRecipeLaunchGuidance() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.RecipeLaunchGuidanceAck = true
	return s.save()
}

// FirstLaunchSetup returns the user's durable first-launch provisioning choice.
// State files created before this choice existed are treated as already installed
// so an upgrade does not unexpectedly stop their normal engine boot lifecycle.
func (s *Store) FirstLaunchSetup() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.FirstLaunchSetup == "" {
		return FirstLaunchSetupInstall
	}
	return s.st.FirstLaunchSetup
}

// FirstLaunchSetupRequired reports whether a new installation is still waiting
// for explicit permission before downloading or starting an inference runtime.
func (s *Store) FirstLaunchSetupRequired() bool {
	return s.FirstLaunchSetup() == FirstLaunchSetupPending
}

// StartupProvisioningEnabled determines whether daemon startup may reconcile the
// managed inference runtime. Manual first-launch setup stays inert until the user
// has explicitly selected an engine, model, or recipe.
func (s *Store) StartupProvisioningEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	choice := s.st.FirstLaunchSetup
	if choice == "" || choice == FirstLaunchSetupInstall {
		return true
	}
	return choice == FirstLaunchSetupManual && (s.st.Model != "" || s.st.Engine != "" || s.st.LocalRecipeID != "")
}

// SetFirstLaunchSetup persists an explicit setup decision. Selecting install
// permits the provisioner to start; selecting manual keeps inference unloaded.
func (s *Store) SetFirstLaunchSetup(choice string) error {
	if choice != FirstLaunchSetupInstall && choice != FirstLaunchSetupManual {
		return fmt.Errorf("invalid first-launch setup choice %q", choice)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.FirstLaunchSetup = choice
	s.st.EngineUnloaded = choice != FirstLaunchSetupInstall
	return s.save()
}

// DisplayPreference returns the persisted display mode.
func (s *Store) DisplayPreference() DisplayPreference {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Display
}

// SetDisplayPreference persists a display mode already validated against
// xrandr's connected-output mode list.
func (s *Store) SetDisplayPreference(pref DisplayPreference) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Display = pref
	return s.save()
}

// InferenceContract returns the normalized, install-wide API identity.
func (s *Store) InferenceContract() InferenceContract {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.InferenceAPI.Normalized()
}

// SetInferenceContract persists a contract already validated by the API layer.
func (s *Store) SetInferenceContract(contract InferenceContract) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.InferenceAPI = contract.Normalized()
	return s.save()
}

// SetEngine records the selected inference engine and persists.
func (s *Store) SetEngine(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Engine = id
	return s.save()
}

// SetEngineUnloaded persists whether the selected model should remain stopped.
// The zero value is intentionally loaded for backward compatibility.
func (s *Store) SetEngineUnloaded(unloaded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.EngineUnloaded = unloaded
	return s.save()
}

// SetLocalRecipe records the reviewed local recipe which owns the inference
// endpoint. Empty values clear recipe ownership and return provisioning to the
// regular Cloudless engine lifecycle.
func (s *Store) SetLocalRecipe(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.LocalRecipeID = id
	return s.save()
}

// CommitInferenceRuntime atomically persists every field that identifies the
// active inference runtime. Callers must not promote these fields one setter at
// a time.
func (s *Store) CommitInferenceRuntime(runtime InferenceRuntime) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.st.InferenceRuntime()
	s.st.Engine = runtime.Engine
	s.st.Model = runtime.Model
	s.st.EngineUnloaded = runtime.EngineUnloaded
	s.st.ExecutionMode = runtime.ExecutionMode
	s.st.LocalRecipeID = runtime.LocalRecipeID
	if err := s.save(); err != nil {
		s.st.Engine = previous.Engine
		s.st.Model = previous.Model
		s.st.EngineUnloaded = previous.EngineUnloaded
		s.st.ExecutionMode = previous.ExecutionMode
		s.st.LocalRecipeID = previous.LocalRecipeID
		return err
	}
	return nil
}

// SetModel records the selected model and persists.
func (s *Store) SetModel(model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Model = model
	return s.save()
}

// SetModelPromotion persists a complete promotion snapshot atomically. The
// caller owns phase transitions; the store stamps Updated for consistent UI and
// support-bundle timelines.
func (s *Store) SetModelPromotion(p ModelPromotion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Updated = now()
	if p.Started == "" && p.Phase != "" && p.Phase != "idle" {
		p.Started = p.Updated
	}
	s.st.ModelPromotion = p
	return s.save()
}

func (s *Store) ModelDownloads() []ModelDownload {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ModelDownload, 0, len(s.st.ModelDownloads))
	for _, download := range s.st.ModelDownloads {
		out = append(out, download)
	}
	return out
}

func (s *Store) SetModelDownload(download ModelDownload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.ModelDownloads == nil {
		s.st.ModelDownloads = make(map[string]ModelDownload)
	}
	download.Updated = now()
	if download.Started == "" {
		download.Started = download.Updated
	}
	s.st.ModelDownloads[download.ModelID] = download
	return s.save()
}

func (s *Store) RemoveModelDownload(modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.st.ModelDownloads, modelID)
	return s.save()
}

func (s *Store) BeginInferenceOperation(operation InferenceOperation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation.Updated = now()
	if operation.Started == "" {
		operation.Started = operation.Updated
	}
	s.st.InferenceOperation = operation
	return s.save()
}

// UpdateInferenceOperation ignores stale writers. This matters when Abort
// supersedes a launch: the canceled launch goroutine must not overwrite the
// newer abort journal after it observes context cancellation.
func (s *Store) UpdateInferenceOperation(operation InferenceOperation) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if operation.ID == "" || s.st.InferenceOperation.ID != operation.ID {
		return false, nil
	}
	operation.Started = s.st.InferenceOperation.Started
	operation.Previous = s.st.InferenceOperation.Previous
	operation.Action = s.st.InferenceOperation.Action
	operation.TargetEngine = s.st.InferenceOperation.TargetEngine
	operation.TargetModel = s.st.InferenceOperation.TargetModel
	operation.TargetMode = s.st.InferenceOperation.TargetMode
	operation.Updated = now()
	s.st.InferenceOperation = operation
	return true, s.save()
}

func (s *Store) ClearInferenceOperation(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || s.st.InferenceOperation.ID != id {
		return false, nil
	}
	s.st.InferenceOperation = InferenceOperation{}
	return true, s.save()
}

func (s *Store) ManagedEngineArtifact(engineID string) (ManagedEngineArtifact, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.st.ManagedEngineArtifacts[engineID]
	return artifact, ok
}

func (s *Store) SetManagedEngineArtifact(artifact ManagedEngineArtifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.ManagedEngineArtifacts == nil {
		s.st.ManagedEngineArtifacts = make(map[string]ManagedEngineArtifact)
	}
	artifact.Updated = now()
	s.st.ManagedEngineArtifacts[artifact.EngineID] = artifact
	return s.save()
}

// SetExecutionMode persists whether inference runs locally or across a healthy
// multi-Spark cluster. Unknown values safely fall back to local execution.
func (s *Store) SetExecutionMode(mode string) error {
	if mode != "cluster" {
		mode = "local"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.ExecutionMode = mode
	return s.save()
}

func (s *Store) Packs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.st.InstalledPacks...)
}

func (s *Store) SetPackInstalled(id string, installed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]string, 0, len(s.st.InstalledPacks)+1)
	found := false
	for _, existing := range s.st.InstalledPacks {
		if existing == id {
			found = true
			if !installed {
				continue
			}
		}
		next = append(next, existing)
	}
	if installed && !found {
		next = append(next, id)
	}
	s.st.InstalledPacks = next
	return s.save()
}

// CustomModels returns user-imported Hugging Face repositories. Model slices are
// copied so callers cannot mutate the store without taking its lock.
func (s *Store) CustomModels() map[string]models.Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]models.Model, len(s.st.CustomModels))
	for id, model := range s.st.CustomModels {
		model.Tags = append([]string(nil), model.Tags...)
		out[id] = model
	}
	return out
}

// UpsertCustomModel persists metadata for a repository imported from the Hub.
func (s *Store) UpsertCustomModel(model models.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.CustomModels == nil {
		s.st.CustomModels = map[string]models.Model{}
	}
	model.Tags = append([]string(nil), model.Tags...)
	s.st.CustomModels[model.ID] = model
	return s.save()
}

func cmdKey(engine, model string) string { return engine + "\x00" + model }

// EngineCmd returns the saved launch-command override for (engine, model), if any.
func (s *Store) EngineCmd(engine, model string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.st.EngineCmds[cmdKey(engine, model)]
	if !ok || len(a) == 0 {
		return nil, false
	}
	return append([]string(nil), a...), true
}

// SetEngineCmd saves a launch-command override for (engine, model) and persists.
func (s *Store) SetEngineCmd(engine, model string, args []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.EngineCmds == nil {
		s.st.EngineCmds = map[string][]string{}
	}
	s.st.EngineCmds[cmdKey(engine, model)] = append([]string(nil), args...)
	return s.save()
}

// ClearEngineCmd removes the override for (engine, model) (revert to default) and persists.
func (s *Store) ClearEngineCmd(engine, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.st.EngineCmds, cmdKey(engine, model))
	return s.save()
}

// Pins returns the pinned app ids and whether the user has customized them.
func (s *Store) Pins() (ids []string, customized bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.st.Pinned...), s.st.PinnedSet
}

// SetPins records the dashboard "fast launch" pins (marking them customized).
func (s *Store) SetPins(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Pinned = append([]string(nil), ids...)
	s.st.PinnedSet = true
	return s.save()
}

// LocalNetwork reports whether apps should be served on the local network.
// Defaults to ON until the user explicitly chooses (in the welcome tour or Settings).
func (s *Store) LocalNetwork() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.st.LocalNetSet {
		return true
	}
	return s.st.LocalNet
}

// SetLocalNetwork records the local-network preference.
func (s *Store) SetLocalNetwork(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.LocalNet = v
	s.st.LocalNetSet = true
	return s.save()
}

// Profile returns a copy of the user profile.
func (s *Store) Profile() Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Profile
}

// SetProfile records the user profile and persists.
func (s *Store) SetProfile(p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Profile = p
	return s.save()
}

// APIKeys returns a copy of the stored API keys (with hashes; callers should not
// expose Hash to clients).
func (s *Store) APIKeys() []APIKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]APIKey(nil), s.st.APIKeys...)
}

// AddAPIKey creates a new gateway key, returning the FULL secret (shown once) plus
// the stored record (hash only). The secret is never persisted in plaintext.
func (s *Store) AddAPIKey(name string, requestedScope ...string) (secret string, key APIKey, err error) {
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		return "", APIKey{}, err
	}
	idb := make([]byte, 6)
	if _, err = rand.Read(idb); err != nil {
		return "", APIKey{}, err
	}
	secret = "sk-cloudless-" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(secret))
	key = APIKey{
		ID:      hex.EncodeToString(idb),
		Name:    name,
		Prefix:  secret[:20], // "sk-cloudless-" + 7 hex chars
		Hash:    hex.EncodeToString(sum[:]),
		Created: now(),
		Scope:   APIKeyScopeModel,
	}
	if len(requestedScope) > 0 {
		key.Scope = NormalizeAPIKeyScope(requestedScope[0])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.APIKeys = append(s.st.APIKeys, key)
	return secret, key, s.save()
}

// DeleteAPIKey removes (revokes) a key by id.
func (s *Store) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.st.APIKeys[:0]
	for _, k := range s.st.APIKeys {
		if k.ID != id {
			out = append(out, k)
		}
	}
	s.st.APIKeys = out
	return s.save()
}

// ValidateAPIKey reports whether `secret` matches a stored key (by hash compare),
// returning the matched key's id.
func (s *Store) ValidateAPIKey(secret string) (id string, ok bool) {
	if secret == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(secret))
	h := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.st.APIKeys {
		if k.Hash == h {
			return k.ID, true
		}
	}
	return "", false
}

// ValidateAPIKeyFor authenticates a key and enforces its model/agent scope.
func (s *Store) ValidateAPIKeyFor(secret, requiredScope string) (id string, ok bool) {
	if secret == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(secret))
	h := hex.EncodeToString(sum[:])
	requiredScope = NormalizeAPIKeyScope(requiredScope)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.st.APIKeys {
		if k.Hash != h {
			continue
		}
		scope := NormalizeAPIKeyScope(k.Scope)
		return k.ID, scope == APIKeyScopeBoth || scope == requiredScope
	}
	return "", false
}

// RecordAPIUsage updates one key's lifetime metrics IN MEMORY, marking the store
// dirty for a lazy flush (PersistIfDirty) so request proxying never writes to disk.
func (s *Store) RecordAPIUsage(id string, success bool, promptTokens, completionTokens int64) {
	s.RecordAPIUsageKind(id, APIKeyScopeModel, success, promptTokens, completionTokens)
}

// RecordAPIUsageKind records aggregate usage plus the selected gateway surface.
func (s *Store) RecordAPIUsageKind(id, kind string, success bool, promptTokens, completionTokens int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.APIKeys {
		if s.st.APIKeys[i].ID == id {
			k := &s.st.APIKeys[i]
			k.Requests++
			if NormalizeAPIKeyScope(kind) == APIKeyScopeAgent {
				k.AgentRequests++
			} else {
				k.ModelRequests++
			}
			if success {
				k.Successes++
			} else {
				k.Failures++
			}
			if promptTokens > 0 {
				k.PromptTokens += promptTokens
			}
			if completionTokens > 0 {
				k.CompletionTokens += completionTokens
			}
			k.LastUsed = now()
			s.dirty = true
			return
		}
	}
}

// PersistIfDirty flushes in-memory usage counters to disk if any changed.
func (s *Store) PersistIfDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		_ = s.save()
		s.dirty = false
	}
}

// FirstRun reports whether this process saw no prior state file at startup.
func (s *Store) FirstRun() bool { return s.firstRun }

// Path returns the state file path.
func (s *Store) Path() string { return s.path }

// Dir returns the state directory (where per-app config also lives).
func (s *Store) Dir() string { return filepath.Dir(s.path) }

// HuggingFaceToken returns the locally connected Hugging Face credential. The
// token deliberately lives outside state.json so ordinary state inspection and
// diagnostics can never expose it.
func (s *Store) HuggingFaceToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.Dir(), "huggingface-token"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// HuggingFaceTokenPath returns the protected token file used for read-only
// container secret mounts. Callers must still check HuggingFaceToken first so
// an absent account never creates an empty credential mount.
func (s *Store) HuggingFaceTokenPath() string {
	return filepath.Join(s.Dir(), "huggingface-token")
}

// SetHuggingFaceToken atomically stores a Hub credential with owner-only
// permissions. It is never placed in the normal JSON state store.
func (s *Store) SetHuggingFaceToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.persist {
		return nil
	}
	path := filepath.Join(s.Dir(), "huggingface-token")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o600)
}

// ClearHuggingFaceToken disconnects the local Hugging Face account.
func (s *Store) ClearHuggingFaceToken() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(filepath.Join(s.Dir(), "huggingface-token"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
