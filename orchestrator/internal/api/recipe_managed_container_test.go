package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
)

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
			Environment: map[string]string{"HF_CACHE": "evil", "HF_TOKEN": "recipe-secret", "SAFE": "yes"},
		},
		Health: localrecipes.Health{
			Scheme: "http", Host: "127.0.0.1", Port: 8890, Path: "/health",
			TimeoutSeconds: 180, IntervalSeconds: 3,
		},
	}
}

func TestManagedContainerRecipeSpecOwnsSecurityAndContract(t *testing.T) {
	recipe := managedContainerRecipeForTest()
	spec, err := managedContainerRecipeSpec(recipe, "sha256:"+strings.Repeat("c", 64), "cloudless-recipe-test", "operation-test", "account-token")
	if err != nil {
		t.Fatal(err)
	}
	if spec.EntryPoint != "vllm" || spec.Network != "" || spec.IPC != "" || len(spec.ExtraHosts) != 0 {
		t.Fatalf("unsafe or unexpected runtime scaffold: %#v", spec)
	}
	if !spec.ReadOnly || !reflect.DeepEqual(spec.CapDrop, []string{"ALL"}) ||
		!reflect.DeepEqual(spec.SecurityOpts, []string{"no-new-privileges:true"}) ||
		spec.PidsLimit != 8192 {
		t.Fatalf("sandbox controls missing: %#v", spec)
	}
	if spec.Volumes[modelcache.Root()] != "/root/.cache/huggingface" || len(spec.Volumes) != 1 {
		t.Fatalf("unexpected mounts: %#v", spec.Volumes)
	}
	if spec.Labels["cloudless.recipe.operation"] != "operation-test" {
		t.Fatalf("operation ownership label missing: %#v", spec.Labels)
	}
	if spec.Env["HF_TOKEN"] != "account-token" || spec.Env["HF_HOME"] != "/root/.cache/huggingface" ||
		spec.Env["HF_CACHE"] != "" || spec.Env["SAFE"] != "yes" {
		t.Fatalf("credential/environment mediation failed: %#v", spec.Env)
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
