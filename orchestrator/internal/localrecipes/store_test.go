package localrecipes

import (
	"reflect"
	"testing"
)

func TestNormalizeRepairsWindowsCheckoutFingerprints(t *testing.T) {
	recipe := recipeFromDraft(DeepSeekDSparkID, "github", "reviewed-import", "now", deepSeekDraft())
	recipe.Source.Files = deepSeekWindowsCheckoutFiles()
	recipe = normalize(recipe)
	if !reflect.DeepEqual(recipe.Source.Files, deepSeekCanonicalFiles()) {
		t.Fatalf("fingerprints were not migrated: %#v", recipe.Source.Files)
	}
}

func TestNormalizeMigratesReviewedDeepSeekAwayFromSearXNGPort(t *testing.T) {
	recipe := recipeFromDraft(DeepSeekDSparkID, "github", "reviewed-import", "now", deepSeekDraft())
	recipe.Engine.ContainerPort = 8888
	recipe.Health.Port = 8888
	recipe = normalize(recipe)
	if recipe.Engine.ContainerPort != DefaultRuntimePort || recipe.Health.Port != DefaultRuntimePort {
		t.Fatalf("ports = engine %d, health %d; want %d", recipe.Engine.ContainerPort, recipe.Health.Port, DefaultRuntimePort)
	}
	if recipe.Engine.ServedModelName != CloudlessModelAlias {
		t.Fatalf("served model = %q, want %q", recipe.Engine.ServedModelName, CloudlessModelAlias)
	}
}

func TestImportPrefillsEditableDeepSeekRecipe(t *testing.T) {
	store := New(t.TempDir())
	preview, err := store.PreviewImport(DeepSeekDSparkSource + ".git")
	if err != nil {
		t.Fatal(err)
	}
	if all, listErr := store.List(); listErr != nil || len(all) != 0 {
		t.Fatalf("preview changed the store: %#v, %v", all, listErr)
	}
	if preview.ID != DeepSeekDSparkID || preview.Source.Revision != DeepSeekDSparkRevision {
		t.Fatalf("preview = %#v", preview)
	}
	recipe, err := store.Import(DeepSeekDSparkSource + ".git")
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Source.Revision != DeepSeekDSparkRevision || recipe.Engine.Type != "vllm" || recipe.Model.TensorParallel != 2 {
		t.Fatalf("recipe = %#v", recipe)
	}
	if !recipe.Runtime.BuildOnce || !recipe.Runtime.DownloadOnce {
		t.Fatalf("DeepSeek recipe does not use coordinator-once distribution: %#v", recipe.Runtime)
	}
	if _, err := store.Import("https://github.com/example/unreviewed"); err == nil {
		t.Fatal("unknown repository was imported without an import profile")
	}
}

func TestDeleteLocalRecipe(t *testing.T) {
	store := New(t.TempDir())
	if _, err := store.Import(DeepSeekDSparkSource); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(DeepSeekDSparkID); err != nil {
		t.Fatal(err)
	}
	all, err := store.List()
	if err != nil || len(all) != 0 {
		t.Fatalf("recipes = %#v, %v", all, err)
	}
}

func TestCreateAndEditEveryRecipeSectionWithoutGitHub(t *testing.T) {
	store := New(t.TempDir())
	draft := NewDraft()
	draft.Name = "My local inference setup"
	draft.Source = Source{}
	draft.Engine = Engine{Type: "sglang", Image: "example/sglang:1", ServedModelName: "research", ContainerPort: 30000, APIPath: "/v1", ProxyHost: "engine.internal", RestartPolicy: "on-failure:4", Arguments: []string{"--enable-metrics"}}
	draft.Model = Model{ID: "local/model", Revision: "v2", Quantization: "awq", DType: "bfloat16", KVCacheDType: "fp8", MaxContext: 196608, MaxSequences: 4, GPUMemoryUtilization: .72, TensorParallel: 1, PipelineParallel: 1, TrustRemoteCode: true}
	draft.Distributed = Distributed{Nodes: 1, Backend: "nccl", MasterPort: 29500, Interface: "auto", HCA: "auto", WorkerAlias: "worker"}
	draft.Runtime.Adapter = "commands-v1"
	draft.Runtime.Environment = map[string]string{"CUSTOM": "yes"}
	draft.Runtime.Lifecycle.Start = command("docker", "run", "example/sglang:1")
	draft.Health = Health{Scheme: "http", Host: "127.0.0.1", Port: 30000, Path: "/health", TimeoutSeconds: 60, IntervalSeconds: 2}
	recipe, err := store.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Origin != "local" || recipe.Source.URL != "" || recipe.Engine.Type != "sglang" || recipe.Engine.ServedModelName != CloudlessModelAlias || recipe.Runtime.Environment["CUSTOM"] != "yes" {
		t.Fatalf("recipe was not fully authored: %#v", recipe)
	}

	draft.Engine.Type, draft.Engine.Image = "llama.cpp", "example/llama:2"
	draft.Model.Quantization, draft.Model.MaxContext = "gguf", 32768
	draft.Distributed.Nodes, draft.Distributed.Backend = 2, "mpi"
	draft.Runtime.Lifecycle.Start = command("bash", "./launch-custom.sh", "--all")
	draft.Health.Path = "/ready"
	updated, err := store.Update(recipe.ID, draft)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Engine.Type != "llama.cpp" || updated.Model.Quantization != "gguf" || updated.Distributed.Nodes != 2 || updated.Runtime.Lifecycle.Start.Args[1] != "--all" || updated.Health.Path != "/ready" {
		t.Fatalf("full recipe edits were not persisted: %#v", updated)
	}
}

func TestRecipeValidationRejectsInvalidRuntimeValues(t *testing.T) {
	store := New(t.TempDir())
	for name, mutate := range map[string]func(*Draft){
		"empty engine":     func(d *Draft) { d.Engine.Type = "" },
		"memory above one": func(d *Draft) { d.Model.GPUMemoryUtilization = 1.1 },
		"missing start":    func(d *Draft) { d.Runtime.Lifecycle.Start = Command{} },
		"escaping checksum": func(d *Draft) {
			d.Source = Source{URL: "https://example.com/repo", Revision: "main", Files: map[string]string{"../bad": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
		},
	} {
		draft := NewDraft()
		mutate(&draft)
		if _, err := store.Create(draft); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestVersionOneRecipeMigratesToCompleteSpecification(t *testing.T) {
	recipe := normalize(Recipe{ID: "legacy", Name: "Legacy", SourceURL: DeepSeekDSparkSource, Revision: DeepSeekDSparkRevision, Adapter: "dspark-vllm-mp-v1", ModelID: "legacy/model", ModelRevision: "abc", Nodes: 2})
	if recipe.Engine.Type != "vllm" || recipe.Model.ID != "legacy/model" || recipe.Distributed.Nodes != 2 || recipe.Runtime.Lifecycle.Start.Program == "" {
		t.Fatalf("migration incomplete: %#v", recipe)
	}
}

func TestNormalizeUpgradesPersistedDeepSeekRecipeToCoordinatorOnce(t *testing.T) {
	recipe := recipeFromDraft(DeepSeekDSparkID, "github", "reviewed-import", "now", deepSeekDraft())
	recipe.Runtime.BuildOnce = false
	recipe.Runtime.DownloadOnce = false
	recipe = normalize(recipe)
	if !recipe.Runtime.BuildOnce || !recipe.Runtime.DownloadOnce {
		t.Fatalf("persisted recipe was not upgraded: %#v", recipe.Runtime)
	}
}
