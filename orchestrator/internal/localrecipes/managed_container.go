package localrecipes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const ManagedContainerAdapter = "managed-container-v1"
const AdvancedContainerAdapter = "advanced-container-v1"

const ManagedVLLMEngine = "vllm"
const ManagedSGLangEngine = "sglang"

var (
	immutableContainerImagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,430}@sha256:[0-9a-fA-F]{64}$`)
	immutableModelRevisionPattern  = regexp.MustCompile(`^[0-9a-fA-F]{40}(?:[0-9a-fA-F]{24})?$`)
	containerSizePattern           = regexp.MustCompile(`^[1-9][0-9]*(?:[kKmMgG])?$`)
)

var managedVLLMBooleanArguments = map[string]struct{}{
	"--disable-log-requests":      {},
	"--disable-sliding-window":    {},
	"--enable-chunked-prefill":    {},
	"--enable-prefix-caching":     {},
	"--enforce-eager":             {},
	"--enable-reasoning":          {},
	"--disable-custom-all-reduce": {},
}

var managedSGLangBooleanArguments = map[string]struct{}{
	"--disable-cuda-graph":            {},
	"--disable-radix-cache":           {},
	"--enable-metrics":                {},
	"--enable-torch-compile":          {},
	"--allow-auto-truncate":           {},
	"--enable-fp32-lm-head":           {},
	"--disable-shared-experts-fusion": {},
}

var managedContainerEnvironment = map[string]map[string]struct{}{
	ManagedVLLMEngine: {
		"HF_HUB_DISABLE_XET":            {},
		"VLLM_ALLOW_LONG_MAX_MODEL_LEN": {},
	},
	ManagedSGLangEngine: {
		"HF_HUB_DISABLE_XET":                        {},
		"SGLANG_ALLOW_OVERWRITE_LONGER_CONTEXT_LEN": {},
		"SGLANG_JIT_DEEPGEMM_PRECOMPILE":            {},
		"SGLANG_ENABLE_SPEC_V2":                     {},
		"FLASHINFER_DISABLE_VERSION_CHECK":          {},
	},
}

func managedContainerEngine(value string) (string, bool) {
	engine := strings.ToLower(strings.TrimSpace(value))
	_, ok := managedContainerEnvironment[engine]
	return engine, ok
}

func managedBooleanArguments(engine string) map[string]struct{} {
	if engine == ManagedSGLangEngine {
		return managedSGLangBooleanArguments
	}
	return managedVLLMBooleanArguments
}

// ValidateManagedContainerRecipe proves that an editable recipe can be
// represented without executing repository code, host commands, SSH, arbitrary
// mounts, or a Docker socket. The runtime broker remains responsible for
// synthesizing the complete container command and security boundary.
func ValidateManagedContainerRecipe(recipe Recipe) error {
	return validateManagedContainerDraft(DraftFromRecipe(recipe))
}

func IsContainerAdapter(adapter string) bool {
	switch strings.TrimSpace(adapter) {
	case ManagedContainerAdapter, AdvancedContainerAdapter:
		return true
	default:
		return false
	}
}

func containerRuntimeConfigured(value ContainerRuntime) bool {
	return value.User != "" || value.ReadOnly || value.IPC != "" || value.ShmSize != "" ||
		len(value.Ulimits) != 0 || len(value.CapAdd) != 0 || len(value.CapDrop) != 0 ||
		len(value.Tmpfs) != 0 || value.PidsLimit != 0 || value.ModelCachePath != "" ||
		value.Memory != "" || value.MemorySwap != "" || value.Infiniband
}

func validateManagedContainerDraft(d Draft) error {
	adapter := strings.TrimSpace(d.Runtime.Adapter)
	if !IsContainerAdapter(adapter) {
		return fmt.Errorf("runtime adapter must be %q or %q", ManagedContainerAdapter, AdvancedContainerAdapter)
	}
	if d.Source.URL != "" || d.Source.Revision != "" || len(d.Source.Files) != 0 {
		return errors.New("managed container recipes cannot include source repositories")
	}
	if !immutableContainerImagePattern.MatchString(strings.TrimSpace(d.Engine.Image)) {
		return errors.New("managed container recipes require an image pinned as repository@sha256:<digest>")
	}
	managedEngine, supportedManagedEngine := managedContainerEngine(d.Engine.Type)
	if strings.TrimSpace(d.Engine.Type) == "" || adapter == ManagedContainerAdapter && !supportedManagedEngine {
		return errors.New("managed-container-v1 requires vLLM or SGLang; advanced-container-v1 may declare another OpenAI-compatible engine")
	}
	if d.Engine.ServedModelName != CloudlessModelAlias || d.Engine.APIPath != "/v1" {
		return errors.New("managed container recipes must preserve the Cloudless model and /v1 API contract")
	}
	if d.Engine.ProxyHost != "host.docker.internal" {
		return errors.New("managed container recipes must use the Cloudless host gateway")
	}
	if !immutableModelRevisionPattern.MatchString(strings.TrimSpace(d.Model.Revision)) {
		return errors.New("managed container recipes require an immutable 40- or 64-character model revision")
	}
	for _, dependency := range d.Model.Dependencies {
		if strings.TrimSpace(dependency.ID) == "" || !immutableModelRevisionPattern.MatchString(strings.TrimSpace(dependency.Revision)) {
			return errors.New("advanced container model dependencies require an ID and immutable 40- or 64-character revision")
		}
	}
	if adapter == ManagedContainerAdapter && (d.Distributed.Nodes != 1 || len(d.Distributed.SelectedNodes) != 0 || d.Runtime.BuildOnce || d.Runtime.DownloadOnce) {
		return errors.New("managed-container-v1 supports one local node; use advanced-container-v1 for a distributed container")
	}
	if adapter == AdvancedContainerAdapter && d.Distributed.Nodes > 1 {
		if d.Distributed.Nodes > 8 {
			return errors.New("distributed advanced containers support two through eight DGX Sparks")
		}
		if d.Model.TensorParallel != d.Distributed.Nodes || d.Model.PipelineParallel != 1 {
			return errors.New("distributed advanced containers require tensor parallelism equal to node count and pipeline parallelism 1")
		}
		if d.Platform != "dgx-spark" || !d.Runtime.BuildOnce || !d.Runtime.DownloadOnce {
			return errors.New("distributed advanced containers require DGX Spark and coordinator-owned image and model distribution")
		}
		if d.Distributed.Backend != "nccl" || d.Runtime.Container.IPC != "host" || !d.Runtime.Container.Infiniband {
			return errors.New("distributed advanced containers require NCCL, host IPC, and the bounded InfiniBand device permission")
		}
	}
	if len(d.Runtime.Prerequisites) != 0 {
		return errors.New("managed container recipes cannot request host prerequisites")
	}
	if d.Runtime.WorkingDir != "" && d.Runtime.WorkingDir != "." {
		return errors.New("managed container recipes cannot select a host working directory")
	}
	for label, command := range map[string]Command{
		"build": d.Runtime.Lifecycle.Build, "download": d.Runtime.Lifecycle.Download,
		"start": d.Runtime.Lifecycle.Start, "stop": d.Runtime.Lifecycle.Stop,
	} {
		if command.Program != "" || len(command.Args) != 0 {
			return fmt.Errorf("managed container recipes cannot provide a %s command", label)
		}
	}
	if adapter == ManagedContainerAdapter {
		if d.Engine.EntryPoint != "" || len(d.Engine.Command) != 0 || len(d.Model.Dependencies) != 0 || containerRuntimeConfigured(d.Runtime.Container) {
			return errors.New("managed-container-v1 cannot override the container command or security profile; use advanced-container-v1")
		}
		for _, argument := range d.Engine.Arguments {
			if _, ok := managedBooleanArguments(managedEngine)[argument]; !ok {
				return fmt.Errorf("%s argument %q is not available in the constrained recipe runtime", managedEngine, argument)
			}
		}
	} else {
		if len(d.Engine.Command) == 0 && strings.TrimSpace(d.Engine.EntryPoint) != "" {
			return errors.New("advanced container entryPoint requires an explicit command")
		}
		if strings.ContainsRune(d.Engine.EntryPoint, '\x00') {
			return errors.New("advanced container entryPoint contains an invalid byte")
		}
		container := d.Runtime.Container
		if container.ModelCachePath != "" && !strings.HasPrefix(container.ModelCachePath, "/") {
			return errors.New("advanced container modelCachePath must be absolute")
		}
		if container.Memory != "" && !containerSizePattern.MatchString(strings.TrimSpace(container.Memory)) {
			return errors.New("advanced container memory limit is invalid")
		}
		if container.MemorySwap != "" && !containerSizePattern.MatchString(strings.TrimSpace(container.MemorySwap)) {
			return errors.New("advanced container memory-swap limit is invalid")
		}
		if container.MemorySwap != "" && container.Memory == "" {
			return errors.New("advanced container memory-swap limit requires a memory limit")
		}
	}
	if d.Health.Scheme != "http" || d.Health.Host != "127.0.0.1" || d.Health.Port != d.Engine.ContainerPort {
		return errors.New("managed container health checks must use the managed loopback port over HTTP")
	}
	return nil
}
