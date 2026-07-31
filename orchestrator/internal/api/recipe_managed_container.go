package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
)

// managedContainerRecipeSpec is the sole translation boundary from editable
// recipe metadata to a runtime command. No recipe-provided executable,
// entrypoint, host path, network namespace, capability, or socket crosses it.
func managedContainerRecipeSpec(recipe localrecipes.Recipe, immutableImage, name, operationID, hfToken string) (engine.RunSpec, error) {
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

	env := make(map[string]string, len(recipe.Runtime.Environment)+3)
	for key, value := range recipe.Runtime.Environment {
		switch key {
		case "HF_CACHE", "HF_HOME", "HF_TOKEN", "HUGGING_FACE_HUB_TOKEN":
			continue
		default:
			env[key] = value
		}
	}
	env["HF_HOME"] = "/root/.cache/huggingface"
	env["XDG_CACHE_HOME"] = "/root/.cache/huggingface"
	env["VLLM_CONFIG_ROOT"] = "/root/.cache/huggingface/vllm"
	if hfToken != "" {
		env["HF_TOKEN"] = hfToken
	}
	return engine.RunSpec{
		Name:  name,
		Image: immutableImage,
		Ports: map[int]int{recipe.Engine.ContainerPort: recipe.Engine.ContainerPort},
		Env:   env,
		Labels: map[string]string{
			"cloudless.recipe.operation": operationID,
			"cloudless.recipe.id":        recipe.ID,
			"cloudless.recipe.runtime":   localrecipes.ManagedContainerAdapter,
		},
		Volumes:    map[string]string{modelcache.Root(): "/root/.cache/huggingface"},
		GPUs:       "all",
		EntryPoint: "vllm",
		Args:       args,
		ReadOnly:   true,
		CapDrop:    []string{"ALL"},
		SecurityOpts: []string{
			"no-new-privileges:true",
		},
		Tmpfs:     []string{"/run:rw,nosuid,nodev,size=64m", "/tmp:rw,nosuid,nodev,size=16g"},
		PidsLimit: 8192,
		ShmSize:   "16g",
	}, nil
}
