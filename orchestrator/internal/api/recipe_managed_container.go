package api

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
)

func managedPreparedImageReference(image engine.ImageInfo) (string, error) {
	reference := strings.ToLower(strings.TrimSpace(image.ID))
	if !recipeImageDigestPattern.MatchString(reference) {
		return "", errors.New("pulled runtime image has no immutable local content ID")
	}
	return reference, nil
}

func managedContainerCacheUser() string {
	uid, gid := os.Geteuid(), os.Getegid()
	if info, err := os.Stat(modelcache.Root()); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			gid = int(stat.Gid)
		}
	}
	return strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
}

// managedContainerRecipeSpec translates both the constrained and advanced
// container adapters. Advanced recipes may own their in-container command and
// permissions, but never gain host command execution or arbitrary host mounts.
func managedContainerRecipeSpec(recipe localrecipes.Recipe, immutableImage, name, operationID, hfTokenPath string) (engine.RunSpec, error) {
	if err := localrecipes.ValidateManagedContainerRecipe(recipe); err != nil {
		return engine.RunSpec{}, err
	}
	immutableImage, name = strings.TrimSpace(immutableImage), strings.TrimSpace(name)
	if immutableImage == "" || name == "" {
		return engine.RunSpec{}, fmt.Errorf("managed recipe image and runtime name are required")
	}
	args := []string{
		"serve", recipe.Model.ID,
		"--revision", recipe.Model.Revision,
		"--served-model-name", localrecipes.CloudlessModelAlias,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(recipe.Engine.ContainerPort),
		"--max-model-len", strconv.Itoa(recipe.Model.MaxContext),
		"--max-num-seqs", strconv.Itoa(recipe.Model.MaxSequences),
		"--gpu-memory-utilization", strconv.FormatFloat(recipe.Model.GPUMemoryUtilization, 'f', -1, 64),
		"--tensor-parallel-size", strconv.Itoa(recipe.Model.TensorParallel),
		"--pipeline-parallel-size", strconv.Itoa(recipe.Model.PipelineParallel),
	}
	if value := strings.TrimSpace(recipe.Model.Quantization); value != "" && value != "none" && value != "auto" {
		args = append(args, "--quantization", value)
	}
	if value := strings.TrimSpace(recipe.Model.DType); value != "" && value != "auto" {
		args = append(args, "--dtype", value)
	}
	if value := strings.TrimSpace(recipe.Model.KVCacheDType); value != "" && value != "auto" {
		args = append(args, "--kv-cache-dtype", value)
	}
	if recipe.Model.TrustRemoteCode {
		args = append(args, "--trust-remote-code")
	}
	args = append(args, recipe.Engine.Arguments...)
	advanced := recipe.Runtime.Adapter == localrecipes.AdvancedContainerAdapter
	if advanced && len(recipe.Engine.Command) != 0 {
		args = append([]string(nil), recipe.Engine.Command...)
	}

	env := make(map[string]string, len(recipe.Runtime.Environment)+8)
	for key, value := range recipe.Runtime.Environment {
		switch key {
		case "HF_CACHE", "HF_HOME", "HF_TOKEN", "HUGGING_FACE_HUB_TOKEN":
			continue
		default:
			env[key] = value
		}
	}
	containerCache := "/cache/huggingface"
	if advanced && strings.TrimSpace(recipe.Runtime.Container.ModelCachePath) != "" {
		containerCache = strings.TrimSpace(recipe.Runtime.Container.ModelCachePath)
	}
	env["HF_HOME"] = containerCache
	env["HOME"] = containerCache
	env["XDG_CACHE_HOME"] = containerCache
	env["VLLM_CONFIG_ROOT"] = containerCache + "/vllm"
	env["FLASHINFER_WORKSPACE_DIR"] = containerCache + "/flashinfer"
	env["PIP_CACHE_DIR"] = containerCache + "/.cloudless-runtime/pip"
	env["UV_CACHE_DIR"] = containerCache + "/.cloudless-runtime/uv"
	env["TORCH_EXTENSIONS_DIR"] = containerCache + "/.cloudless-runtime/torch-extensions"
	secrets := map[string]string{}
	if hfTokenPath != "" {
		env["HF_TOKEN_PATH"] = engine.HuggingFaceTokenContainerPath
		secrets[hfTokenPath] = engine.HuggingFaceTokenContainerPath
	}
	spec := engine.RunSpec{
		Name:        name,
		Image:       immutableImage,
		Ports:       map[int]int{recipe.Engine.ContainerPort: recipe.Engine.ContainerPort},
		Env:         env,
		SecretFiles: secrets,
		Labels: map[string]string{
			"cloudless.recipe.operation": operationID,
			"cloudless.recipe.id":        recipe.ID,
			"cloudless.recipe.runtime":   recipe.Runtime.Adapter,
		},
		Volumes:    map[string]string{modelcache.Root(): containerCache},
		GPUs:       "all",
		Network:    "cloudless",
		EntryPoint: "vllm",
		// cloudlessd owns the private model cache. Matching its unprivileged
		// identity lets the sandbox keep every Linux capability dropped.
		User:     managedContainerCacheUser(),
		Args:     args,
		ReadOnly: true,
		CapDrop:  []string{"ALL"},
		SecurityOpts: []string{
			"no-new-privileges:true",
		},
		Tmpfs:     []string{"/run:rw,nosuid,nodev,size=64m", "/tmp:rw,nosuid,nodev,size=16g"},
		PidsLimit: 8192,
		ShmSize:   "16g",
	}
	if advanced {
		container := recipe.Runtime.Container
		spec.EntryPoint = strings.TrimSpace(recipe.Engine.EntryPoint)
		spec.User = strings.TrimSpace(container.User)
		if spec.User == "" {
			spec.User = "0"
		}
		spec.ReadOnly = container.ReadOnly
		spec.CapAdd = append([]string(nil), container.CapAdd...)
		spec.CapDrop = append([]string(nil), container.CapDrop...)
		spec.SecurityOpts = nil
		spec.IPC = strings.TrimSpace(container.IPC)
		spec.Ulimits = append([]string(nil), container.Ulimits...)
		spec.Tmpfs = append([]string(nil), container.Tmpfs...)
		spec.PidsLimit = container.PidsLimit
		spec.ShmSize = strings.TrimSpace(container.ShmSize)
		if container.Infiniband {
			spec.Devices = []string{"/dev/infiniband:/dev/infiniband"}
		}
		if recipe.Distributed.Nodes > 1 {
			spec.Network = "host"
			spec.NetworkAlias = ""
			spec.Ports = nil
		}
	}
	return spec, nil
}
