package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func TestPrepareRecipeComposeAddsRestartPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.dspark.yml")
	source := "services:\n  vllm-dspark:\n    image: ${DSPARK_VLLM_IMAGE:-vllm-dspark-runtime:clean}\n    network_mode: host\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareRecipeCompose(dir, "on-failure:5"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "restart: on-failure:5") {
		t.Fatal("managed restart policy was not added")
	}
}

func TestRecipeRuntimeUsesEditableEngineModelClusterAndEnvironment(t *testing.T) {
	draft := localrecipes.NewDraft()
	draft.Source = localrecipes.Source{}
	draft.Engine.Type = "sglang"
	draft.Engine.Image = "example/sglang:custom"
	draft.Engine.ServedModelName = "served-custom"
	draft.Engine.ContainerPort = 31000
	draft.Engine.ProxyHost = "engine.internal"
	draft.Engine.RestartPolicy = "on-failure:5"
	draft.Engine.Arguments = []string{"--custom-flag", "custom-value"}
	draft.Model.ID = "example/custom-model"
	draft.Model.Revision = "custom-revision"
	draft.Model.MaxContext = 77777
	draft.Model.MaxSequences = 9
	draft.Model.GPUMemoryUtilization = .63
	draft.Model.TensorParallel = 3
	draft.Model.PipelineParallel = 2
	draft.Distributed.Nodes = 1
	draft.Distributed.MasterPort = 29999
	draft.Runtime.Environment["CUSTOM_RECIPE_VALUE"] = "present"
	store := localrecipes.New(t.TempDir())
	recipe, err := store.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	env, workdir, err := writeRecipeRuntime(recipe, checkout, sparkcluster.State{})
	if err != nil {
		t.Fatal(err)
	}
	if workdir != checkout {
		t.Fatalf("workdir = %q", workdir)
	}
	for key, want := range map[string]string{
		"ENGINE_TYPE": "sglang", "ENGINE_IMAGE": "example/sglang:custom", "SERVED_MODEL_NAME": "served-custom",
		"ENGINE_PORT": "31000", "DSPARK_MODEL": "example/custom-model", "DSPARK_MODEL_REVISION": "custom-revision",
		"MAX_MODEL_LEN": "77777", "MAX_NUM_SEQS": "9", "GPU_MEMORY_UTILIZATION": "0.63",
		"TENSOR_PARALLEL_SIZE": "3", "PIPELINE_PARALLEL_SIZE": "2", "MASTER_PORT": "29999", "CUSTOM_RECIPE_VALUE": "present",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q", key, env[key], want)
		}
	}
	data, err := os.ReadFile(filepath.Join(checkout, ".env.dspark"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "CUSTOM_RECIPE_VALUE=present") || !strings.Contains(string(data), "MAX_MODEL_LEN=77777") {
		t.Fatalf("runtime env file did not include editable settings:\n%s", data)
	}
}

func TestPrepareRecipeModelPinMakesBothDownloadsImmutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prepare-dspark-model-cache.sh")
	source := `docker run --rm -i \
    -e DSPARK_MODEL="$DSPARK_MODEL" \
    -e HF_DOWNLOAD_WORKERS="$HF_DOWNLOAD_WORKERS" \
    image -c 'snapshot_download(os.environ["DSPARK_MODEL"], max_workers=1)'
docker run --rm -i \
    -e DSPARK_MODEL="$DSPARK_MODEL" \
    image -c 'snapshot_download(os.environ["DSPARK_MODEL"], local_files_only=True)'
`
	if err := os.WriteFile(path, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{ModelRevision: "immutable-model-revision"}
	if err := prepareRecipeModelPin(recipe, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, `DSPARK_MODEL_REVISION="$DSPARK_MODEL_REVISION"`) != 2 {
		t.Fatalf("model revision was not passed into both containers:\n%s", text)
	}
	if strings.Count(text, `revision=os.environ["DSPARK_MODEL_REVISION"]`) != 2 {
		t.Fatalf("model revision was not applied to both downloads:\n%s", text)
	}
}

func TestRecipeSourceDocumentEscapesUntrustedReadme(t *testing.T) {
	recipe := localrecipes.Recipe{
		Name: "Reviewed <recipe>", SourceURL: "https://github.com/example/repo",
		Revision: "1234567890abcdef", ModelRevision: "abcdef1234567890",
	}
	document := recipeSourceDocument(recipe, `<script>alert("bad")</script>`)
	for _, want := range []string{"Reviewed &lt;recipe&gt;", "Recipe 1234567890ab", "Model abcdef123456", "&lt;script&gt;alert"} {
		if !strings.Contains(document, want) {
			t.Fatalf("source document is missing %q", want)
		}
	}
	if strings.Contains(document, `<script>alert`) {
		t.Fatal("source document rendered untrusted README HTML")
	}
}

func TestRecipeUIUsesRecipesIconAndCloudlessSourceViewer(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`id="ui-recipes"`,
		`${uiIcon('recipes')}<span>Recipes</span>`,
		`<h3>Your Recipes</h3>`,
		`.recipe-import .btn, .recipe-actions .btn`,
		`'/api/recipes/' + encodeURIComponent(card.dataset.recipeId) + '/source'`,
		`['Engine', 'Inference server and container']`,
		`['Model', 'Model, memory and parallelism']`,
		`['Runtime', 'Commands and environment']`,
		`rb-engine-type`,
		`rb-runtime-env`,
		`recipeCommandFields('start','Start inference step'`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("recipe UI is missing %q", want)
		}
	}
	if strings.Contains(page, `${uiIcon('workflows')}<span>Local recipes</span>`) {
		t.Fatal("recipe tab still uses the missing workflows icon or old name")
	}
	if strings.Contains(page, "Recipe templates") || strings.Contains(page, "Built-in template") {
		t.Fatal("recipe editor still exposes the removed template workflow")
	}
}
