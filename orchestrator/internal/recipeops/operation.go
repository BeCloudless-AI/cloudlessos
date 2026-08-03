// Package recipeops defines the durable lifecycle and ownership model for
// Cloudless recipe operations.
package recipeops

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

const documentVersion = 1

var ErrPreflightRequired = errors.New("run requires a runnable Check result for this exact recipe revision")
var ErrRecipeTombstoned = errors.New("recipe is pending deletion and cannot start new work")

type MutationConflict struct {
	Operation Operation
}

func (e *MutationConflict) Error() string {
	return fmt.Sprintf("recipe operation %s (%s/%s) still references this recipe", e.Operation.ID, e.Operation.Kind, e.Operation.Phase)
}

// Kind identifies the user-visible operation that owns a lifecycle record.
type Kind string

const (
	KindCheck Kind = "check"
	KindRun   Kind = "run"
	KindStop  Kind = "stop"
)

// Phase is a durable recipe lifecycle state. Transitions are intentionally
// stricter than progress messages: invalid transitions indicate a programming
// or recovery error and must never be silently persisted.
type Phase string

const (
	PhaseChecking   Phase = "checking"
	PhasePreparing  Phase = "preparing"
	PhasePrepared   Phase = "prepared"
	PhaseSwitching  Phase = "switching"
	PhaseStarting   Phase = "starting"
	PhaseVerifying  Phase = "verifying"
	PhaseActive     Phase = "active"
	PhaseStopping   Phase = "stopping"
	PhaseStopped    Phase = "stopped"
	PhaseFailed     Phase = "failed"
	PhaseAborted    Phase = "aborted"
	PhaseRecovering Phase = "recovering"
)

var transitions = map[Phase]map[Phase]struct{}{
	"": {
		PhaseChecking: {}, PhasePreparing: {}, PhaseStopping: {}, PhaseRecovering: {},
	},
	PhaseChecking: {
		PhasePrepared: {}, PhaseFailed: {}, PhaseAborted: {}, PhaseRecovering: {},
	},
	PhasePreparing: {
		PhasePrepared: {}, PhaseStopping: {}, PhaseFailed: {}, PhaseAborted: {}, PhaseRecovering: {},
	},
	PhasePrepared: {
		PhaseSwitching: {}, PhaseStopping: {}, PhaseFailed: {}, PhaseAborted: {}, PhaseRecovering: {},
	},
	PhaseSwitching: {
		PhaseStarting: {}, PhaseStopping: {}, PhaseFailed: {}, PhaseAborted: {}, PhaseRecovering: {},
	},
	PhaseStarting: {
		PhaseVerifying: {}, PhaseStopping: {}, PhaseFailed: {}, PhaseAborted: {}, PhaseRecovering: {},
	},
	PhaseVerifying: {
		PhaseActive: {}, PhaseStopping: {}, PhaseFailed: {}, PhaseAborted: {}, PhaseRecovering: {},
	},
	PhaseActive: {
		PhaseStopping: {}, PhaseRecovering: {},
	},
	PhaseStopping: {
		PhaseStopped: {}, PhaseFailed: {}, PhaseAborted: {}, PhaseRecovering: {},
	},
	PhaseRecovering: {
		PhaseChecking: {}, PhasePreparing: {}, PhasePrepared: {}, PhaseSwitching: {}, PhaseStarting: {},
		PhaseVerifying: {}, PhaseActive: {}, PhaseStopping: {}, PhaseStopped: {},
		PhaseFailed: {}, PhaseAborted: {},
	},
	PhaseStopped: {PhaseRecovering: {}},
	PhaseFailed:  {PhaseRecovering: {}},
	PhaseAborted: {PhaseRecovering: {}},
}

// ValidTransition reports whether a phase change is part of the lifecycle.
// Re-applying the current phase is allowed so recovery and retries are
// idempotent.
func ValidTransition(from, to Phase) bool {
	if from == to && from != "" {
		return true
	}
	_, ok := transitions[from][to]
	return ok
}

// Resource identifies an operating-system or container resource owned by an
// operation. ID must be the real Docker name/ID, process identity, port,
// checkout path or artifact identity used for cleanup and reconciliation.
type Resource struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Node    string `json:"node,omitempty"`
	Locator string `json:"locator,omitempty"`
}

// RequiresCleanup reports whether a resource is an ephemeral runtime claim
// that must be absent before an interrupted operation is reconciled. Images,
// model caches and normal checkouts are retained artifacts, not leaks.
func (r Resource) RequiresCleanup() bool {
	switch r.Kind {
	case "container", "container-set", "compose-project", "private-port", "stable-port", "rendezvous-port", "process-set", "staging-checkout":
		return true
	default:
		return false
	}
}

// CheckStatus is the machine-readable outcome of one read-only preflight
// assertion. Unknown is deliberately distinct from pass: unavailable evidence
// must never make a recipe appear launchable.
type CheckStatus string

const (
	CheckPass    CheckStatus = "pass"
	CheckFail    CheckStatus = "fail"
	CheckWarning CheckStatus = "warning"
	CheckUnknown CheckStatus = "unknown"
)

// CheckResult is durable evidence collected during preflight. Values contains
// display-safe measurements such as a digest, architecture, port or byte count;
// credentials and raw command environments must never be stored here.
type CheckResult struct {
	ID         string            `json:"id"`
	Status     CheckStatus       `json:"status"`
	Summary    string            `json:"summary"`
	Detail     string            `json:"detail,omitempty"`
	ObservedAt string            `json:"observedAt"`
	Values     map[string]string `json:"values,omitempty"`
}

type Progress struct {
	Stage          string              `json:"stage"`
	Message        string              `json:"message"`
	OverallPercent int                 `json:"overallPercent"`
	PhasePercent   int                 `json:"phasePercent"`
	Completed      int                 `json:"completed,omitempty"`
	Total          int                 `json:"total,omitempty"`
	BytesDone      int64               `json:"bytesDone,omitempty"`
	BytesTotal     int64               `json:"bytesTotal,omitempty"`
	ElapsedSeconds int64               `json:"elapsedSeconds,omitempty"`
	ETASeconds     int64               `json:"etaSeconds,omitempty"`
	BytesPerSecond int64               `json:"bytesPerSecond,omitempty"`
	StalledSeconds int64               `json:"stalledSeconds,omitempty"`
	Components     []ProgressComponent `json:"components,omitempty"`
	UpdatedAt      string              `json:"updatedAt"`
}

type ProgressComponent struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	BytesDone      int64  `json:"bytesDone,omitempty"`
	BytesTotal     int64  `json:"bytesTotal,omitempty"`
	BytesPerSecond int64  `json:"bytesPerSecond,omitempty"`
	ETASeconds     int64  `json:"etaSeconds,omitempty"`
}

type Checkpoint struct {
	Phase      Phase  `json:"phase"`
	Sequence   uint64 `json:"sequence"`
	ObservedAt string `json:"observedAt"`
}

// PreviousRuntime is the durable rollback target captured atomically with run
// admission, before preparation can affect the current engine.
type PreviousRuntime struct {
	Engine         string `json:"engine,omitempty"`
	Model          string `json:"model,omitempty"`
	EngineUnloaded bool   `json:"engineUnloaded,omitempty"`
	ExecutionMode  string `json:"executionMode,omitempty"`
	LocalRecipeID  string `json:"localRecipeId,omitempty"`
}

// PreflightArtifact binds a complete set of check evidence to every mutable
// input that can make it stale. EvidenceHash is calculated over the artifact
// with that field empty and detects on-disk modification or partial writes.
type PreflightArtifact struct {
	RecipeID            string        `json:"recipeId"`
	RecipeRevision      string        `json:"recipeRevision"`
	SourceRevision      string        `json:"sourceRevision,omitempty"`
	ImageDigest         string        `json:"imageDigest,omitempty"`
	PlatformFingerprint string        `json:"platformFingerprint"`
	ClusterFingerprint  string        `json:"clusterFingerprint"`
	Runnable            bool          `json:"runnable"`
	Launchable          bool          `json:"launchable"`
	CreatedAt           string        `json:"createdAt"`
	Checks              []CheckResult `json:"checks"`
	EvidenceHash        string        `json:"evidenceHash"`
}

// Operation is the durable lifecycle record for one recipe action.
type Operation struct {
	ID                     string              `json:"id"`
	Kind                   Kind                `json:"kind"`
	RecipeID               string              `json:"recipeId"`
	RecipeRevision         string              `json:"recipeRevision"`
	RecipeSnapshot         localrecipes.Recipe `json:"recipeSnapshot,omitempty"`
	ResolvedSourceRevision string              `json:"resolvedSourceRevision,omitempty"`
	PreparedImageReference string              `json:"preparedImageReference,omitempty"`
	PreparedImageDigest    string              `json:"preparedImageDigest,omitempty"`
	Phase                  Phase               `json:"phase"`
	Sequence               uint64              `json:"sequence"`
	CreatedAt              string              `json:"createdAt"`
	UpdatedAt              string              `json:"updatedAt"`
	Error                  string              `json:"error,omitempty"`
	Resources              []Resource          `json:"resources,omitempty"`
	Checks                 []CheckResult       `json:"checks,omitempty"`
	Preflight              *PreflightArtifact  `json:"preflight,omitempty"`
	Progress               *Progress           `json:"progress,omitempty"`
	Checkpoints            []Checkpoint        `json:"checkpoints,omitempty"`
	PreviousRuntime        *PreviousRuntime    `json:"previousRuntime,omitempty"`
}

// Terminal reports whether no automatic work remains for this operation.
func (o Operation) Terminal() bool {
	if o.Kind == KindCheck && o.Phase == PhasePrepared {
		return true
	}
	switch o.Phase {
	case PhaseActive, PhaseStopped, PhaseFailed, PhaseAborted:
		return true
	default:
		return false
	}
}

// NeedsRecovery reports whether daemon restart interrupted this lifecycle or
// whether an active runtime must be reconciled with the real machine state.
func (o Operation) NeedsRecovery() bool {
	for _, resource := range o.Resources {
		if resource.RequiresCleanup() {
			return true
		}
	}
	if o.Kind == KindCheck && o.Phase == PhasePrepared {
		return false
	}
	switch o.Phase {
	case PhaseStopped, PhaseFailed, PhaseAborted:
		return false
	default:
		return true
	}
}

// RecipeRevision returns a stable identity for the executable recipe profile.
// Import/update timestamps and compatibility-only top-level fields are excluded
// by hashing the canonical Draft representation.
func RecipeRevision(recipe localrecipes.Recipe) (string, error) {
	return recipeRevisionForDraft(localrecipes.DraftFromRecipe(recipe))
}

func snapshotRecipeRevision(recipe localrecipes.Recipe) (string, error) {
	return recipeRevisionForDraft(localrecipes.DraftFromSnapshot(recipe))
}

func recipeRevisionForDraft(draft localrecipes.Draft) (string, error) {
	payload, err := json.Marshal(draft)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type document struct {
	Version    int                  `json:"version"`
	Operations map[string]Operation `json:"operations"`
	Tombstones map[string]string    `json:"tombstones,omitempty"`
}

type rawDocument struct {
	Operations map[string]json.RawMessage `json:"operations"`
}

type rawPersistedOperation struct {
	RecipeSnapshot json.RawMessage `json:"recipeSnapshot"`
}

// rawRecipeDraft preserves the JSON field set that existed when a historical
// operation revision was created. Decoding into today's Recipe structs can add
// zero-value fields introduced by later schema versions and change the hash of
// an otherwise authentic immutable snapshot.
type rawRecipeDraft struct {
	Name        json.RawMessage `json:"name"`
	Description json.RawMessage `json:"description"`
	Platform    json.RawMessage `json:"platform"`
	Source      json.RawMessage `json:"source"`
	Engine      json.RawMessage `json:"engine"`
	Model       json.RawMessage `json:"model"`
	Distributed json.RawMessage `json:"distributed"`
	Runtime     json.RawMessage `json:"runtime"`
	Health      json.RawMessage `json:"health"`
}

func rawPersistedSnapshotRevision(raw json.RawMessage) (string, error) {
	var operation rawPersistedOperation
	if err := json.Unmarshal(raw, &operation); err != nil || len(operation.RecipeSnapshot) == 0 || string(operation.RecipeSnapshot) == "null" {
		return "", errors.New("persisted operation has no recipe snapshot")
	}
	var draft rawRecipeDraft
	if err := json.Unmarshal(operation.RecipeSnapshot, &draft); err != nil {
		return "", fmt.Errorf("decode persisted recipe snapshot: %w", err)
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return "", fmt.Errorf("encode persisted recipe snapshot: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Store persists lifecycle records independently from UI jobs. Jobs may be
// pruned or lost during a service restart; operation ownership may not.
type Store struct {
	mu                  sync.Mutex
	path                string
	doc                 document
	historicalSnapshots map[string]json.RawMessage
}

// Open loads the operation store below the Cloudless state directory.
func Open(stateDir string) (*Store, error) {
	s := &Store{path: filepath.Join(stateDir, "recipe-operations", "index.json"), historicalSnapshots: make(map[string]json.RawMessage)}
	s.doc = document{Version: documentVersion, Operations: make(map[string]Operation), Tombstones: make(map[string]string)}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.doc); err != nil {
		return nil, fmt.Errorf("decode recipe operation store: %w", err)
	}
	var raw rawDocument
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode raw recipe operation store: %w", err)
	}
	if s.doc.Version != documentVersion {
		return nil, fmt.Errorf("unsupported recipe operation store version %d", s.doc.Version)
	}
	if s.doc.Operations == nil {
		s.doc.Operations = make(map[string]Operation)
	}
	if s.doc.Tombstones == nil {
		s.doc.Tombstones = make(map[string]string)
	}
	for id, operation := range s.doc.Operations {
		authenticHistoricalSnapshot := false
		if rawOperation, ok := raw.Operations[id]; ok {
			revision, err := rawPersistedSnapshotRevision(rawOperation)
			authenticHistoricalSnapshot = err == nil && revision == operation.RecipeRevision
			if authenticHistoricalSnapshot && !validOperation(operation) {
				var persisted rawPersistedOperation
				if json.Unmarshal(rawOperation, &persisted) == nil && len(persisted.RecipeSnapshot) > 0 {
					s.historicalSnapshots[id] = append(json.RawMessage(nil), persisted.RecipeSnapshot...)
				}
			}
		}
		if operation.ID != id || !validOperationWithHistoricalSnapshot(operation, authenticHistoricalSnapshot) {
			return nil, fmt.Errorf("invalid persisted recipe operation %q", id)
		}
	}
	return s, nil
}

// Begin creates and persists a new operation with immutable recipe ownership.
func (s *Store) Begin(kind Kind, recipe localrecipes.Recipe, initial Phase) (Operation, error) {
	return s.begin(kind, recipe, initial, false, false)
}

// BeginUnique atomically admits an operation only when no conflicting durable
// operation exists. This closes the list-then-create race across simultaneous
// API requests and continues to work after in-memory jobs are lost.
func (s *Store) BeginUnique(kind Kind, recipe localrecipes.Recipe, initial Phase) (Operation, error) {
	return s.begin(kind, recipe, initial, true, false)
}

// BeginRunValidated atomically admits a run and binds it to the newest
// launchable preflight for the same immutable recipe revision. A run can never
// silently proceed using a historical check for different recipe inputs.
func (s *Store) BeginRunValidated(recipe localrecipes.Recipe) (Operation, error) {
	return s.BeginRunValidatedWithPrevious(recipe, PreviousRuntime{})
}

func (s *Store) BeginRunValidatedWithPrevious(recipe localrecipes.Recipe, previous PreviousRuntime) (Operation, error) {
	return s.beginWithPrevious(KindRun, recipe, PhasePreparing, true, true, &previous)
}

func (s *Store) begin(kind Kind, recipe localrecipes.Recipe, initial Phase, unique, requirePreflight bool) (Operation, error) {
	return s.beginWithPrevious(kind, recipe, initial, unique, requirePreflight, nil)
}

func (s *Store) beginWithPrevious(kind Kind, recipe localrecipes.Recipe, initial Phase, unique, requirePreflight bool, previous *PreviousRuntime) (Operation, error) {
	if !validKind(kind) || initialPhase(kind) != initial || !ValidTransition("", initial) {
		return Operation{}, fmt.Errorf("invalid recipe operation start %q/%q", kind, initial)
	}
	revision, err := RecipeRevision(recipe)
	if err != nil {
		return Operation{}, err
	}
	snapshot, err := cloneRecipe(recipe)
	if err != nil {
		return Operation{}, err
	}
	id, err := newID()
	if err != nil {
		return Operation{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	operation := Operation{
		ID: id, Kind: kind, RecipeID: recipe.ID, RecipeRevision: revision,
		RecipeSnapshot: snapshot, Phase: initial, Sequence: 1, CreatedAt: now, UpdatedAt: now,
		Checkpoints: []Checkpoint{{Phase: initial, Sequence: 1, ObservedAt: now}},
	}
	if previous != nil {
		copy := *previous
		operation.PreviousRuntime = &copy
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, tombstoned := s.doc.Tombstones[recipe.ID]; tombstoned {
		return Operation{}, ErrRecipeTombstoned
	}
	if unique {
		for _, existing := range s.doc.Operations {
			if operationBlocksAdmission(existing, kind) {
				return Operation{}, fmt.Errorf("recipe operation %s (%s/%s) is still active", existing.ID, existing.Kind, existing.Phase)
			}
		}
	}
	if requirePreflight {
		artifact, ok := latestLaunchablePreflight(s.doc.Operations, recipe.ID, revision)
		if !ok {
			return Operation{}, ErrPreflightRequired
		}
		operation.Preflight = &artifact
	}
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		delete(s.doc.Operations, id)
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// Transition validates and durably records a lifecycle transition.
func (s *Store) Transition(id string, next Phase, cause error) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	if !ValidTransition(operation.Phase, next) {
		return Operation{}, fmt.Errorf("invalid recipe lifecycle transition %q -> %q", operation.Phase, next)
	}
	previous := detachedOperation(operation)
	operation.Phase = next
	operation.Sequence++
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if previous.Phase != next {
		operation.Checkpoints = append(operation.Checkpoints, Checkpoint{Phase: next, Sequence: operation.Sequence, ObservedAt: operation.UpdatedAt})
	}
	operation.Error = ""
	if cause != nil {
		operation.Error = strings.TrimSpace(cause.Error())
	}
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// RecordProgress persists a monotonic operation-level view independently from
// the ephemeral SSE job. Phase-local byte percentage may move between stages;
// OverallPercent is never allowed to move backwards.
func (s *Store) RecordProgress(id string, progress Progress) (Operation, error) {
	progress.Stage = strings.TrimSpace(progress.Stage)
	progress.Message = strings.TrimSpace(progress.Message)
	if progress.Stage == "" || progress.Message == "" || progress.OverallPercent < 0 || progress.OverallPercent > 100 || progress.PhasePercent < 0 || progress.PhasePercent > 100 || progress.Completed < 0 || progress.Total < 0 || progress.BytesDone < 0 || progress.BytesTotal < 0 || progress.ElapsedSeconds < 0 || progress.ETASeconds < 0 {
		return Operation{}, errors.New("invalid recipe operation progress")
	}
	if progress.UpdatedAt == "" {
		progress.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else if _, err := time.Parse(time.RFC3339Nano, progress.UpdatedAt); err != nil {
		return Operation{}, errors.New("invalid recipe operation progress timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	if operation.Progress != nil && progress.OverallPercent < operation.Progress.OverallPercent {
		progress.OverallPercent = operation.Progress.OverallPercent
	}
	previous := detachedOperation(operation)
	operation.Progress = &progress
	operation.Sequence++
	operation.UpdatedAt = progress.UpdatedAt
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// BindResolvedSource records the immutable Git object actually fetched for an
// operation. A branch or tag may be convenient recipe input, but recovery and
// later launch checks must refer to the exact commit that passed preflight.
func (s *Store) BindResolvedSource(id, revision string) (Operation, error) {
	revision = strings.ToLower(strings.TrimSpace(revision))
	if !validGitObjectID(revision) {
		return Operation{}, errors.New("resolved recipe source revision must be a full Git object id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	if operation.ResolvedSourceRevision != "" && operation.ResolvedSourceRevision != revision {
		return Operation{}, errors.New("recipe operation source revision is already bound")
	}
	if operation.ResolvedSourceRevision == revision {
		return detachedOperation(operation), nil
	}
	previous := detachedOperation(operation)
	operation.ResolvedSourceRevision = revision
	operation.Sequence++
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// BindPreparedImage records the immutable runtime image that preparation and
// recovery must use. It is write-once: a mutable tag cannot silently resolve
// to different content after Cloudless has attested the operation.
func (s *Store) BindPreparedImage(id, reference, digest string) (Operation, error) {
	reference = strings.TrimSpace(reference)
	digest = strings.ToLower(strings.TrimSpace(digest))
	if reference == "" || len(reference) > 512 || strings.ContainsAny(reference, "\x00\r\n\t ") || !validSHA256Identity(digest) {
		return Operation{}, errors.New("prepared recipe image requires a safe immutable reference and SHA-256 digest")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	if operation.PreparedImageReference != "" && (operation.PreparedImageReference != reference || operation.PreparedImageDigest != digest) {
		return Operation{}, errors.New("prepared recipe image identity is already bound")
	}
	if operation.PreparedImageReference == reference && operation.PreparedImageDigest == digest {
		return detachedOperation(operation), nil
	}
	previous := detachedOperation(operation)
	operation.PreparedImageReference, operation.PreparedImageDigest = reference, digest
	operation.Sequence++
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// RecordCheck upserts durable preflight evidence by check ID. Re-running a
// bounded check replaces its earlier observation instead of creating an
// ambiguous history inside the current operation.
func (s *Store) RecordCheck(id string, result CheckResult) (Operation, error) {
	result.ID = strings.TrimSpace(result.ID)
	result.Summary = strings.TrimSpace(result.Summary)
	result.Detail = strings.TrimSpace(result.Detail)
	if result.ID == "" || result.Summary == "" || !validCheckStatus(result.Status) {
		return Operation{}, errors.New("recipe check id, status and summary are required")
	}
	if result.ObservedAt == "" {
		result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else if _, err := time.Parse(time.RFC3339Nano, result.ObservedAt); err != nil {
		return Operation{}, errors.New("recipe check observation time is invalid")
	}
	result.Values = cloneValues(result.Values)
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	previous := detachedOperation(operation)
	replaced := false
	for index := range operation.Checks {
		if operation.Checks[index].ID == result.ID {
			operation.Checks[index] = result
			replaced = true
			break
		}
	}
	if !replaced {
		operation.Checks = append(operation.Checks, result)
	}
	sort.Slice(operation.Checks, func(i, j int) bool { return operation.Checks[i].ID < operation.Checks[j].ID })
	operation.Sequence++
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// BindPreflight persists the immutable evidence bundle produced by Check.
func (s *Store) BindPreflight(id, imageDigest, platformFingerprint, clusterFingerprint string) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	if operation.Kind != KindCheck || operation.Phase != PhaseChecking {
		return Operation{}, errors.New("preflight can only be bound to an active check operation")
	}
	artifact, err := newPreflightArtifact(operation, imageDigest, platformFingerprint, clusterFingerprint)
	if err != nil {
		return Operation{}, err
	}
	previous := detachedOperation(operation)
	operation.Preflight = &artifact
	operation.Sequence++
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// Claim records a resource which must be cleaned or reconciled with its owning
// operation. Repeated claims are idempotent.
func (s *Store) Claim(id string, resource Resource) (Operation, error) {
	resource.Kind = strings.TrimSpace(resource.Kind)
	resource.ID = strings.TrimSpace(resource.ID)
	resource.Node = strings.TrimSpace(resource.Node)
	resource.Locator = strings.TrimSpace(resource.Locator)
	if resource.Kind == "" || resource.ID == "" {
		return Operation{}, errors.New("recipe resource kind and id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	for _, existing := range operation.Resources {
		if existing == resource {
			return detachedOperation(operation), nil
		}
	}
	previous := detachedOperation(operation)
	operation.Resources = append(operation.Resources, resource)
	sortResources(operation.Resources)
	operation.Sequence++
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// Release removes a resource after cleanup has been verified.
func (s *Store) Release(id string, resource Resource) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	if !ok {
		return Operation{}, os.ErrNotExist
	}
	next := make([]Resource, 0, len(operation.Resources))
	found := false
	for _, existing := range operation.Resources {
		if existing == resource {
			found = true
			continue
		}
		next = append(next, existing)
	}
	if !found {
		return detachedOperation(operation), nil
	}
	previous := detachedOperation(operation)
	operation.Resources = next
	operation.Sequence++
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.doc.Operations[id] = operation
	if err := s.saveLocked(); err != nil {
		s.doc.Operations[id] = previous
		return Operation{}, err
	}
	return detachedOperation(operation), nil
}

// Get returns a detached operation snapshot.
func (s *Store) Get(id string) (Operation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.doc.Operations[id]
	return detachedOperation(operation), ok
}

// List returns operations in creation order.
func (s *Store) List() []Operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	operations := make([]Operation, 0, len(s.doc.Operations))
	for _, operation := range s.doc.Operations {
		operations = append(operations, detachedOperation(operation))
	}
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].CreatedAt == operations[j].CreatedAt {
			return operations[i].ID < operations[j].ID
		}
		return operations[i].CreatedAt < operations[j].CreatedAt
	})
	return operations
}

// ActiveForRecipe returns the newest active run operation for a recipe. Stop
// and recovery commands use its runtime identity instead of inventing a new
// Compose project name which would leave the real containers untouched.
func (s *Store) ActiveForRecipe(recipeID string) (Operation, bool) {
	operations := s.List()
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.Kind == KindRun && operation.RecipeID == recipeID && operation.Phase == PhaseActive {
			return operation, true
		}
	}
	return Operation{}, false
}

// MutationBlocker returns the newest operation whose lifecycle or unreconciled
// resources still depend on a recipe. Completed read-only checks do not pin a
// recipe forever, while failed/aborted operations keep blocking if cleanup
// obligations remain in the journal.
func (s *Store) MutationBlocker(recipeID string) (Operation, bool) {
	operations := s.List()
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.RecipeID == recipeID && operationBlocksMutation(operation) {
			return operation, true
		}
	}
	return Operation{}, false
}

// ExecuteMutation serializes a recipe-store write with operation admission.
// The callback must only mutate the separate recipe store and must not call
// back into this Store. Holding this lock closes the final guard-then-write
// race against concurrent Run, Check and Stop requests.
func (s *Store) ExecuteMutation(recipeID string, mutate func() error) error {
	if strings.TrimSpace(recipeID) == "" || mutate == nil {
		return errors.New("recipe mutation requires an id and callback")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, tombstoned := s.doc.Tombstones[recipeID]; tombstoned {
		return ErrRecipeTombstoned
	}
	var blocker Operation
	found := false
	for _, operation := range s.doc.Operations {
		if operation.RecipeID != recipeID || !operationBlocksMutation(operation) {
			continue
		}
		if !found || operation.UpdatedAt > blocker.UpdatedAt || (operation.UpdatedAt == blocker.UpdatedAt && operation.ID > blocker.ID) {
			blocker, found = operation, true
		}
	}
	if found {
		return &MutationConflict{Operation: detachedOperation(blocker)}
	}
	return mutate()
}

// Tombstone serializes deletion with operation admission. Once this returns,
// no stale caller can begin new work for the recipe even if cleanup is still
// pending in the operation journal.
func (s *Store) Tombstone(recipeID string, mark func() error) error {
	if strings.TrimSpace(recipeID) == "" || mark == nil {
		return errors.New("recipe tombstone requires an id and callback")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.doc.Tombstones[recipeID]; exists {
		return mark()
	}
	s.doc.Tombstones[recipeID] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.saveLocked(); err != nil {
		delete(s.doc.Tombstones, recipeID)
		return err
	}
	if err := mark(); err != nil {
		delete(s.doc.Tombstones, recipeID)
		rollbackErr := s.saveLocked()
		return errors.Join(err, rollbackErr)
	}
	return nil
}

// FinalizeTombstone physically removes the recipe only after no operation can
// still own or mutate its resources. A false result means cleanup must finish
// before the same call is retried.
func (s *Store) FinalizeTombstone(recipeID string, purge func() error) (bool, error) {
	if strings.TrimSpace(recipeID) == "" || purge == nil {
		return false, errors.New("recipe tombstone finalization requires an id and callback")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.doc.Tombstones[recipeID]; !exists {
		return false, nil
	}
	for _, operation := range s.doc.Operations {
		if operation.RecipeID == recipeID && operationBlocksMutation(operation) {
			return false, nil
		}
	}
	if err := purge(); err != nil {
		return false, err
	}
	delete(s.doc.Tombstones, recipeID)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) IsTombstoned(recipeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.doc.Tombstones[recipeID]
	return ok
}

func (s *Store) Tombstones() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.doc.Tombstones))
	for id := range s.doc.Tombstones {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Prune bounds reconciled history while preserving every operation that still
// needs recovery and the newest reusable Check artifact for each recipe
// revision. It never removes runtime ownership merely because it is old.
func (s *Store) Prune(retain int, maxAge time.Duration) error {
	if retain < 0 || maxAge < 0 {
		return errors.New("invalid recipe operation retention policy")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	protected := make(map[string]struct{})
	latestChecks := make(map[string]Operation)
	for _, operation := range s.doc.Operations {
		if operation.NeedsRecovery() {
			protected[operation.ID] = struct{}{}
		}
		if operation.Kind == KindCheck && operation.Phase == PhasePrepared && operation.Preflight != nil {
			key := operation.RecipeID + "\x00" + operation.RecipeRevision
			current, ok := latestChecks[key]
			if !ok || operation.UpdatedAt > current.UpdatedAt || (operation.UpdatedAt == current.UpdatedAt && operation.ID > current.ID) {
				latestChecks[key] = operation
			}
		}
	}
	for _, operation := range latestChecks {
		protected[operation.ID] = struct{}{}
	}
	candidates := make([]Operation, 0)
	for _, operation := range s.doc.Operations {
		if _, keep := protected[operation.ID]; !keep {
			candidates = append(candidates, operation)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].UpdatedAt == candidates[j].UpdatedAt {
			return candidates[i].ID > candidates[j].ID
		}
		return candidates[i].UpdatedAt > candidates[j].UpdatedAt
	})
	now := time.Now()
	remove := make(map[string]struct{})
	for index, operation := range candidates {
		updated, _ := time.Parse(time.RFC3339Nano, operation.UpdatedAt)
		tooOld := maxAge > 0 && now.Sub(updated) > maxAge
		if index >= retain || tooOld {
			remove[operation.ID] = struct{}{}
		}
	}
	if len(remove) == 0 {
		return nil
	}
	previous := s.doc.Operations
	next := make(map[string]Operation, len(previous)-len(remove))
	for id, operation := range previous {
		if _, drop := remove[id]; !drop {
			next[id] = operation
		}
	}
	s.doc.Operations = next
	if err := s.saveLocked(); err != nil {
		s.doc.Operations = previous
		return err
	}
	return nil
}

// RuntimeName is a Docker Compose-compatible, operation-unique project name.
// It is intentionally independent of mutable recipe names and survives daemon
// restarts through the persisted operation record.
func RuntimeName(operation Operation) (string, error) {
	const prefix = "rop-"
	if !strings.HasPrefix(operation.ID, prefix) {
		return "", errors.New("invalid recipe operation id")
	}
	hexID := strings.TrimPrefix(operation.ID, prefix)
	if len(hexID) != 32 {
		return "", errors.New("invalid recipe operation id")
	}
	if _, err := hex.DecodeString(hexID); err != nil {
		return "", errors.New("invalid recipe operation id")
	}
	return "cloudless-recipe-" + hexID[:16], nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	operations := make(map[string]json.RawMessage, len(s.doc.Operations))
	for id, operation := range s.doc.Operations {
		payload, err := json.Marshal(operation)
		if err != nil {
			return err
		}
		if snapshot := s.historicalSnapshots[id]; len(snapshot) > 0 {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fields); err != nil {
				return err
			}
			fields["recipeSnapshot"] = snapshot
			payload, err = json.Marshal(fields)
			if err != nil {
				return err
			}
		}
		operations[id] = payload
	}
	persisted := struct {
		Version    int                        `json:"version"`
		Operations map[string]json.RawMessage `json:"operations"`
		Tombstones map[string]string          `json:"tombstones,omitempty"`
	}{Version: s.doc.Version, Operations: operations, Tombstones: s.doc.Tombstones}
	payload, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".operations-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, s.path)
}

func validKind(kind Kind) bool {
	return kind == KindCheck || kind == KindRun || kind == KindStop
}

func initialPhase(kind Kind) Phase {
	switch kind {
	case KindCheck:
		return PhaseChecking
	case KindRun:
		return PhasePreparing
	case KindStop:
		return PhaseStopping
	default:
		return ""
	}
}

func validOperation(operation Operation) bool {
	return validOperationWithHistoricalSnapshot(operation, false)
}

func validOperationWithHistoricalSnapshot(operation Operation, authenticHistoricalSnapshot bool) bool {
	if operation.ID == "" || !validKind(operation.Kind) || operation.RecipeID == "" || operation.RecipeRevision == "" || operation.Sequence == 0 {
		return false
	}
	if operation.CreatedAt == "" || operation.UpdatedAt == "" {
		return false
	}
	if operation.ResolvedSourceRevision != "" && !validGitObjectID(operation.ResolvedSourceRevision) {
		return false
	}
	if (operation.PreparedImageReference == "") != (operation.PreparedImageDigest == "") ||
		operation.PreparedImageReference != "" && (len(operation.PreparedImageReference) > 512 || strings.ContainsAny(operation.PreparedImageReference, "\x00\r\n\t ") || !validSHA256Identity(operation.PreparedImageDigest)) {
		return false
	}
	if operation.RecipeSnapshot.ID != "" {
		if operation.RecipeSnapshot.ID != operation.RecipeID {
			return false
		}
		revision, err := RecipeRevision(operation.RecipeSnapshot)
		if err != nil || revision != operation.RecipeRevision {
			// Normalizers intentionally migrate exact known legacy recipe fields.
			// Historical journals must remain readable, so also accept the hash of
			// the exact immutable snapshot stored in that journal. This does not
			// trust or execute the snapshot; it proves the revision was not altered.
			storedRevision, storedErr := snapshotRecipeRevision(operation.RecipeSnapshot)
			if (storedErr != nil || storedRevision != operation.RecipeRevision) && !authenticHistoricalSnapshot {
				return false
			}
		}
	}
	if operation.Preflight != nil {
		if (operation.Kind != KindCheck && operation.Kind != KindRun) || operation.Preflight.RecipeID != operation.RecipeID || operation.Preflight.RecipeRevision != operation.RecipeRevision || !validPreflightArtifact(*operation.Preflight) {
			return false
		}
	}
	if _, ok := transitions[operation.Phase]; !ok && operation.Phase != PhaseStopped && operation.Phase != PhaseFailed && operation.Phase != PhaseAborted {
		return false
	}
	for _, resource := range operation.Resources {
		if strings.TrimSpace(resource.Kind) == "" || strings.TrimSpace(resource.ID) == "" {
			return false
		}
	}
	for _, result := range operation.Checks {
		if strings.TrimSpace(result.ID) == "" || strings.TrimSpace(result.Summary) == "" || !validCheckStatus(result.Status) {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, result.ObservedAt); err != nil {
			return false
		}
	}
	if operation.Progress != nil {
		if operation.Progress.Stage == "" || operation.Progress.Message == "" || operation.Progress.OverallPercent < 0 || operation.Progress.OverallPercent > 100 || operation.Progress.PhasePercent < 0 || operation.Progress.PhasePercent > 100 {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, operation.Progress.UpdatedAt); err != nil {
			return false
		}
	}
	if operation.PreviousRuntime != nil {
		previous := *operation.PreviousRuntime
		operation.PreviousRuntime = &previous
	}
	for _, checkpoint := range operation.Checkpoints {
		if checkpoint.Phase == "" || checkpoint.Sequence == 0 || checkpoint.Sequence > operation.Sequence || !validPhase(checkpoint.Phase) {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, checkpoint.ObservedAt); err != nil {
			return false
		}
	}
	return true
}

func validPhase(phase Phase) bool {
	_, fromKnown := transitions[phase]
	if fromKnown {
		return true
	}
	for _, destinations := range transitions {
		if _, ok := destinations[phase]; ok {
			return true
		}
	}
	return false
}

func validCheckStatus(status CheckStatus) bool {
	return status == CheckPass || status == CheckFail || status == CheckWarning || status == CheckUnknown
}

func cloneValues(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func detachedOperation(operation Operation) Operation {
	if snapshot, err := cloneRecipe(operation.RecipeSnapshot); err == nil {
		operation.RecipeSnapshot = snapshot
	}
	operation.Resources = append([]Resource(nil), operation.Resources...)
	operation.Checks = append([]CheckResult(nil), operation.Checks...)
	operation.Checkpoints = append([]Checkpoint(nil), operation.Checkpoints...)
	if operation.Progress != nil {
		progress := *operation.Progress
		operation.Progress = &progress
	}
	for index := range operation.Checks {
		operation.Checks[index].Values = cloneValues(operation.Checks[index].Values)
	}
	if operation.Preflight != nil {
		artifact := *operation.Preflight
		artifact.Checks = append([]CheckResult(nil), operation.Preflight.Checks...)
		for index := range artifact.Checks {
			artifact.Checks[index].Values = cloneValues(artifact.Checks[index].Values)
		}
		operation.Preflight = &artifact
	}
	return operation
}

func cloneRecipe(recipe localrecipes.Recipe) (localrecipes.Recipe, error) {
	payload, err := json.Marshal(recipe)
	if err != nil {
		return localrecipes.Recipe{}, err
	}
	var clone localrecipes.Recipe
	if err := json.Unmarshal(payload, &clone); err != nil {
		return localrecipes.Recipe{}, err
	}
	return clone, nil
}

func operationBlocksAdmission(existing Operation, incoming Kind) bool {
	if existing.Kind == KindRun && existing.Phase == PhaseActive && incoming == KindStop {
		return false
	}
	for _, resource := range existing.Resources {
		if resource.RequiresCleanup() {
			return true
		}
	}
	switch existing.Phase {
	case PhaseStopped, PhaseFailed, PhaseAborted:
		return false
	}
	if existing.Kind == KindCheck && existing.Phase == PhasePrepared {
		return false
	}
	return true
}

func latestLaunchablePreflight(operations map[string]Operation, recipeID, revision string) (PreflightArtifact, bool) {
	var selected Operation
	found := false
	for _, operation := range operations {
		if operation.Kind != KindCheck || operation.Phase != PhasePrepared || operation.RecipeID != recipeID || operation.RecipeRevision != revision || operation.Preflight == nil || !operation.Preflight.Runnable || !validPreflightArtifact(*operation.Preflight) {
			continue
		}
		if !found || operation.CreatedAt > selected.CreatedAt || (operation.CreatedAt == selected.CreatedAt && operation.ID > selected.ID) {
			selected = operation
			found = true
		}
	}
	if !found {
		return PreflightArtifact{}, false
	}
	artifact := *detachedOperation(selected).Preflight
	return artifact, true
}

func operationBlocksMutation(operation Operation) bool {
	for _, resource := range operation.Resources {
		if resource.RequiresCleanup() {
			return true
		}
	}
	if operation.Kind == KindCheck && operation.Phase == PhasePrepared {
		return false
	}
	switch operation.Phase {
	case PhaseStopped, PhaseFailed, PhaseAborted:
		return false
	default:
		return true
	}
}

func newPreflightArtifact(operation Operation, imageDigest, platformFingerprint, clusterFingerprint string) (PreflightArtifact, error) {
	platformFingerprint = strings.TrimSpace(platformFingerprint)
	clusterFingerprint = strings.TrimSpace(clusterFingerprint)
	imageDigest = strings.ToLower(strings.TrimSpace(imageDigest))
	if !validSHA256Identity(platformFingerprint) || !validSHA256Identity(clusterFingerprint) || (imageDigest != "" && !validSHA256Identity(imageDigest)) {
		return PreflightArtifact{}, errors.New("preflight image, platform and cluster identities must be SHA-256")
	}
	runnable := len(operation.Checks) > 0
	launchable := runnable
	for _, result := range operation.Checks {
		if result.Status == CheckFail || result.Status == CheckUnknown {
			runnable = false
		}
		if result.Status != CheckPass {
			launchable = false
		}
	}
	artifact := PreflightArtifact{
		RecipeID: operation.RecipeID, RecipeRevision: operation.RecipeRevision,
		SourceRevision: operation.ResolvedSourceRevision, ImageDigest: imageDigest,
		PlatformFingerprint: platformFingerprint, ClusterFingerprint: clusterFingerprint,
		Runnable: runnable, Launchable: launchable, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Checks: detachedOperation(operation).Checks,
	}
	hash, err := preflightEvidenceHash(artifact)
	if err != nil {
		return PreflightArtifact{}, err
	}
	artifact.EvidenceHash = hash
	return artifact, nil
}

func preflightEvidenceHash(artifact PreflightArtifact) (string, error) {
	artifact.EvidenceHash = ""
	payload, err := json.Marshal(artifact)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validPreflightArtifact(artifact PreflightArtifact) bool {
	if artifact.RecipeID == "" || artifact.RecipeRevision == "" || !validSHA256Identity(artifact.PlatformFingerprint) || !validSHA256Identity(artifact.ClusterFingerprint) || !validSHA256Identity(artifact.EvidenceHash) {
		return false
	}
	if artifact.ImageDigest != "" && !validSHA256Identity(artifact.ImageDigest) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, artifact.CreatedAt); err != nil {
		return false
	}
	for _, result := range artifact.Checks {
		if strings.TrimSpace(result.ID) == "" || strings.TrimSpace(result.Summary) == "" || !validCheckStatus(result.Status) {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, result.ObservedAt); err != nil {
			return false
		}
	}
	want, err := preflightEvidenceHash(artifact)
	return err == nil && want == artifact.EvidenceHash
}

// Matches reports whether the artifact remains valid for the current immutable
// recipe, platform and cluster generation.
func (artifact PreflightArtifact) Matches(recipeRevision, platformFingerprint, clusterFingerprint string) bool {
	return validPreflightArtifact(artifact) && artifact.RecipeRevision == recipeRevision &&
		artifact.PlatformFingerprint == platformFingerprint && artifact.ClusterFingerprint == clusterFingerprint
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validSHA256Identity(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func sortResources(resources []Resource) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Node != resources[j].Node {
			return resources[i].Node < resources[j].Node
		}
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		if resources[i].ID != resources[j].ID {
			return resources[i].ID < resources[j].ID
		}
		return resources[i].Locator < resources[j].Locator
	})
}

func newID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("create recipe operation id: %w", err)
	}
	return "rop-" + hex.EncodeToString(bytes), nil
}
