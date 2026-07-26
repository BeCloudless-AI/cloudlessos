// Package state is a small JSON-file-backed store for per-user/per-install daemon
// state — currently just first-run / onboarding status. It is the source of truth
// for "has this CloudlessOS install (user account) been set up before?".
package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/models"
)

// Profile is the user-controlled profile for this CloudlessOS install.
type Profile struct {
	Name   string `json:"name,omitempty"`   // display name (greeting / personalization)
	Region string `json:"region,omitempty"` // ISO-3166 alpha-2 override, e.g. "FR"; "" = auto-detect
}

// DisplayPreference is the validated X11 output mode CloudlessOS should restore
// when its kiosk session starts.
type DisplayPreference struct {
	Output string `json:"output,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
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
	FirstSeen      string                  `json:"firstSeen"`                // RFC3339; when the daemon first initialized this store
	Onboarded      bool                    `json:"onboarded"`                // user has completed first-run onboarding
	Engine         string                  `json:"engine,omitempty"`         // selected inference engine ("" = default)
	Model          string                  `json:"model,omitempty"`          // selected model ("" = catalog default)
	EngineUnloaded bool                    `json:"engineUnloaded,omitempty"` // selected model stays cached but no inference engine holds accelerator memory
	ExecutionMode  string                  `json:"executionMode,omitempty"`  // local | cluster; empty is local
	Pinned         []string                `json:"pinned,omitempty"`         // app ids pinned to the dashboard "fast launch"
	PinnedSet      bool                    `json:"pinnedSet,omitempty"`      // user has customized pins (else use catalog default)
	LocalNet       bool                    `json:"localNet"`                 // serve apps on the local network (LAN)
	LocalNetSet    bool                    `json:"localNetSet,omitempty"`    // user has chosen (else default ON)
	Profile        Profile                 `json:"profile"`                  // user-controlled profile
	APIKeys        []APIKey                `json:"apiKeys,omitempty"`        // Cloudless Proxy credentials
	Display        DisplayPreference       `json:"display,omitempty"`        // preferred display output and mode
	CustomModels   map[string]models.Model `json:"customModels,omitempty"`   // user-imported Hugging Face repositories

	// EngineCmds holds user-edited launch commands, keyed "engineID\x00modelID".
	// The value is the container command (args after the image) to use when that
	// model is launched on that engine, overriding the catalog default.
	EngineCmds map[string][]string `json:"engineCmds,omitempty"`
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
		s.st = State{FirstSeen: now()}
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
	s.st = State{FirstSeen: now()}
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

// SetModel records the selected model and persists.
func (s *Store) SetModel(model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Model = model
	return s.save()
}

// SetExecutionMode persists whether inference runs locally or across a healthy
// two-Spark cluster. Unknown values safely fall back to local execution.
func (s *Store) SetExecutionMode(mode string) error {
	if mode != "cluster" {
		mode = "local"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.ExecutionMode = mode
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
