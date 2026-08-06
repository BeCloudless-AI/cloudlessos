package localrecipes

import (
	"strings"
	"testing"
)

const testManagedImage = "registry.example/cloudless/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func managedContainerTestDraft() Draft {
	d := NewDraft()
	d.Source = Source{}
	d.Engine.Image = testManagedImage
	d.Engine.ServedModelName = CloudlessModelAlias
	d.Engine.ProxyHost = "host.docker.internal"
	d.Engine.Arguments = []string{"--enable-prefix-caching"}
	d.Model.Revision = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	d.Model.TensorParallel = 1
	d.Distributed.Nodes = 1
	d.Distributed.SelectedNodes = nil
	d.Runtime = Runtime{
		Adapter:        ManagedContainerAdapter,
		WorkingDir:     ".",
		TimeoutMinutes: 120,
		Environment:    map[string]string{"HF_HUB_DISABLE_XET": "1"},
	}
	d.Health = Health{
		Scheme: "http", Host: "127.0.0.1", Port: d.Engine.ContainerPort,
		Path: "/health", TimeoutSeconds: 180, IntervalSeconds: 3,
	}
	return d
}

func TestManagedContainerDraftIsAcceptedWithoutLifecycleCommands(t *testing.T) {
	validated, err := validateDraft(managedContainerTestDraft())
	if err != nil {
		t.Fatalf("validate managed container draft: %v", err)
	}
	if validated.Runtime.Lifecycle.Start.Program != "" {
		t.Fatal("managed runtime unexpectedly gained a host command")
	}
}

func TestManagedContainerDraftAcceptsSGLangWithoutCommandOverride(t *testing.T) {
	draft := managedContainerTestDraft()
	draft.Engine.Type = ManagedSGLangEngine
	draft.Engine.Arguments = []string{"--allow-auto-truncate", "--enable-fp32-lm-head"}
	draft.Runtime.Environment = map[string]string{
		"SGLANG_ALLOW_OVERWRITE_LONGER_CONTEXT_LEN": "1",
		"SGLANG_ENABLE_SPEC_V2":                     "1",
	}
	if _, err := validateDraft(draft); err != nil {
		t.Fatalf("validate managed SGLang draft: %v", err)
	}
}

func TestManagedContainerDraftRejectsMutableOrExecutableInputs(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Draft)
		want string
	}{
		{"mutable image", func(d *Draft) { d.Engine.Image = "vllm/vllm-openai:latest" }, "pinned"},
		{"mutable model", func(d *Draft) { d.Model.Revision = "main" }, "immutable"},
		{"source", func(d *Draft) { d.Source.URL, d.Source.Revision = "https://github.com/x/y", strings.Repeat("c", 40) }, "source repositories"},
		{"command", func(d *Draft) { d.Runtime.Lifecycle.Start = Command{Program: "bash", Args: []string{"run.sh"}} }, "start command"},
		{"host prerequisite", func(d *Draft) { d.Runtime.Prerequisites = []string{"ssh"} }, "host prerequisites"},
		{"multiple nodes", func(d *Draft) { d.Distributed.Nodes = 2 }, "one local node"},
		{"contract override", func(d *Draft) { d.Engine.Arguments = []string{"--port"} }, "not available"},
		{"unsupported engine", func(d *Draft) { d.Engine.Type = "unknown-engine" }, "vLLM or SGLang"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			draft := managedContainerTestDraft()
			tc.edit(&draft)
			_, err := validateDraft(draft)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestAdvancedContainerDraftAllowsPinnedAuxiliaryModelsAndContainerCommand(t *testing.T) {
	draft := managedContainerTestDraft()
	draft.Runtime.Adapter = AdvancedContainerAdapter
	draft.Engine.EntryPoint = "/bin/bash"
	draft.Engine.Command = []string{"-lc", "exec vllm serve model --speculative-config '{\"method\":\"dflash\"}'"}
	draft.Engine.Arguments = nil
	draft.Model.Dependencies = []ModelDependency{{
		ID: "example/dflash", Revision: strings.Repeat("d", 40), Role: "speculative-draft",
	}}
	draft.Runtime.Environment["CUTE_DSL_ARCH"] = "sm_121a"
	draft.Runtime.Container = ContainerRuntime{
		User: "0", IPC: "host", ShmSize: "32g", Ulimits: []string{"memlock=-1:-1"},
		CapAdd: []string{"IPC_LOCK"}, ModelCachePath: "/root/.cache/huggingface", Memory: "100g", MemorySwap: "100g",
	}
	if _, err := validateDraft(draft); err != nil {
		t.Fatalf("validate advanced container draft: %v", err)
	}
	draft.Runtime.Container.MemorySwap, draft.Runtime.Container.Memory = "100g", ""
	if _, err := validateDraft(draft); err == nil || !strings.Contains(err.Error(), "requires a memory limit") {
		t.Fatalf("memory-swap without memory error = %v", err)
	}
}

func TestAdvancedContainerDraftStillRequiresPinnedArtifacts(t *testing.T) {
	draft := managedContainerTestDraft()
	draft.Runtime.Adapter = AdvancedContainerAdapter
	draft.Model.Dependencies = []ModelDependency{{ID: "example/dflash", Revision: "main"}}
	if _, err := validateDraft(draft); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("mutable auxiliary model error = %v", err)
	}
}

func TestAdvancedContainerDraftAllowsBoundedDistributedSparkRuntime(t *testing.T) {
	draft := managedContainerTestDraft()
	draft.Runtime.Adapter = AdvancedContainerAdapter
	draft.Engine.EntryPoint = "/bin/bash"
	draft.Engine.Command = []string{"-lc", "exec vllm serve model --nnodes 2 --node-rank $NODE_RANK"}
	draft.Engine.Arguments = nil
	draft.Model.TensorParallel = 2
	draft.Model.PipelineParallel = 1
	draft.Distributed.Nodes = 2
	draft.Distributed.Backend = "nccl"
	draft.Runtime.BuildOnce = true
	draft.Runtime.DownloadOnce = true
	draft.Runtime.Container = ContainerRuntime{IPC: "host", Infiniband: true}
	if _, err := validateDraft(draft); err != nil {
		t.Fatalf("validate distributed advanced container: %v", err)
	}
	draft.Runtime.Container.Infiniband = false
	if _, err := validateDraft(draft); err == nil || !strings.Contains(err.Error(), "InfiniBand") {
		t.Fatalf("missing InfiniBand permission error = %v", err)
	}
}
