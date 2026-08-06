package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestManagedPreparedImageReferenceUsesLocalContentID(t *testing.T) {
	const localID = "sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"
	reference, err := managedPreparedImageReference(engine.ImageInfo{
		ID:          localID,
		RepoDigests: []string{"ghcr.io/example/runtime@sha256:a83948492cf13df455170fb42885f5ef4db54fefe0feff0f841ecbff464ac9d8"},
	})
	if err != nil {
		t.Fatalf("prepared image reference: %v", err)
	}
	if reference != localID {
		t.Fatalf("prepared image reference = %q, want local content ID %q", reference, localID)
	}
}

func TestManagedPreparedImageReferenceRejectsMissingContentID(t *testing.T) {
	if _, err := managedPreparedImageReference(engine.ImageInfo{}); err == nil {
		t.Fatal("expected missing local content ID to be rejected")
	}
}

func TestValidateManagedRuntimeImageIdentitiesKeepsRegistryAndContentSeparate(t *testing.T) {
	const registryDigest = "sha256:a83948492cf13df455170fb42885f5ef4db54fefe0feff0f841ecbff464ac9d8"
	const contentID = "sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"
	err := validateManagedRuntimeImageIdentities(registryDigest, registryDigest, contentID, engine.ImageInfo{
		ID:          contentID,
		RepoDigests: []string{"ghcr.io/example/runtime@" + registryDigest},
	})
	if err != nil {
		t.Fatalf("validate dual image identity: %v", err)
	}
}

func TestValidateManagedRuntimeImageIdentitiesRejectsContentIDAsRegistryDigest(t *testing.T) {
	const registryDigest = "sha256:a83948492cf13df455170fb42885f5ef4db54fefe0feff0f841ecbff464ac9d8"
	const contentID = "sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"
	err := validateManagedRuntimeImageIdentities(registryDigest, contentID, contentID, engine.ImageInfo{ID: contentID})
	if err == nil || !strings.Contains(err.Error(), "registry image differs") {
		t.Fatalf("expected registry/content conflation to fail, got %v", err)
	}
}

func TestValidateManagedRuntimeImageIdentitiesRejectsUnboundContent(t *testing.T) {
	const registryDigest = "sha256:a83948492cf13df455170fb42885f5ef4db54fefe0feff0f841ecbff464ac9d8"
	const contentID = "sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"
	err := validateManagedRuntimeImageIdentities(registryDigest, registryDigest, contentID, engine.ImageInfo{ID: contentID})
	if err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("expected missing registry binding to fail, got %v", err)
	}
}

func TestRecoveryRecipeSnapshotPreservesSignedRegistryReference(t *testing.T) {
	recipe := managedContainerRecipeForTest()
	const contentID = "sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"
	server := &Server{}
	recovered, err := server.recoveryRecipeSnapshot(recipeops.Operation{
		RecipeSnapshot:         recipe,
		PreparedImageReference: contentID,
	})
	if err != nil {
		t.Fatalf("recover recipe snapshot: %v", err)
	}
	if recovered.Engine.Image != recipe.Engine.Image {
		t.Fatalf("recovery replaced signed registry reference %q with execution reference %q", recipe.Engine.Image, recovered.Engine.Image)
	}
	spec, err := managedContainerRecipeSpec(recovered, contentID, "cloudless-recipe-recovery", "operation-recovery", "")
	if err != nil {
		t.Fatalf("build recovered execution spec: %v", err)
	}
	if spec.Image != contentID {
		t.Fatalf("recovered execution image = %q, want %q", spec.Image, contentID)
	}
}

func managedContainerRecipeForTest() localrecipes.Recipe {
	return localrecipes.Recipe{
		ID: "local-managed", Name: "Managed", Platform: "generic",
		Engine: localrecipes.Engine{
			Type: "vllm", Image: "registry.example/vllm@sha256:" + strings.Repeat("a", 64),
			ServedModelName: "cloudless", ContainerPort: 8890, APIPath: "/v1",
			ProxyHost: "host.docker.internal", Arguments: []string{"--enable-prefix-caching"},
		},
		Model: localrecipes.Model{
			ID: "example/model", Revision: strings.Repeat("b", 40), MaxContext: 32768,
			MaxSequences: 2, GPUMemoryUtilization: .75, TensorParallel: 1, PipelineParallel: 1,
		},
		Distributed: localrecipes.Distributed{Nodes: 1, MasterPort: 25000, WorkerAlias: "worker"},
		Runtime: localrecipes.Runtime{
			Adapter: localrecipes.ManagedContainerAdapter, WorkingDir: ".", TimeoutMinutes: 120,
			Environment: map[string]string{"HF_CACHE": "evil", "HF_TOKEN": "recipe-secret", "VLLM_ALLOW_LONG_MAX_MODEL_LEN": "1"},
		},
		Health: localrecipes.Health{
			Scheme: "http", Host: "127.0.0.1", Port: 8890, Path: "/health",
			TimeoutSeconds: 180, IntervalSeconds: 3,
		},
	}
}

func TestManagedContainerRecipeSpecOwnsSecurityAndContract(t *testing.T) {
	t.Setenv("CLOUDLESS_MODEL_CACHE", t.TempDir())
	recipe := managedContainerRecipeForTest()
	spec, err := managedContainerRecipeSpec(recipe, "sha256:"+strings.Repeat("c", 64), "cloudless-recipe-test", "operation-test", "/var/lib/cloudless/huggingface-token")
	if err != nil {
		t.Fatal(err)
	}
	if spec.EntryPoint != "vllm" || spec.Network != "cloudless" || spec.IPC != "" || len(spec.ExtraHosts) != 0 {
		t.Fatalf("unsafe or unexpected runtime scaffold: %#v", spec)
	}
	if !spec.ReadOnly || !reflect.DeepEqual(spec.CapDrop, []string{"ALL"}) ||
		!reflect.DeepEqual(spec.SecurityOpts, []string{"no-new-privileges:true"}) ||
		spec.PidsLimit != 8192 {
		t.Fatalf("sandbox controls missing: %#v", spec)
	}
	if spec.Volumes[modelcache.Root()] != "/cache/huggingface" || len(spec.Volumes) != 1 {
		t.Fatalf("unexpected mounts: %#v", spec.Volumes)
	}
	if spec.User != managedContainerCacheUser() {
		t.Fatalf("managed runtime user does not match the cache owner: %q", spec.User)
	}
	if spec.Labels["cloudless.recipe.operation"] != "operation-test" {
		t.Fatalf("operation ownership label missing: %#v", spec.Labels)
	}
	if spec.Env["HF_TOKEN"] != "" || spec.Env["HF_TOKEN_PATH"] != "/run/secrets/cloudless-huggingface-token" || spec.Env["HF_HOME"] != "/cache/huggingface" ||
		spec.Env["HOME"] != "/cache/huggingface" || spec.Env["FLASHINFER_WORKSPACE_DIR"] != "/cache/huggingface/flashinfer" ||
		spec.Env["PIP_CACHE_DIR"] != "/cache/huggingface/.cloudless-runtime/pip" || spec.Env["UV_CACHE_DIR"] != "/cache/huggingface/.cloudless-runtime/uv" ||
		spec.Env["TORCH_EXTENSIONS_DIR"] != "/cache/huggingface/.cloudless-runtime/torch-extensions" ||
		spec.Env["HF_CACHE"] != "" || spec.Env["VLLM_ALLOW_LONG_MAX_MODEL_LEN"] != "1" {
		t.Fatalf("credential/environment mediation failed: %#v", spec.Env)
	}
	if spec.SecretFiles["/var/lib/cloudless/huggingface-token"] != "/run/secrets/cloudless-huggingface-token" || len(spec.SecretFiles) != 1 {
		t.Fatalf("protected token mount missing: %#v", spec.SecretFiles)
	}
	command := strings.Join(spec.Args, " ")
	for _, expected := range []string{
		"serve example/model", "--revision " + strings.Repeat("b", 40),
		"--served-model-name cloudless", "--host 0.0.0.0", "--port 8890",
		"--enable-prefix-caching",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("managed command %q is missing %q", command, expected)
		}
	}
}

func TestManagedSGLangRecipeSpecUsesSGLangContract(t *testing.T) {
	t.Setenv("CLOUDLESS_MODEL_CACHE", t.TempDir())
	recipe := managedContainerRecipeForTest()
	recipe.Engine.Type = localrecipes.ManagedSGLangEngine
	recipe.Engine.Image = "registry.example/sglang@sha256:" + strings.Repeat("d", 64)
	recipe.Engine.Arguments = []string{"--allow-auto-truncate", "--enable-fp32-lm-head"}
	recipe.Runtime.Environment = map[string]string{
		"SGLANG_ALLOW_OVERWRITE_LONGER_CONTEXT_LEN": "1",
		"SGLANG_ENABLE_SPEC_V2":                     "1",
	}
	spec, err := managedContainerRecipeSpec(recipe, "sha256:"+strings.Repeat("e", 64), "cloudless-sglang-test", "operation-sglang", "")
	if err != nil {
		t.Fatal(err)
	}
	if spec.EntryPoint != "python3" || !spec.ReadOnly || spec.Network != "cloudless" {
		t.Fatalf("managed SGLang scaffold = %#v", spec)
	}
	command := strings.Join(spec.Args, " ")
	for _, expected := range []string{
		"-m sglang.launch_server", "--model-path example/model",
		"--revision " + strings.Repeat("b", 40), "--served-model-name cloudless",
		"--host 0.0.0.0", "--port 8890", "--context-length 32768",
		"--max-running-requests 2", "--mem-fraction-static 0.75",
		"--tp-size 1", "--pp-size 1", "--allow-auto-truncate", "--enable-fp32-lm-head",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("managed SGLang command %q is missing %q", command, expected)
		}
	}
	if spec.Env["SGLANG_ALLOW_OVERWRITE_LONGER_CONTEXT_LEN"] != "1" || spec.Env["SGLANG_ENABLE_SPEC_V2"] != "1" {
		t.Fatalf("managed SGLang environment was not preserved: %#v", spec.Env)
	}
}

func TestAdvancedContainerRecipeSpecPreservesDeclaredCommandAndPermissions(t *testing.T) {
	t.Setenv("CLOUDLESS_MODEL_CACHE", t.TempDir())
	recipe := managedContainerRecipeForTest()
	recipe.Runtime.Adapter = localrecipes.AdvancedContainerAdapter
	recipe.Engine.Arguments = nil
	recipe.Engine.EntryPoint = "/bin/bash"
	recipe.Engine.Command = []string{"-lc", "exec vllm serve pinned-model"}
	recipe.Model.Dependencies = []localrecipes.ModelDependency{{ID: "example/draft", Revision: strings.Repeat("d", 40), Role: "speculative-draft"}}
	recipe.Runtime.Environment["CUTE_DSL_ARCH"] = "sm_121a"
	recipe.Runtime.Container = localrecipes.ContainerRuntime{
		User: "0", IPC: "host", ShmSize: "32g", Ulimits: []string{"memlock=-1:-1"},
		CapAdd: []string{"IPC_LOCK"}, ModelCachePath: "/root/.cache/huggingface", Memory: "100g", MemorySwap: "100g",
	}
	spec, err := managedContainerRecipeSpec(recipe, recipe.Engine.Image, "cloudless-recipe-advanced", "operation-advanced", "")
	if err != nil {
		t.Fatal(err)
	}
	if spec.EntryPoint != "/bin/bash" || !reflect.DeepEqual(spec.Args, recipe.Engine.Command) || spec.User != "0" || spec.ReadOnly || spec.IPC != "host" ||
		!reflect.DeepEqual(spec.CapAdd, []string{"IPC_LOCK"}) || spec.Volumes[modelcache.Root()] != "/root/.cache/huggingface" ||
		spec.Env["CUTE_DSL_ARCH"] != "sm_121a" || spec.Memory != "100g" || spec.MemorySwap != "100g" {
		t.Fatalf("advanced runtime declaration was not preserved: %#v", spec)
	}
}

func TestManagedContainerRecipeNodeSpecInjectsDistributedFabric(t *testing.T) {
	t.Setenv("CLOUDLESS_MODEL_CACHE", t.TempDir())
	recipe := managedContainerRecipeForTest()
	recipe.Runtime.Adapter = localrecipes.AdvancedContainerAdapter
	recipe.Engine.EntryPoint = "/bin/bash"
	recipe.Engine.Command = []string{"-lc", "exec vllm serve model --node-rank $NODE_RANK"}
	recipe.Engine.Arguments = nil
	recipe.Model.TensorParallel = 2
	recipe.Platform = "dgx-spark"
	recipe.Distributed.Nodes = 2
	recipe.Distributed.Backend = "nccl"
	recipe.Runtime.BuildOnce = true
	recipe.Runtime.DownloadOnce = true
	recipe.Runtime.Container.IPC = "host"
	recipe.Runtime.Container.Infiniband = true
	spec, err := managedContainerRecipeNodeSpec(recipe, recipe.Engine.Image, "cloudless-recipe-node", "operation-node", "", 1,
		"10.100.0.2", "enP2p1s0f1np1", "rocep1s0f1", "10.100.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network != "host" || spec.Env["NODE_RANK"] != "1" || spec.Env["HEADLESS"] != "1" ||
		spec.Env["MASTER_ADDR"] != "10.100.0.1" || len(spec.Devices) != 1 {
		t.Fatalf("distributed node spec = %#v", spec)
	}
}
