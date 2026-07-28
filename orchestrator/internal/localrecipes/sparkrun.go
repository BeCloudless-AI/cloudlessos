package localrecipes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SparkRunAdapter         = "sparkrun-provider-v1"
	SparkRunProviderVersion = "0.2.40"
)

var sparkRunKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

type sparkRunMetadata struct {
	Description string `yaml:"description"`
	Maintainer  string `yaml:"maintainer"`
	ModelParams string `yaml:"model_params"`
	ModelDType  string `yaml:"model_dtype"`
	KVDType     string `yaml:"kv_dtype"`
}

type sparkRunDocument struct {
	Name            string               `yaml:"name"`
	Description     string               `yaml:"description"`
	RecipeVersion   string               `yaml:"recipe_version"`
	Model           string               `yaml:"model"`
	ModelRevision   string               `yaml:"model_revision"`
	Runtime         string               `yaml:"runtime"`
	Container       string               `yaml:"container"`
	Command         string               `yaml:"command"`
	MinNodes        int                  `yaml:"min_nodes"`
	MaxNodes        int                  `yaml:"max_nodes"`
	Mode            string               `yaml:"mode"`
	SoloOnly        bool                 `yaml:"solo_only"`
	ClusterOnly     bool                 `yaml:"cluster_only"`
	Defaults        map[string]any       `yaml:"defaults"`
	Environment     map[string]any       `yaml:"env"`
	Metadata        sparkRunMetadata     `yaml:"metadata"`
	Mods            []string             `yaml:"mods"`
	Builder         any                  `yaml:"builder"`
	ExecutorConfig  map[string]any       `yaml:"executor_config"`
	PreExec         []any                `yaml:"pre_exec"`
	PostExec        []any                `yaml:"post_exec"`
	PostCommands    []any                `yaml:"post_commands"`
	RuntimeConfig   map[string]any       `yaml:"runtime_config"`
	Benchmark       map[string]any       `yaml:"benchmark"`
	Distribution    map[string]any       `yaml:"distribution_config"`
	Speculative     map[string]any       `yaml:"speculative_config"`
	UnknownTopLevel map[string]yaml.Node `yaml:",inline"`
}

// SparkRunPreview is returned before persistence so the UI can show exactly
// what Cloudless understood and why a recipe is or is not compatible.
type SparkRunPreview struct {
	Draft           Draft    `json:"draft"`
	Compatible      bool     `json:"compatible"`
	Warnings        []string `json:"warnings,omitempty"`
	SourceURL       string   `json:"sourceUrl,omitempty"`
	Digest          string   `json:"digest"`
	Format          string   `json:"format"`
	Unsupported     []string `json:"unsupported,omitempty"`
	Risks           []string `json:"risks,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	ProviderReady   bool     `json:"providerReady"`
	ProviderMessage string   `json:"providerMessage,omitempty"`
}

func scalarMap(label string, values map[string]any) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" || !sparkRunKey.MatchString(key) {
			return nil, fmt.Errorf("%s contains invalid key %q", label, key)
		}
		switch typed := value.(type) {
		case string:
			result[key] = typed
		case int:
			result[key] = strconv.Itoa(typed)
		case int64:
			result[key] = strconv.FormatInt(typed, 10)
		case uint64:
			result[key] = strconv.FormatUint(typed, 10)
		case float64:
			result[key] = strconv.FormatFloat(typed, 'f', -1, 64)
		case bool:
			result[key] = strconv.FormatBool(typed)
		case nil:
			result[key] = ""
		default:
			return nil, fmt.Errorf("%s value %q must be a scalar", label, key)
		}
	}
	return result, nil
}

func intDefault(values map[string]string, key string, fallback int) (int, error) {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("default %s must be an integer", key)
	}
	return parsed, nil
}

func floatDefault(values map[string]string, key string, fallback float64) (float64, error) {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("default %s must be a number", key)
	}
	return parsed, nil
}

func inferSparkRunRuntime(value, command string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "" {
		return value
	}
	command = strings.ToLower(strings.TrimSpace(command))
	switch {
	case strings.HasPrefix(command, "sglang serve"), strings.HasPrefix(command, "python -m sglang.launch_server"), strings.HasPrefix(command, "python3 -m sglang.launch_server"):
		return "sglang"
	case strings.HasPrefix(command, "llama-server"):
		return "llama-cpp"
	default:
		return "vllm"
	}
}

func cloudlessEngine(runtime string) (string, bool) {
	switch runtime {
	case "vllm", "vllm-distributed", "vllm-ray":
		return "vllm", true
	case "sglang":
		return "sglang", true
	case "llama-cpp":
		return "llama-cpp", true
	case "trtllm":
		return "trtllm", true
	default:
		return runtime, false
	}
}

func recipeName(model, served string) string {
	if served != "" {
		return served
	}
	name := path.Base(strings.SplitN(model, ":", 2)[0])
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	if name == "" || name == "." {
		return "Imported SparkRun recipe"
	}
	return name
}

func nodeRange(doc sparkRunDocument, defaults map[string]string) (int, int, int, error) {
	minNodes, maxNodes := doc.MinNodes, doc.MaxNodes
	if minNodes == 0 {
		minNodes = 1
	}
	if doc.SoloOnly || strings.EqualFold(doc.Mode, "solo") {
		minNodes, maxNodes = 1, 1
	}
	if doc.ClusterOnly || strings.EqualFold(doc.Mode, "cluster") {
		if minNodes < 2 {
			minNodes = 2
		}
	}
	if maxNodes > 0 && maxNodes < minNodes {
		return 0, 0, 0, errors.New("max_nodes cannot be smaller than min_nodes")
	}
	tp, err := intDefault(defaults, "tensor_parallel", minNodes)
	if err != nil {
		return 0, 0, 0, err
	}
	selected := minNodes
	if tp > selected {
		selected = tp
	}
	if maxNodes > 0 && selected > maxNodes {
		return 0, 0, 0, fmt.Errorf("tensor_parallel=%d exceeds max_nodes=%d", selected, maxNodes)
	}
	return selected, minNodes, maxNodes, nil
}

// ParseSparkRun converts the documented portable SparkRun YAML schema into an
// editable Cloudless draft. It does not execute, install, or import Python code.
func ParseSparkRun(data []byte, sourceURL string) (SparkRunPreview, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return SparkRunPreview{}, errors.New("SparkRun recipe must be between 1 byte and 2 MB")
	}
	var doc sparkRunDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return SparkRunPreview{}, fmt.Errorf("parse SparkRun YAML: %w", err)
	}
	digestBytes := sha256.Sum256(data)
	digest := hex.EncodeToString(digestBytes[:])
	preview := SparkRunPreview{Compatible: true, SourceURL: sourceURL, Digest: digest, Format: "sparkrun/v2", Provider: SparkRunProviderVersion}
	doc.Model, doc.ModelRevision = strings.TrimSpace(doc.Model), strings.TrimSpace(doc.ModelRevision)
	doc.Container, doc.Command = strings.TrimSpace(doc.Container), strings.TrimSpace(doc.Command)
	if doc.Model == "" || doc.Container == "" || doc.Command == "" {
		return SparkRunPreview{}, errors.New("SparkRun recipe requires model, container, and command")
	}
	defaults, err := scalarMap("defaults", doc.Defaults)
	if err != nil {
		return SparkRunPreview{}, err
	}
	environment, err := scalarMap("env", doc.Environment)
	if err != nil {
		return SparkRunPreview{}, err
	}
	runtime := inferSparkRunRuntime(doc.Runtime, doc.Command)
	if runtime == "vllm" {
		if strings.EqualFold(defaults["distributed_executor_backend"], "ray") {
			runtime = "vllm-ray"
		} else {
			runtime = "vllm-distributed"
		}
	}
	engineType, _ := cloudlessEngine(runtime)
	if len(doc.Mods) > 0 {
		preview.Risks = append(preview.Risks, "Runs registry-provided setup modifications inside inference containers")
	}
	if len(doc.RuntimeConfig) > 0 {
		preview.Risks = append(preview.Risks, "Uses runtime-specific orchestration settings")
	}
	if len(doc.Distribution) > 0 {
		preview.Risks = append(preview.Risks, "Distributes models or container images to cluster nodes")
	}
	if len(doc.Speculative) > 0 {
		preview.Risks = append(preview.Risks, "Distributes and launches an additional speculative model")
	}
	if doc.Builder != nil {
		preview.Risks = append(preview.Risks, "Uses a SparkRun image builder")
	}
	if len(doc.ExecutorConfig) > 0 {
		preview.Risks = append(preview.Risks, "Requests custom container privileges, devices, or identity")
	}
	if len(doc.PreExec) > 0 || len(doc.PostExec) > 0 {
		preview.Risks = append(preview.Risks, "Executes recipe-provided shell commands inside containers")
	}
	if len(doc.PostCommands) > 0 {
		preview.Risks = append(preview.Risks, "Executes registry-provided commands on Spark hosts")
	}
	if len(doc.UnknownTopLevel) > 0 {
		keys := make([]string, 0, len(doc.UnknownTopLevel))
		for key := range doc.UnknownTopLevel {
			keys = append(keys, key)
		}
		preview.Warnings = append(preview.Warnings, "Preserved recipe ignores unknown metadata: "+strings.Join(keys, ", "))
	}
	selectedNodes, minNodes, maxNodes, err := nodeRange(doc, defaults)
	if err != nil {
		return SparkRunPreview{}, err
	}
	port, err := intDefault(defaults, "port", 8000)
	if err != nil {
		return SparkRunPreview{}, err
	}
	context, err := intDefault(defaults, "max_model_len", 32768)
	if err != nil {
		return SparkRunPreview{}, err
	}
	sequences, err := intDefault(defaults, "max_num_seqs", 1)
	if err != nil {
		return SparkRunPreview{}, err
	}
	tp, err := intDefault(defaults, "tensor_parallel", selectedNodes)
	if err != nil {
		return SparkRunPreview{}, err
	}
	pp, err := intDefault(defaults, "pipeline_parallel", 1)
	if err != nil {
		return SparkRunPreview{}, err
	}
	gpuMemory, err := floatDefault(defaults, "gpu_memory_utilization", .8)
	if err != nil {
		return SparkRunPreview{}, err
	}
	served := strings.TrimSpace(defaults["served_model_name"])
	if served == "" {
		served = doc.Model
	}
	revision := doc.ModelRevision
	if revision == "" {
		revision = "main"
		preview.Warnings = append(preview.Warnings, "Model revision is not pinned; main may change over time.")
	}
	if !strings.Contains(doc.Container, "@sha256:") {
		preview.Warnings = append(preview.Warnings, "Container image is not digest-pinned; its tag may change over time.")
	}
	description := strings.TrimSpace(doc.Metadata.Description)
	if description == "" {
		description = strings.TrimSpace(doc.Description)
	}
	if description == "" {
		description = "Imported from a SparkRun-compatible YAML recipe."
	}
	if doc.Metadata.Maintainer != "" {
		description += " Maintainer: " + strings.TrimSpace(doc.Metadata.Maintainer) + "."
	}
	quantization := strings.ToLower(strings.TrimSpace(doc.Metadata.ModelDType))
	if quantization == "" {
		quantization = "none"
	}
	if maxNodes == 0 {
		maxNodes = 64
	}
	draft := NewDraft()
	draft.Name, draft.Description, draft.Platform = recipeName(doc.Model, served), description, "dgx-spark"
	draft.Source = Source{}
	draft.Engine = Engine{Type: engineType, Image: doc.Container, ServedModelName: served, ContainerPort: port, APIPath: "/v1", ProxyHost: "host.docker.internal", RestartPolicy: "unless-stopped"}
	draft.Model = Model{ID: doc.Model, Revision: revision, Quantization: quantization, DType: strings.TrimSpace(doc.Metadata.ModelDType), KVCacheDType: strings.TrimSpace(doc.Metadata.KVDType), MaxContext: context, MaxSequences: sequences, GPUMemoryUtilization: gpuMemory, TensorParallel: tp, PipelineParallel: pp, TrustRemoteCode: strings.Contains(doc.Command, "--trust-remote-code")}
	if draft.Model.DType == "" {
		draft.Model.DType = "auto"
	}
	if draft.Model.KVCacheDType == "" {
		draft.Model.KVCacheDType = "auto"
	}
	draft.Distributed.Nodes = selectedNodes
	draft.Runtime = Runtime{Adapter: SparkRunAdapter, WorkingDir: ".", TimeoutMinutes: 480, Prerequisites: []string{"docker"}, Environment: environment,
		SparkRun: &SparkRunRuntime{Schema: 1, SourceURL: sourceURL, SourceDigest: digest, Document: string(data), ProviderVersion: SparkRunProviderVersion, OriginalRuntime: runtime, CommandTemplate: doc.Command, Defaults: defaults, MinNodes: minNodes, MaxNodes: maxNodes, Warnings: preview.Warnings, Risks: preview.Risks}}
	draft.Health = Health{Scheme: "http", Host: "127.0.0.1", Port: port, Path: "/health", TimeoutSeconds: 600, IntervalSeconds: 3}
	preview.Draft = draft
	preview.Risks = append([]string(nil), preview.Risks...)
	return preview, nil
}

func cloneStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
