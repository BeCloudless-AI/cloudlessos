// Package localrecipes persists machine-local, user-authored inference recipes.
// A recipe is a complete, editable execution specification. GitHub is an
// optional source of defaults, never a requirement or an execution boundary.
package localrecipes

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DeepSeekDSparkID          = "deepseek-v4-flash-dspark-2x"
	DeepSeekDSparkSource      = "https://github.com/tonyd2wild/DeepSeek-v4-Flash-DSpark-60-tok-s-900K-ctx-2x-DGX-Spark"
	DeepSeekDSparkRevision    = "51261f419a4a35a02966405ea9774b41735ec412"
	DeepSeekV4Flash1MID       = "deepseek-v4-flash-dual-dspark-1m"
	DeepSeekV4Flash1MSource   = "https://github.com/MiaAI-Lab/DeepSeek-V4-Flash-Dual-DGX-Spark-1M-Context"
	DeepSeekV4Flash1MRevision = "ffa38f9e1bea5c06a6195fca9bff17c04f4785da"
	CloudlessModelAlias       = "cloudless"
	// 8888 belongs to the bundled SearXNG service. Recipe engines use their own
	// host port so installing Research cannot prevent an inference runtime from
	// starting.
	DefaultRuntimePort = 8890
)

var containerImagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,511}$`)

type Command struct {
	Program string   `json:"program" yaml:"program"`
	Args    []string `json:"args" yaml:"args"`
}

type Source struct {
	URL      string            `json:"url" yaml:"url"`
	Revision string            `json:"revision" yaml:"revision"`
	Files    map[string]string `json:"files,omitempty" yaml:"files,omitempty"`
}

type Engine struct {
	Type            string   `json:"type" yaml:"type"`
	Image           string   `json:"image" yaml:"image"`
	ServedModelName string   `json:"servedModelName" yaml:"servedModelName"`
	ContainerPort   int      `json:"containerPort" yaml:"containerPort"`
	APIPath         string   `json:"apiPath" yaml:"apiPath"`
	ProxyHost       string   `json:"proxyHost" yaml:"proxyHost"`
	RestartPolicy   string   `json:"restartPolicy" yaml:"restartPolicy"`
	Arguments       []string `json:"arguments,omitempty" yaml:"arguments,omitempty"`
	EntryPoint      string   `json:"entryPoint,omitempty" yaml:"entryPoint,omitempty"`
	Command         []string `json:"command,omitempty" yaml:"command,omitempty"`
}

type ModelDependency struct {
	ID       string `json:"id" yaml:"id"`
	Revision string `json:"revision" yaml:"revision"`
	Role     string `json:"role,omitempty" yaml:"role,omitempty"`
}

type Model struct {
	ID                   string            `json:"id" yaml:"id"`
	Revision             string            `json:"revision" yaml:"revision"`
	Quantization         string            `json:"quantization" yaml:"quantization"`
	DType                string            `json:"dtype" yaml:"dtype"`
	KVCacheDType         string            `json:"kvCacheDtype" yaml:"kvCacheDtype"`
	MaxContext           int               `json:"maxContext" yaml:"maxContext"`
	MaxSequences         int               `json:"maxSequences" yaml:"maxSequences"`
	GPUMemoryUtilization float64           `json:"gpuMemoryUtilization" yaml:"gpuMemoryUtilization"`
	TensorParallel       int               `json:"tensorParallel" yaml:"tensorParallel"`
	PipelineParallel     int               `json:"pipelineParallel" yaml:"pipelineParallel"`
	TrustRemoteCode      bool              `json:"trustRemoteCode" yaml:"trustRemoteCode"`
	Dependencies         []ModelDependency `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
}

type Distributed struct {
	Nodes         int      `json:"nodes" yaml:"nodes"`
	Backend       string   `json:"backend" yaml:"backend"`
	MasterPort    int      `json:"masterPort" yaml:"masterPort"`
	Interface     string   `json:"interface" yaml:"interface"`
	HCA           string   `json:"hca" yaml:"hca"`
	IBGIDIndex    int      `json:"ibGidIndex" yaml:"ibGidIndex"`
	WorkerAlias   string   `json:"workerAlias" yaml:"workerAlias"`
	SelectedNodes []string `json:"selectedNodes,omitempty" yaml:"selectedNodes,omitempty"`
}

type Lifecycle struct {
	Build    Command `json:"build" yaml:"build"`
	Download Command `json:"download" yaml:"download"`
	Start    Command `json:"start" yaml:"start"`
	Stop     Command `json:"stop" yaml:"stop"`
}

type Runtime struct {
	Adapter        string `json:"adapter" yaml:"adapter"`
	ArtifactDigest string `json:"artifactDigest,omitempty" yaml:"-"`
	// BuildOnce and DownloadOnce let Cloudless own distribution for a
	// multi-node recipe. The runtime is built/downloaded on the coordinator,
	// then copied over the configured Spark fabric instead of asking every
	// node to repeat the same Internet transfer.
	BuildOnce      bool              `json:"buildOnce,omitempty" yaml:"buildOnce,omitempty"`
	DownloadOnce   bool              `json:"downloadOnce,omitempty" yaml:"downloadOnce,omitempty"`
	WorkingDir     string            `json:"workingDir" yaml:"workingDir"`
	TimeoutMinutes int               `json:"timeoutMinutes" yaml:"timeoutMinutes"`
	Prerequisites  []string          `json:"prerequisites,omitempty" yaml:"prerequisites,omitempty"`
	Environment    map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
	Lifecycle      Lifecycle         `json:"lifecycle" yaml:"lifecycle"`
	Container      ContainerRuntime  `json:"container,omitempty" yaml:"container,omitempty"`
}

// ContainerRuntime exposes container-contained execution controls for advanced
// recipes. It never grants host command execution or arbitrary host mounts;
// every requested container permission remains part of the signed manifest.
type ContainerRuntime struct {
	User           string   `json:"user,omitempty" yaml:"user,omitempty"`
	ReadOnly       bool     `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
	IPC            string   `json:"ipc,omitempty" yaml:"ipc,omitempty"`
	ShmSize        string   `json:"shmSize,omitempty" yaml:"shmSize,omitempty"`
	Ulimits        []string `json:"ulimits,omitempty" yaml:"ulimits,omitempty"`
	CapAdd         []string `json:"capAdd,omitempty" yaml:"capAdd,omitempty"`
	CapDrop        []string `json:"capDrop,omitempty" yaml:"capDrop,omitempty"`
	Tmpfs          []string `json:"tmpfs,omitempty" yaml:"tmpfs,omitempty"`
	PidsLimit      int      `json:"pidsLimit,omitempty" yaml:"pidsLimit,omitempty"`
	ModelCachePath string   `json:"modelCachePath,omitempty" yaml:"modelCachePath,omitempty"`
	Memory         string   `json:"memory,omitempty" yaml:"memory,omitempty"`
	MemorySwap     string   `json:"memorySwap,omitempty" yaml:"memorySwap,omitempty"`
	// Infiniband grants only the fixed /dev/infiniband device tree. Distributed
	// advanced containers use it for the enrolled Spark fabric; arbitrary
	// device paths remain impossible to express in a recipe.
	Infiniband bool `json:"infiniband,omitempty" yaml:"infiniband,omitempty"`
}

type Health struct {
	Scheme          string `json:"scheme" yaml:"scheme"`
	Host            string `json:"host" yaml:"host"`
	Port            int    `json:"port" yaml:"port"`
	Path            string `json:"path" yaml:"path"`
	TimeoutSeconds  int    `json:"timeoutSeconds" yaml:"timeoutSeconds"`
	IntervalSeconds int    `json:"intervalSeconds" yaml:"intervalSeconds"`
}

type legacyPreset struct {
	ID      string `json:"id"`
	Context int    `json:"context"`
}

type Draft struct {
	Name        string      `json:"name" yaml:"name"`
	Description string      `json:"description" yaml:"description"`
	Platform    string      `json:"platform" yaml:"platform"`
	Source      Source      `json:"source" yaml:"source"`
	Engine      Engine      `json:"engine" yaml:"engine"`
	Model       Model       `json:"model" yaml:"model"`
	Distributed Distributed `json:"distributed" yaml:"distributed"`
	Runtime     Runtime     `json:"runtime" yaml:"runtime"`
	Health      Health      `json:"health" yaml:"health"`
}

// CommunityProvenance binds a local installation to the exact immutable
// community revision that was verified before it entered the executable
// recipe store. It is deliberately separate from the editable recipe fields.
type CommunityProvenance struct {
	RecipeID      string               `json:"recipeId"`
	RevisionID    string               `json:"revisionId"`
	Slug          string               `json:"slug"`
	Version       string               `json:"version"`
	Digest        string               `json:"digest"`
	SigningKeyID  string               `json:"signingKeyId"`
	InstalledAt   string               `json:"installedAt"`
	Revoked       bool                 `json:"revoked,omitempty"`
	RevokedReason string               `json:"revokedReason,omitempty"`
	Validation    *CommunityValidation `json:"validation,omitempty"`
}

// CommunityValidation is supplemental, sanitized publication evidence from
// the community service. The signed release remains the execution authority;
// this evidence exists so Cloudless can explain known image risk before run.
type CommunityValidation struct {
	Result    string                    `json:"result,omitempty"`
	CheckedAt string                    `json:"checkedAt,omitempty"`
	Warnings  []string                  `json:"warnings,omitempty"`
	Image     *CommunityImageValidation `json:"image,omitempty"`
}

type CommunityImageValidation struct {
	Admission        string                          `json:"admission,omitempty"`
	Policy           string                          `json:"policy,omitempty"`
	Scanner          string                          `json:"scanner,omitempty"`
	Summary          CommunityVulnerabilitySummary   `json:"summary"`
	BlockingFindings []CommunityVulnerabilityFinding `json:"blockingFindings,omitempty"`
}

type CommunityVulnerabilitySummary struct {
	Total              int `json:"total"`
	High               int `json:"high"`
	Critical           int `json:"critical"`
	ActionableCritical int `json:"actionableCritical"`
	FixableCritical    int `json:"fixableCritical"`
	Warnings           int `json:"warnings"`
}

type CommunityVulnerabilityFinding struct {
	ID        string `json:"id,omitempty"`
	Package   string `json:"package,omitempty"`
	Installed string `json:"installed,omitempty"`
	Fixed     string `json:"fixed,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Target    string `json:"target,omitempty"`
}

type CommunityRollback struct {
	Draft      Draft               `json:"draft"`
	Provenance CommunityProvenance `json:"provenance"`
}

// Recipe retains several top-level compatibility fields so an installed
// 0.2.x UI can still render migrated recipes while the complete specification
// lives in the structured fields below.
type Recipe struct {
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Description       string               `json:"description"`
	Platform          string               `json:"platform"`
	Origin            string               `json:"origin"`
	Trust             string               `json:"trust"`
	ImportedAt        string               `json:"importedAt"`
	UpdatedAt         string               `json:"updatedAt"`
	TombstonedAt      string               `json:"tombstonedAt,omitempty"`
	Source            Source               `json:"source"`
	Engine            Engine               `json:"engine"`
	Model             Model                `json:"model"`
	Distributed       Distributed          `json:"distributed"`
	Runtime           Runtime              `json:"runtime"`
	Health            Health               `json:"health"`
	SourceURL         string               `json:"sourceUrl,omitempty"`
	Revision          string               `json:"revision,omitempty"`
	Adapter           string               `json:"adapter,omitempty"`
	ModelID           string               `json:"modelId,omitempty"`
	ModelRevision     string               `json:"modelRevision,omitempty"`
	Nodes             int                  `json:"nodes,omitempty"`
	Files             map[string]string    `json:"files,omitempty"`
	DefaultPreset     string               `json:"defaultPreset,omitempty"`
	Presets           []legacyPreset       `json:"presets,omitempty"`
	Community         *CommunityProvenance `json:"community,omitempty"`
	CommunityRollback []CommunityRollback  `json:"communityRollback,omitempty"`
}

type document struct {
	Version int      `json:"version"`
	Recipes []Recipe `json:"recipes"`
}

type Store struct {
	mu   sync.Mutex
	path string
}

func New(dir string) *Store { return &Store{path: filepath.Join(dir, "recipes", "index.json")} }

func command(program string, args ...string) Command { return Command{Program: program, Args: args} }

func deepSeekCanonicalFiles() map[string]string {
	return map[string]string{
		"build-dspark-vllm-runtime.sh":      "f563d82aaea11d48999fe485f12614cc54d8cac49bcaba4fe80b81596f7eb72e",
		"prepare-dspark-model-cache.sh":     "a75249711a9900f10eb5d148e5ecdbcb53ea879841a8d6944ea2b27238d0183e",
		"start-deepseek-v4-flash-dspark.sh": "5de0da3c5f5151a80465bc823e8f6b5908d65070e36652ea282ceaeff6c460a6",
		"stop-deepseek-v4-flash-dspark.sh":  "b8b252e129c22ca7984fae71d2efdf92f2a23a76009ab1f9ba3d6276e940b1aa",
		"docker-compose.dspark.yml":         "9c3e6cf6fd9895fd304bad2357f9c03a486b97da6d1c73b4237487e9fe56da79",
	}
}

func deepSeekV4Flash1MFiles() map[string]string {
	return map[string]string{
		"docker-compose.yml":         "9b660f696c86ffbf59ce0a139568545977819ff218f6f5356cb8b536f851ce87",
		"start-deepseek-v4-flash.sh": "a6a279947a6714a7775eadb8764b4340e0765f29f679255544b912ab28eb397e",
		"stop-deepseek-v4-flash.sh":  "efcd7350cec2d27d9d98f0ad541678ec2bd009e97935f95b72e8bb38dd096ee0",
		".env.example":               "8d0a0346e3d87aed2c1c0d670958bd51ecdc3a89135b0e8032aa574f6888598f",
	}
}

func deepSeekWindowsCheckoutFiles() map[string]string {
	return map[string]string{
		"build-dspark-vllm-runtime.sh":      "82d6c7e386ba16c59590a95a1e37d0f63e980bc6f6ccb6772cdcda96b9fb95f4",
		"prepare-dspark-model-cache.sh":     "0c5da4efdd8a9196985ac7cef833f57123021200e773e2ee7374747bf5ec2f16",
		"start-deepseek-v4-flash-dspark.sh": "32790858c9d1fb6914ef5e8b0ba0f5219cf4aae1dd30eb8dfc094027088c8fef",
		"stop-deepseek-v4-flash-dspark.sh":  "fb14350f96652063abad44a3791e2bb84fa20101083ba8c302d0a6b91850ee0a",
		"docker-compose.dspark.yml":         "06865a3e0e1392bb2e3ccd5200fa9389df19d6ef411b012175f44fb0a1fce9ba",
	}
}

func sameFiles(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, sum := range left {
		if right[name] != sum {
			return false
		}
	}
	return true
}

// NewDraft is a useful, runnable starting point—not a locked template. Every
// value returned here is sent to the editor and may be changed by the user.
func NewDraft() Draft {
	return Draft{
		Name: "My inference recipe", Description: "A repeatable local inference setup.", Platform: "dgx-spark",
		Engine:      Engine{Type: "vllm", Image: "cloudless/dspark-vllm:latest", ServedModelName: CloudlessModelAlias, ContainerPort: DefaultRuntimePort, APIPath: "/v1", ProxyHost: "host.docker.internal", RestartPolicy: "no", Arguments: []string{}},
		Model:       Model{ID: "deepseek-ai/DeepSeek-V4-Flash-DSpark", Revision: "main", Quantization: "none", DType: "auto", KVCacheDType: "auto", MaxContext: 131072, MaxSequences: 1, GPUMemoryUtilization: 0.80, TensorParallel: 2, PipelineParallel: 1},
		Distributed: Distributed{Nodes: 2, Backend: "nccl", MasterPort: 25000, Interface: "auto", HCA: "auto", IBGIDIndex: 0, WorkerAlias: "cloudless-recipe-worker"},
		Runtime: Runtime{Adapter: "source-scripts-v1", WorkingDir: ".", TimeoutMinutes: 480,
			Prerequisites: []string{"git", "bash", "docker", "ssh", "scp", "rsync"}, Environment: map[string]string{"HF_HUB_DISABLE_XET": "1", "HF_CACHE": "/var/lib/cloudless/models-cache"},
			Lifecycle: Lifecycle{Build: command("bash", "./build-dspark-vllm-runtime.sh"), Download: command("bash", "./prepare-dspark-model-cache.sh"), Start: command("bash", "./start-deepseek-v4-flash-dspark.sh"), Stop: command("bash", "./stop-deepseek-v4-flash-dspark.sh")}},
		Health: Health{Scheme: "http", Host: "127.0.0.1", Port: DefaultRuntimePort, Path: "/health", TimeoutSeconds: 180, IntervalSeconds: 3},
	}
}

// NewManagedDraft is the safe starting point exposed by the recipe builder.
// It deliberately leaves immutable identities blank: the user must select an
// exact image digest and model revision before the store accepts the recipe.
func NewManagedDraft() Draft {
	return Draft{
		Name: "My inference recipe", Description: "A repeatable, Cloudless-managed inference setup.", Platform: "generic",
		Engine: Engine{
			Type: "vllm", ServedModelName: CloudlessModelAlias, ContainerPort: DefaultRuntimePort,
			APIPath: "/v1", ProxyHost: "host.docker.internal", RestartPolicy: "no",
		},
		Model: Model{
			MaxContext: 32768, MaxSequences: 1, GPUMemoryUtilization: 0.8,
			TensorParallel: 1, PipelineParallel: 1, Quantization: "none", DType: "auto", KVCacheDType: "auto",
		},
		Distributed: Distributed{Nodes: 1, Backend: "nccl", MasterPort: 25000, WorkerAlias: "cloudless-recipe-worker"},
		Runtime: Runtime{
			Adapter: ManagedContainerAdapter, WorkingDir: ".", TimeoutMinutes: 480,
			Environment: map[string]string{"HF_HUB_DISABLE_XET": "1"},
		},
		Health: Health{
			Scheme: "http", Host: "127.0.0.1", Port: DefaultRuntimePort,
			Path: "/health", TimeoutSeconds: 7200, IntervalSeconds: 3,
		},
	}
}

func deepSeekDraft() Draft {
	d := NewDraft()
	d.Name = "DeepSeek V4 Flash DSpark"
	d.Description = "A two-DGX-Spark vLLM runtime using tensor parallelism, FP8 KV cache, and DSpark speculative decoding."
	// These hashes cover the canonical Git blob bytes at DeepSeekDSparkRevision
	// (LF line endings), not a Windows working tree. Using checkout bytes here
	// makes core.autocrlf silently produce fingerprints that can never verify on
	// the Linux appliance where recipes run.
	d.Source = Source{URL: DeepSeekDSparkSource, Revision: DeepSeekDSparkRevision, Files: deepSeekCanonicalFiles()}
	d.Engine.Image = "cloudless/dspark-vllm:" + DeepSeekDSparkRevision[:12]
	d.Engine.ServedModelName = "deepseek-v4-flash-dspark"
	d.Model.ID = "deepseek-ai/DeepSeek-V4-Flash-DSpark"
	d.Model.Revision = "62af8fffb2f7030cac4de2f0169f5b8d1101b646"
	d.Model.KVCacheDType = "fp8"
	d.Model.MaxContext = 262144
	d.Runtime.Environment["GPU_MEMORY_UTILIZATION"] = "0.80"
	d.Runtime.BuildOnce = true
	d.Runtime.DownloadOnce = true
	return d
}

func deepSeekV4Flash1MDraft() Draft {
	d := NewDraft()
	d.Name = "DeepSeek V4 Flash · Dual Spark · 1M"
	d.Description = "The reviewed MiaAI-Lab two-DGX-Spark vLLM recipe for DeepSeek V4 Flash with a one-million-token context window."
	d.Source = Source{URL: DeepSeekV4Flash1MSource, Revision: DeepSeekV4Flash1MRevision, Files: deepSeekV4Flash1MFiles()}
	d.Engine.Image = "aidendle94/sparkrun-vllm-ds4-gb10:production-ready"
	d.Model.ID = "deepseek-ai/DeepSeek-V4-Flash"
	d.Model.Revision = "60d8d70770c6776ff598c94bb586a859a38244f1"
	d.Model.KVCacheDType = "fp8"
	d.Model.MaxContext = 1000000
	d.Model.MaxSequences = 6
	d.Model.GPUMemoryUtilization = 0.83
	d.Model.TrustRemoteCode = true
	d.Runtime.BuildOnce = false
	d.Runtime.DownloadOnce = true
	d.Runtime.Lifecycle.Build = Command{}
	d.Runtime.Lifecycle.Download = command("bash", "./prepare-deepseek-v4-flash-model.sh")
	d.Runtime.Lifecycle.Start = command("bash", "./start-deepseek-v4-flash.sh")
	d.Runtime.Lifecycle.Stop = command("bash", "./stop-deepseek-v4-flash.sh")
	d.Runtime.Environment["VLLM_ALLOW_LONG_MAX_MODEL_LEN"] = "1"
	d.Health.TimeoutSeconds = 7200
	return d
}

func canonicalGitHub(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") {
		return "", errors.New("enter an https://github.com repository URL")
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("enter a GitHub repository URL containing an owner and repository")
	}
	return "https://github.com/" + parts[0] + "/" + parts[1], nil
}

func syncCompatibility(recipe Recipe) Recipe {
	recipe.SourceURL, recipe.Revision, recipe.Files = recipe.Source.URL, recipe.Source.Revision, recipe.Source.Files
	recipe.Adapter, recipe.ModelID, recipe.ModelRevision = recipe.Runtime.Adapter, recipe.Model.ID, recipe.Model.Revision
	recipe.Nodes = recipe.Distributed.Nodes
	return recipe
}

func recipeFromDraft(id, origin, trust, now string, draft Draft) Recipe {
	return syncCompatibility(Recipe{ID: id, Name: draft.Name, Description: draft.Description, Platform: draft.Platform,
		Origin: origin, Trust: trust, ImportedAt: now, UpdatedAt: now, Source: draft.Source, Engine: draft.Engine,
		Model: draft.Model, Distributed: draft.Distributed, Runtime: draft.Runtime, Health: draft.Health})
}

func DraftFromRecipe(recipe Recipe) Draft {
	// normalize updates nested maps for exact legacy migrations. Detach the
	// value first so calculating an identity can never mutate a persisted
	// operation snapshot supplied by the caller.
	if payload, err := json.Marshal(recipe); err == nil {
		var detached Recipe
		if json.Unmarshal(payload, &detached) == nil {
			recipe = detached
		}
	}
	recipe = normalize(recipe)
	return DraftFromSnapshot(recipe)
}

// DraftFromSnapshot returns the executable fields exactly as they were
// persisted. Operation journals use this only to authenticate historical
// snapshots created before a later normalizer migration. New recipe identity
// must continue to use DraftFromRecipe.
func DraftFromSnapshot(recipe Recipe) Draft {
	return Draft{Name: recipe.Name, Description: recipe.Description, Platform: recipe.Platform, Source: recipe.Source,
		Engine: recipe.Engine, Model: recipe.Model, Distributed: recipe.Distributed, Runtime: recipe.Runtime, Health: recipe.Health}
}

func recognized(raw string) (Recipe, error) {
	source, err := canonicalGitHub(raw)
	if err != nil {
		return Recipe{}, err
	}
	var id string
	var d Draft
	switch {
	case strings.EqualFold(source, DeepSeekDSparkSource):
		id, d = DeepSeekDSparkID, deepSeekDraft()
	case strings.EqualFold(source, DeepSeekV4Flash1MSource):
		id, d = DeepSeekV4Flash1MID, deepSeekV4Flash1MDraft()
	default:
		return Recipe{}, errors.New("this repository has no Cloudless import profile yet; create a recipe manually and enter its source details instead")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return recipeFromDraft(id, "github", "reviewed-import", now, d), nil
}

func normalize(recipe Recipe) Recipe {
	// Migrate the version-1 fixed-adapter document in place.
	if recipe.Engine.Type == "" {
		tombstonedAt := recipe.TombstonedAt
		d := deepSeekDraft()
		if recipe.Name != "" {
			d.Name = recipe.Name
		}
		if recipe.Description != "" {
			d.Description = recipe.Description
		}
		if recipe.Platform != "" {
			d.Platform = recipe.Platform
		}
		if recipe.SourceURL != "" {
			d.Source.URL = recipe.SourceURL
		}
		if recipe.Revision != "" {
			d.Source.Revision = recipe.Revision
		}
		if recipe.Files != nil {
			d.Source.Files = recipe.Files
		}
		if recipe.ModelID != "" {
			d.Model.ID = recipe.ModelID
		}
		if recipe.ModelRevision != "" {
			d.Model.Revision = recipe.ModelRevision
		}
		if recipe.Nodes > 0 {
			d.Distributed.Nodes, d.Model.TensorParallel = recipe.Nodes, recipe.Nodes
		}
		selected := recipe.DefaultPreset
		if selected == "" {
			selected = "balanced"
		}
		for _, preset := range recipe.Presets {
			if preset.ID == selected && preset.Context > 0 {
				d.Model.MaxContext = preset.Context
			}
		}
		migrated := recipeFromDraft(recipe.ID, recipe.Origin, recipe.Trust, recipe.ImportedAt, d)
		migrated.UpdatedAt = recipe.UpdatedAt
		migrated.TombstonedAt = tombstonedAt
		recipe = migrated
	}
	// Repair the original built-in profile, whose fingerprints were generated
	// from a CRLF-converted Windows checkout. Only the exact known-bad set is
	// migrated, so fingerprints edited by a user remain untouched.
	if recipe.Source.URL == DeepSeekDSparkSource && recipe.Source.Revision == DeepSeekDSparkRevision && sameFiles(recipe.Source.Files, deepSeekWindowsCheckoutFiles()) {
		recipe.Source.Files = deepSeekCanonicalFiles()
	}
	// Existing installations already have this reviewed recipe persisted.
	// Upgrade it in place to the coordinator-once distribution contract so an
	// update does not require removing and importing the recipe again.
	if recipe.Source.URL == DeepSeekDSparkSource && recipe.Source.Revision == DeepSeekDSparkRevision {
		recipe.Runtime.BuildOnce = true
		recipe.Runtime.DownloadOnce = true
		if recipe.Engine.ContainerPort == 8888 {
			recipe.Engine.ContainerPort = DefaultRuntimePort
		}
		if recipe.Health.Port == 8888 {
			recipe.Health.Port = DefaultRuntimePort
		}
	}
	// Upgrade the reviewed MiaAI-Lab profile to Cloudless-owned model staging.
	// Letting vLLM discover an incomplete snapshot during startup hides hundreds
	// of gigabytes of Internet transfer behind a misleading "launching" state.
	if recipe.Source.URL == DeepSeekV4Flash1MSource && recipe.Source.Revision == DeepSeekV4Flash1MRevision {
		// Releases before 0.2.7 stored the Hugging Face cache under a Docker
		// volume name. The reviewed profile now uses Cloudless' durable host
		// model cache. Migrate only that exact legacy value; all other edits
		// remain local customizations and therefore stay unreviewed.
		if recipe.ID == DeepSeekV4Flash1MID && recipe.Origin == "github" && recipe.Trust == "reviewed-import" && recipe.Runtime.Environment["HF_CACHE"] == "cloudless-hf" {
			recipe.Runtime.Environment["HF_CACHE"] = "/var/lib/cloudless/models-cache"
		}
		recipe.Runtime.DownloadOnce = true
		recipe.Runtime.Lifecycle.Download = command("bash", "./prepare-deepseek-v4-flash-model.sh")
		recipe.Health.TimeoutSeconds = 7200
	}
	if recipe.Origin == "" {
		recipe.Origin = "local"
	}
	if recipe.Trust == "" {
		recipe.Trust = "local-custom"
	}
	if recipe.ImportedAt == "" {
		recipe.ImportedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if recipe.UpdatedAt == "" {
		recipe.UpdatedAt = recipe.ImportedAt
	}
	if recipe.Engine.ProxyHost == "" {
		recipe.Engine.ProxyHost = "host.docker.internal"
	}
	if strings.TrimSpace(recipe.Engine.APIPath) == "" {
		recipe.Engine.APIPath = "/v1"
	}
	// Cloudless applications and the public API use one permanent model name.
	// A recipe selects the underlying weights and runtime, never the client
	// contract exposed by the OS.
	recipe.Engine.ServedModelName = CloudlessModelAlias
	// Distributed containers must never independently auto-start after a boot.
	// cloudlessd reconciles topology first and starts coordinator then workers.
	recipe.Engine.RestartPolicy = "no"
	return syncCompatibility(recipe)
}

func cleanStrings(values []string, limit int) ([]string, error) {
	if len(values) > limit {
		return nil, fmt.Errorf("no more than %d values are allowed", limit)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 1024 || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("runtime values must be plain text under 1024 characters")
		}
		result = append(result, value)
	}
	return result, nil
}

func validateCommand(label string, value Command) (Command, error) {
	value.Program = strings.TrimSpace(value.Program)
	if value.Program == "" {
		return value, nil
	}
	if strings.ContainsAny(value.Program, "\x00\r\n") || len(value.Program) > 256 {
		return Command{}, fmt.Errorf("%s command is invalid", label)
	}
	args, err := cleanStrings(value.Args, 128)
	if err != nil {
		return Command{}, fmt.Errorf("%s arguments: %w", label, err)
	}
	value.Args = args
	return value, nil
}

func validateDraft(d Draft) (Draft, error) {
	d.Name, d.Description, d.Platform = strings.TrimSpace(d.Name), strings.TrimSpace(d.Description), strings.TrimSpace(d.Platform)
	if len(d.Name) < 2 || len(d.Name) > 80 {
		return Draft{}, errors.New("recipe name must be between 2 and 80 characters")
	}
	if len(d.Description) > 1000 {
		return Draft{}, errors.New("recipe description must be 1000 characters or fewer")
	}
	if d.Platform == "" || len(d.Platform) > 64 {
		return Draft{}, errors.New("choose a target platform")
	}
	d.Source.URL, d.Source.Revision = strings.TrimSpace(d.Source.URL), strings.TrimSpace(d.Source.Revision)
	if d.Source.URL != "" {
		u, err := url.Parse(d.Source.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return Draft{}, errors.New("source URL must be an HTTP or HTTPS repository URL")
		}
		if d.Source.Revision == "" || len(d.Source.Revision) > 128 {
			return Draft{}, errors.New("enter the exact source branch, tag, or commit")
		}
	}
	if len(d.Source.Files) > 128 {
		return Draft{}, errors.New("no more than 128 source file checksums are allowed")
	}
	for name, sum := range d.Source.Files {
		if name == "" || filepath.IsAbs(name) || strings.Contains(filepath.Clean(name), "..") {
			return Draft{}, errors.New("source checksum paths must stay inside the repository")
		}
		if len(sum) != 64 {
			return Draft{}, fmt.Errorf("source checksum for %s must be SHA-256", name)
		}
	}
	d.Engine.Type, d.Engine.Image, d.Engine.APIPath = strings.TrimSpace(d.Engine.Type), strings.TrimSpace(d.Engine.Image), strings.TrimSpace(d.Engine.APIPath)
	if d.Engine.APIPath == "" {
		d.Engine.APIPath = "/v1"
	}
	d.Engine.ServedModelName = CloudlessModelAlias
	d.Engine.ProxyHost, d.Engine.RestartPolicy = strings.TrimSpace(d.Engine.ProxyHost), strings.TrimSpace(d.Engine.RestartPolicy)
	if d.Engine.Type == "" || len(d.Engine.Type) > 64 {
		return Draft{}, errors.New("choose an inference engine")
	}
	if !containerImagePattern.MatchString(d.Engine.Image) {
		return Draft{}, errors.New("engine image is invalid")
	}
	if d.Engine.ContainerPort < 1 || d.Engine.ContainerPort > 65535 {
		return Draft{}, errors.New("engine port must be between 1 and 65535")
	}
	if strings.TrimRight(d.Engine.APIPath, "/") != "/v1" {
		return Draft{}, errors.New("engine API path must preserve the OpenAI-compatible /v1 contract")
	}
	if d.Engine.ProxyHost == "" || len(d.Engine.ProxyHost) > 253 || strings.ContainsAny(d.Engine.ProxyHost, "\x00\r\n /\\") {
		return Draft{}, errors.New("engine proxy host is invalid")
	}
	if len(d.Engine.RestartPolicy) > 64 || strings.ContainsAny(d.Engine.RestartPolicy, "\x00\r\n") {
		return Draft{}, errors.New("engine restart policy is invalid")
	}
	d.Engine.RestartPolicy = "no"
	var err error
	if d.Engine.Arguments, err = cleanStrings(d.Engine.Arguments, 256); err != nil {
		return Draft{}, fmt.Errorf("engine arguments: %w", err)
	}
	d.Engine.EntryPoint = strings.TrimSpace(d.Engine.EntryPoint)
	if len(d.Engine.EntryPoint) > 1024 || strings.ContainsRune(d.Engine.EntryPoint, '\x00') {
		return Draft{}, errors.New("engine entry point is invalid")
	}
	if len(d.Engine.Command) > 512 {
		return Draft{}, errors.New("engine command cannot contain more than 512 arguments")
	}
	command := make([]string, 0, len(d.Engine.Command))
	for _, value := range d.Engine.Command {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 65536 || strings.ContainsRune(value, '\x00') {
			return Draft{}, errors.New("engine command arguments must be plain text under 64 KiB")
		}
		command = append(command, value)
	}
	d.Engine.Command = command
	d.Model.ID, d.Model.Revision = strings.TrimSpace(d.Model.ID), strings.TrimSpace(d.Model.Revision)
	if d.Model.ID == "" || len(d.Model.ID) > 256 {
		return Draft{}, errors.New("enter a model ID or path")
	}
	if d.Model.Revision == "" || len(d.Model.Revision) > 128 {
		return Draft{}, errors.New("enter a model revision")
	}
	if d.Model.MaxContext < 1 || d.Model.MaxContext > 10000000 {
		return Draft{}, errors.New("context window must be between 1 and 10,000,000 tokens")
	}
	if len(d.Model.Dependencies) > 16 {
		return Draft{}, errors.New("no more than 16 additional model dependencies are allowed")
	}
	for index := range d.Model.Dependencies {
		dependency := &d.Model.Dependencies[index]
		dependency.ID, dependency.Revision, dependency.Role = strings.TrimSpace(dependency.ID), strings.TrimSpace(dependency.Revision), strings.TrimSpace(dependency.Role)
		if dependency.ID == "" || len(dependency.ID) > 256 || dependency.Revision == "" || len(dependency.Revision) > 128 || len(dependency.Role) > 64 {
			return Draft{}, errors.New("additional model dependencies require a model ID, revision, and optional short role")
		}
	}
	if d.Model.MaxSequences < 1 || d.Model.MaxSequences > 65536 {
		return Draft{}, errors.New("maximum sequences must be between 1 and 65,536")
	}
	if d.Model.GPUMemoryUtilization <= 0 || d.Model.GPUMemoryUtilization > 1 {
		return Draft{}, errors.New("GPU memory utilization must be above 0 and at most 1")
	}
	if d.Model.TensorParallel < 1 || d.Model.TensorParallel > 1024 || d.Model.PipelineParallel < 1 || d.Model.PipelineParallel > 1024 {
		return Draft{}, errors.New("parallelism values must be between 1 and 1024")
	}
	if d.Distributed.Nodes < 1 || d.Distributed.Nodes > 64 {
		return Draft{}, errors.New("node count must be between 1 and 64")
	}
	if d.Distributed.MasterPort < 1 || d.Distributed.MasterPort > 65535 {
		return Draft{}, errors.New("distributed master port must be between 1 and 65535")
	}
	if d.Distributed.WorkerAlias == "" || len(d.Distributed.WorkerAlias) > 64 {
		return Draft{}, errors.New("enter a worker alias")
	}
	if d.Distributed.SelectedNodes, err = cleanStrings(d.Distributed.SelectedNodes, 63); err != nil {
		return Draft{}, fmt.Errorf("selected nodes: %w", err)
	}
	if len(d.Distributed.SelectedNodes) != 0 && len(d.Distributed.SelectedNodes) != d.Distributed.Nodes-1 {
		return Draft{}, fmt.Errorf("select exactly %d worker node(s), or leave selection empty for automatic placement", d.Distributed.Nodes-1)
	}
	seenNodes := make(map[string]struct{}, len(d.Distributed.SelectedNodes))
	for _, node := range d.Distributed.SelectedNodes {
		if _, duplicate := seenNodes[node]; duplicate {
			return Draft{}, errors.New("selected worker nodes must be unique")
		}
		seenNodes[node] = struct{}{}
	}
	if d.Runtime.Adapter == "" || len(d.Runtime.Adapter) > 64 {
		return Draft{}, errors.New("choose a runtime adapter")
	}
	if d.Runtime.ArtifactDigest != "" && !regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(d.Runtime.ArtifactDigest) {
		return Draft{}, errors.New("catalog artifact digest must be SHA-256")
	}
	if d.Runtime.TimeoutMinutes < 1 || d.Runtime.TimeoutMinutes > 10080 {
		return Draft{}, errors.New("runtime timeout must be between 1 minute and 7 days")
	}
	if d.Runtime.Prerequisites, err = cleanStrings(d.Runtime.Prerequisites, 64); err != nil {
		return Draft{}, fmt.Errorf("prerequisites: %w", err)
	}
	if len(d.Runtime.Environment) > 256 {
		return Draft{}, errors.New("no more than 256 environment variables are allowed")
	}
	for key, value := range d.Runtime.Environment {
		if key == "" || len(key) > 128 || strings.ContainsAny(key, "=\x00\r\n") || len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") {
			return Draft{}, fmt.Errorf("environment variable %q is invalid", key)
		}
	}
	for label, value := range map[string]*Command{"build": &d.Runtime.Lifecycle.Build, "download": &d.Runtime.Lifecycle.Download, "start": &d.Runtime.Lifecycle.Start, "stop": &d.Runtime.Lifecycle.Stop} {
		validated, commandErr := validateCommand(label, *value)
		if commandErr != nil {
			return Draft{}, commandErr
		}
		*value = validated
	}
	if d.Runtime.Container.Ulimits, err = cleanStrings(d.Runtime.Container.Ulimits, 32); err != nil {
		return Draft{}, fmt.Errorf("container ulimits: %w", err)
	}
	if d.Runtime.Container.CapAdd, err = cleanStrings(d.Runtime.Container.CapAdd, 64); err != nil {
		return Draft{}, fmt.Errorf("container capabilities: %w", err)
	}
	if d.Runtime.Container.CapDrop, err = cleanStrings(d.Runtime.Container.CapDrop, 64); err != nil {
		return Draft{}, fmt.Errorf("container dropped capabilities: %w", err)
	}
	if d.Runtime.Container.Tmpfs, err = cleanStrings(d.Runtime.Container.Tmpfs, 32); err != nil {
		return Draft{}, fmt.Errorf("container tmpfs: %w", err)
	}
	d.Runtime.Container.User = strings.TrimSpace(d.Runtime.Container.User)
	d.Runtime.Container.ModelCachePath = strings.TrimSpace(d.Runtime.Container.ModelCachePath)
	if IsContainerAdapter(d.Runtime.Adapter) {
		if err := validateManagedContainerDraft(d); err != nil {
			return Draft{}, err
		}
	} else if d.Runtime.Lifecycle.Start.Program == "" {
		return Draft{}, errors.New("a start command is required")
	}
	if d.Health.Scheme != "http" && d.Health.Scheme != "https" {
		return Draft{}, errors.New("health-check scheme must be HTTP or HTTPS")
	}
	if d.Health.Port < 1 || d.Health.Port > 65535 || d.Health.TimeoutSeconds < 1 || d.Health.TimeoutSeconds > 86400 || d.Health.IntervalSeconds < 1 || d.Health.IntervalSeconds > 3600 {
		return Draft{}, errors.New("health-check port or timing is invalid")
	}
	if !strings.HasPrefix(d.Health.Path, "/") {
		return Draft{}, errors.New("health-check path must begin with /")
	}
	return d, nil
}

func recipeID() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("local-%x", value[:]), nil
}

func (s *Store) load() (document, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return document{Version: 2, Recipes: []Recipe{}}, nil
	}
	if err != nil {
		return document{}, err
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return document{}, fmt.Errorf("read local recipes: %w", err)
	}
	if doc.Version != 1 && doc.Version != 2 {
		return document{}, fmt.Errorf("unsupported local recipe version %d", doc.Version)
	}
	for i := range doc.Recipes {
		doc.Recipes[i] = normalize(doc.Recipes[i])
	}
	doc.Version = 2
	return doc, nil
}

func (s *Store) save(doc document) error {
	doc.Version = 2
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) List() ([]Recipe, error) {
	all, err := s.ListAll()
	if err != nil {
		return nil, err
	}
	result := make([]Recipe, 0, len(all))
	for _, recipe := range all {
		if recipe.TombstonedAt == "" {
			result = append(result, recipe)
		}
	}
	return result, nil
}

// ListAll includes deletion tombstones for recovery and management surfaces.
// Executable catalog paths should use List instead.
func (s *Store) ListAll() ([]Recipe, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return nil, err
	}
	sort.Slice(doc.Recipes, func(i, j int) bool { return doc.Recipes[i].Name < doc.Recipes[j].Name })
	result := make([]Recipe, len(doc.Recipes))
	copy(result, doc.Recipes)
	return result, nil
}
func (s *Store) Get(id string) (Recipe, bool, error) {
	all, err := s.List()
	if err != nil {
		return Recipe{}, false, err
	}
	for _, recipe := range all {
		if recipe.ID == id {
			return recipe, true, nil
		}
	}
	return Recipe{}, false, nil
}

// GetAny includes a tombstoned recipe so cleanup and diagnostics can still
// reconstruct its lifecycle until every owned resource has been reconciled.
func (s *Store) GetAny(id string) (Recipe, bool, error) {
	all, err := s.ListAll()
	if err != nil {
		return Recipe{}, false, err
	}
	for _, recipe := range all {
		if recipe.ID == id {
			return recipe, true, nil
		}
	}
	return Recipe{}, false, nil
}

func (s *Store) Import(source string) (Recipe, error) {
	recipe, err := recognized(source)
	if err != nil {
		return Recipe{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return Recipe{}, err
	}
	replaced := false
	for i := range doc.Recipes {
		if doc.Recipes[i].ID == recipe.ID {
			doc.Recipes[i], replaced = recipe, true
		}
	}
	if !replaced {
		doc.Recipes = append(doc.Recipes, recipe)
	}
	if err := s.save(doc); err != nil {
		return Recipe{}, err
	}
	return recipe, nil
}

// PreviewImport resolves a reviewed GitHub profile without changing the local
// recipe store. The unified importer uses this before asking the user to
// confirm the import.
func (s *Store) PreviewImport(source string) (Recipe, error) {
	return recognized(source)
}

func (s *Store) Create(draft Draft) (Recipe, error) {
	return s.create(draft, "local", "local-custom")
}

// CreateIndexed persists a recipe whose source artifact and catalog entry were
// verified by Cloudless before its native manifest was parsed.
func (s *Store) CreateIndexed(draft Draft, trust, digest string) (Recipe, error) {
	switch trust {
	case "upstream-official", "community", "discovered":
	default:
		trust = "discovered"
	}
	draft.Runtime.ArtifactDigest = strings.ToLower(strings.TrimSpace(digest))
	return s.create(draft, "catalog", trust)
}

// InstallCommunity atomically inserts or replaces one verified immutable
// community revision. Callers must verify the release signature, revocation
// feed and managed-container policy before invoking this method.
func (s *Store) InstallCommunity(draft Draft, provenance CommunityProvenance) (Recipe, error) {
	if strings.TrimSpace(provenance.RecipeID) == "" || strings.TrimSpace(provenance.RevisionID) == "" ||
		strings.TrimSpace(provenance.Version) == "" || !strings.HasPrefix(strings.ToLower(provenance.Digest), "sha256:") ||
		strings.TrimSpace(provenance.SigningKeyID) == "" || provenance.Revoked {
		return Recipe{}, errors.New("community recipe provenance is incomplete or revoked")
	}
	draft, err := validateDraft(draft)
	if err != nil {
		return Recipe{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	provenance.InstalledAt = now
	id := "community-" + strings.ReplaceAll(strings.ToLower(provenance.RecipeID), "-", "")
	if len(id) > 42 {
		id = id[:42]
	}
	recipe := recipeFromDraft(id, "community", "community-signed", now, draft)
	recipe.Community = &provenance

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return Recipe{}, err
	}
	replaced := false
	for index := range doc.Recipes {
		if doc.Recipes[index].Community != nil && doc.Recipes[index].Community.RecipeID == provenance.RecipeID {
			previous := doc.Recipes[index]
			if previous.Community.RevisionID == provenance.RevisionID && previous.Community.Digest == provenance.Digest {
				// The signed recipe is unchanged, but publication evidence may have
				// been added after an older Cloudless version installed it. Refresh
				// that supplemental evidence without creating a false rollback entry.
				if provenance.Validation != nil {
					previous.Community.Validation = provenance.Validation
					previous.UpdatedAt = now
					doc.Recipes[index] = previous
					if err := s.save(doc); err != nil {
						return Recipe{}, err
					}
				}
				return previous, nil
			}
			recipe.CommunityRollback = append(previous.CommunityRollback, CommunityRollback{Draft: DraftFromSnapshot(previous), Provenance: *previous.Community})
			if len(recipe.CommunityRollback) > 3 {
				recipe.CommunityRollback = recipe.CommunityRollback[len(recipe.CommunityRollback)-3:]
			}
			doc.Recipes[index], replaced = recipe, true
			break
		}
	}
	if !replaced {
		doc.Recipes = append(doc.Recipes, recipe)
	}
	if err := s.save(doc); err != nil {
		return Recipe{}, err
	}
	return recipe, nil
}

func (s *Store) RollbackCommunity(id string) (Recipe, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return Recipe{}, err
	}
	for index := range doc.Recipes {
		current := doc.Recipes[index]
		if current.ID != id || current.Community == nil {
			continue
		}
		if len(current.CommunityRollback) == 0 {
			return Recipe{}, errors.New("no previous community revision is available")
		}
		last := current.CommunityRollback[len(current.CommunityRollback)-1]
		if last.Provenance.Revoked {
			return Recipe{}, errors.New("the previous community revision is revoked")
		}
		restored := recipeFromDraft(current.ID, "community", "community-signed", current.ImportedAt, last.Draft)
		restored.Community = &last.Provenance
		restored.CommunityRollback = current.CommunityRollback[:len(current.CommunityRollback)-1]
		doc.Recipes[index] = restored
		if err := s.save(doc); err != nil {
			return Recipe{}, err
		}
		return restored, nil
	}
	return Recipe{}, os.ErrNotExist
}

// MarkCommunityRevoked leaves forensic and cleanup information in place while
// ensuring catalog callers no longer treat the revision as executable.
func (s *Store) MarkCommunityRevoked(revisionID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return err
	}
	found := false
	for index := range doc.Recipes {
		provenance := doc.Recipes[index].Community
		if provenance != nil && provenance.RevisionID == revisionID {
			provenance.Revoked = true
			provenance.RevokedReason = strings.TrimSpace(reason)
			doc.Recipes[index].Trust = "community-revoked"
			found = true
		}
		for rollbackIndex := range doc.Recipes[index].CommunityRollback {
			rollback := &doc.Recipes[index].CommunityRollback[rollbackIndex].Provenance
			if rollback.RevisionID == revisionID {
				rollback.Revoked = true
				rollback.RevokedReason = strings.TrimSpace(reason)
				found = true
			}
		}
	}
	if !found {
		return os.ErrNotExist
	}
	return s.save(doc)
}

func (s *Store) create(draft Draft, origin, trust string) (Recipe, error) {
	draft, err := validateDraft(draft)
	if err != nil {
		return Recipe{}, err
	}
	id, err := recipeID()
	if err != nil {
		return Recipe{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	recipe := recipeFromDraft(id, origin, trust, now, draft)
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return Recipe{}, err
	}
	doc.Recipes = append(doc.Recipes, recipe)
	if err := s.save(doc); err != nil {
		return Recipe{}, err
	}
	return recipe, nil
}

func (s *Store) Update(id string, draft Draft) (Recipe, error) {
	draft, err := validateDraft(draft)
	if err != nil {
		return Recipe{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return Recipe{}, err
	}
	for i := range doc.Recipes {
		if doc.Recipes[i].ID != id {
			continue
		}
		created, origin := doc.Recipes[i].ImportedAt, doc.Recipes[i].Origin
		doc.Recipes[i] = recipeFromDraft(id, origin, "local-custom", created, draft)
		doc.Recipes[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := s.save(doc); err != nil {
			return Recipe{}, err
		}
		return doc.Recipes[i], nil
	}
	return Recipe{}, os.ErrNotExist
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return err
	}
	next := doc.Recipes[:0]
	for _, recipe := range doc.Recipes {
		if recipe.ID != id {
			next = append(next, recipe)
		}
	}
	doc.Recipes = next
	return s.save(doc)
}

// Tombstone immediately removes a recipe from executable catalog lookups but
// retains its exact lifecycle specification for cleanup and diagnostics.
func (s *Store) Tombstone(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return err
	}
	for index := range doc.Recipes {
		if doc.Recipes[index].ID != id {
			continue
		}
		if doc.Recipes[index].TombstonedAt == "" {
			doc.Recipes[index].TombstonedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		return s.save(doc)
	}
	return os.ErrNotExist
}
