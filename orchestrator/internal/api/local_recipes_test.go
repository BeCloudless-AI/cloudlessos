package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func TestLocalRecipeAbortCancelsRegisteredLaunch(t *testing.T) {
	server := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	job := jobs.NewManager().Create("recipe:local-0123456789abcdef")
	server.registerRecipeJob("local-0123456789abcdef", cancel, job)
	request := httptest.NewRequest(http.MethodPost, "/api/recipes/local-0123456789abcdef/abort", nil)
	request.SetPathValue("id", "local-0123456789abcdef")
	recorder := httptest.NewRecorder()
	server.localRecipeAbort(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("recipe context was not cancelled")
	}
	if state := job.Snapshot(); state.Phase != "stopping" {
		t.Fatalf("job phase = %q, want stopping", state.Phase)
	}
}

func TestRunRecipeCommandCancelTerminatesProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	job := jobs.NewManager().Create("recipe:test")
	done := make(chan error, 1)
	go func() {
		done <- runRecipeCommand(ctx, job, "building", "Build", t.TempDir(), nil, "bash", "-c", "sleep 60 & child=$!; echo $child > '"+pidFile+"'; wait")
	}()
	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child process did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled command did not stop")
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("child process %d survived cancellation", childPID)
	}
}

func TestGitHubRecipePreviewDoesNotPersist(t *testing.T) {
	store := localrecipes.New(t.TempDir())
	server := &Server{recipes: store}
	request := httptest.NewRequest(http.MethodPost, "/api/recipes/import/preview", strings.NewReader(`{"sourceUrl":"`+localrecipes.DeepSeekDSparkSource+`"}`))
	recorder := httptest.NewRecorder()
	server.localRecipeImportPreview(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"kind":"cloudless"`) {
		t.Fatalf("preview = %d: %s", recorder.Code, recorder.Body.String())
	}
	recipes, err := store.List()
	if err != nil || len(recipes) != 0 {
		t.Fatalf("preview persisted recipes: %#v, %v", recipes, err)
	}
}

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
	env, workdir, err := writeRecipeRuntime(recipe, checkout, sparkcluster.State{}, true)
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

func TestRecipeRuntimeUsesFabricSSHAndDisablesDuplicatePeerWork(t *testing.T) {
	draft := localrecipes.NewDraft()
	draft.Runtime.BuildOnce = true
	draft.Runtime.DownloadOnce = true
	store := localrecipes.New(t.TempDir())
	recipe, err := store.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	cluster := sparkcluster.State{
		NodeCount: 2, LocalIPs: []string{"10.100.0.1"}, LocalLinks: []string{"enp1s0f0np0"},
		Nodes: []sparkcluster.Node{{Name: "peer", Host: "192.168.50.94", Username: "ledomaine", IPs: []string{"10.100.0.2"}}},
	}
	env, _, err := writeRecipeRuntime(recipe, checkout, cluster, false)
	if err != nil {
		t.Fatal(err)
	}
	if env["WORKER_BUILD"] != "0" || env["PREPARE_WORKER"] != "0" {
		t.Fatalf("peer work was not disabled: WORKER_BUILD=%q PREPARE_WORKER=%q", env["WORKER_BUILD"], env["PREPARE_WORKER"])
	}
	config, err := os.ReadFile(filepath.Join(env["HOME"], ".ssh", "config"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, want := range []string{"HostName 10.100.0.2", "HostKeyAlias 192.168.50.94"} {
		if !strings.Contains(text, want) {
			t.Fatalf("SSH config is missing %q:\n%s", want, text)
		}
	}
}

func TestRecipeSSHCommandPreservesRemoteArguments(t *testing.T) {
	env := map[string]string{"HOME": "/tmp/cloudless home", "PATH": "/usr/bin"}
	cmd := recipeSSHCommand(context.Background(), t.TempDir(), env, recipePeer{Alias: "worker"},
		"docker", "run", "-c", `test -d "$1"`, "argument with spaces", "it's-literal")
	want := `'docker' 'run' '-c' 'test -d "$1"' 'argument with spaces' 'it'"'"'s-literal'`
	if got := cmd.Args[len(cmd.Args)-1]; got != want {
		t.Fatalf("remote command = %q, want %q", got, want)
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
		`<h3>Recipe Library</h3>`,
		`.recipe-actions .btn`,
		`.recipe-actions { display: grid; grid-template-columns: repeat(3,minmax(0,1fr));`,
		`function recipeGitHubURL(value)`,
		`function recipeImportRequest(sourceValue)`,
		`function openRecipeImport()`,
		`let mmRecipeView = { q: '', source: 'all', sparks: 'all', status: 'all', sort: 'updated', page: 1, perPage: 6 }`,
		`function recipeMatchesView(recipe, data)`,
		`function recipeConnectedSparks(data)`,
		`function recipeNeedsMoreSparks(recipe, data)`,
		`function recipeSparkFilterOptions(recipes, data)`,
		`function recipeSortView(recipes)`,
		`function recipePageButtons(current, total)`,
		`aria-label="Search recipes"`,
		`'recipe-source-filter'`,
		`'recipe-sparks-filter'`,
		`'recipe-status-filter'`,
		`'recipe-sort'`,
		`id="recipe-per-page"`,
		`data-recipe-page=`,
		`.recipe-pagination`,
		`.recipe-specs`,
		`.recipe-primary-action`,
		`/api/recipes/import/preview`,
		`<span>Import recipe</span>`,
		`<span>Check</span>`,
		`'/api/recipes/' + encodeURIComponent(id) + '/check'`,
		"`https://github.com/${path}`",
		`source.onkeydown = event => { if (event.key === 'Enter')`,
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
	if strings.Contains(page, `placeholder="https://github.com/owner/repository"`) {
		t.Fatal("recipe import still asks users to type the fixed GitHub prefix")
	}
	for _, removed := range []string{`<span>Prefill from GitHub</span>`, `id="recipe-import-toggle"`} {
		if strings.Contains(page, removed) {
			t.Fatalf("duplicate recipe import UI still contains %q", removed)
		}
	}
}

func TestRecipeUIFiltersSortsAndPaginatesRenderedCollection(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`const filtered = recipeSortView(recipes.filter(recipe => recipeMatchesView(recipe, data)))`,
		`Math.ceil(total / mmRecipeView.perPage)`,
		`filtered.slice(start, start + mmRecipeView.perPage)`,
		`view.source !== 'all'`,
		`view.sparks !== 'all' && nodes !== Number(view.sparks)`,
		`recipeNeedsMoreSparks(recipe, data)`,
		`recipeSparkFilterOptions(recipes,data)`,
		`class="recipe-card'`,
		`' insufficient-cluster'`,
		`Requires ' + nodes + ' DGX Sparks. ' + connected`,
		`.recipe-card.insufficient-cluster .recipe-actions [data-recipe-edit]`,
		`.recipe-card.insufficient-cluster .recipe-actions [data-recipe-delete]`,
		`view.status === 'active'`,
		`view.status === 'ready'`,
		`view.status === 'attention'`,
		`mmRecipeView.sort === 'name'`,
		`mmRecipeView.sort === 'nodes'`,
		`mmRecipeView.sort === 'context'`,
		`recipePageButtons(mmRecipeView.page, pages)`,
		`mmRecipeView.perPage=Number(event.currentTarget.value)||6`,
		`c.closest('.mm-body')?.scrollTo({top:0,behavior:'smooth'})`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("recipe collection UI is missing %q", want)
		}
	}
}

func TestRecipeCheckUsesStableProgressOverlay(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function openRecipeCheckOverlay(recipe, trigger)`,
		`function updateRecipeCheckOverlay(update)`,
		`function finishRecipeCheckOverlay(ok, message)`,
		`class="recipe-check-overlay"`,
		`aria-label="Recipe check progress"`,
		`Cloudless is validating this recipe without launching or downloading the model.`,
		`This is a read-only check. The active model and running services are not changed.`,
		`trackLocalRecipeJob(result.jobId, 'check');`,
		`if(checking)updateRecipeCheckOverlay(update);else if(recipeId)updateRecipeRunCard(recipeId,jobId,update)`,
		`html.motion-disabled .recipe-check-spinner::before`,
		`.recipe-check-error.hidden { display: none; }`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("recipe check UI is missing %q", want)
		}
	}
	if strings.Contains(page, `trackLocalRecipeJob(result.jobId, 'check');mmRecipes=null;renderLocalRecipes(c)`) {
		t.Fatal("recipe check still repaints the entire manager after starting")
	}
}

func TestRecipeRunProgressUpdatesWithoutRepaintingManager(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function updateRecipeRunCard(recipeId, jobId, update = {})`,
		`function markRecipeAbortPending(button)`,
		`function openRecipeLaunchDialog(recipe, plan, trigger)`,
		`Specialized inference runtime required`,
		`standard Cloudless inference engine`,
		`Completed runtime images and model weights remain cached.`,
		`const RECIPE_RUN_STAGES = [`,
		`function recipeRunPhaseLabel(phase)`,
		`class="recipe-progress-stages"`,
		`aria-label="Current recipe operation"`,
		`data.plans&&data.plans[id]`,
		`trackLocalRecipeJob(result.jobId,'run',id)`,
		`action.dataset.liveRecipeAbort = recipeId`,
		`update.phase === 'canceled'`,
		`message.textContent = update.message || 'Starting recipe…'`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("stable recipe run UI is missing %q", want)
		}
	}
	if strings.Contains(page, `refreshTimer = window.setTimeout`) {
		t.Fatal("recipe runs still poll by repainting the whole manager")
	}
}

func TestRecipeManagerOnlyShowsLocalLibrary(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function recipeLibraryHeaderHTML(local)`,
		`<h3>Recipe Library</h3>`,
		`Manage inference recipes saved on this machine.`,
		`paintLocalRecipes(c, mmRecipes)`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("local recipe UI is missing %q", want)
		}
	}
	for _, removed := range []string{"Discover recipes", "/api/recipe-catalog", "mmRecipeCatalog", "paintCatalogRecipes", "data-catalog-install"} {
		if strings.Contains(page, removed) {
			t.Fatalf("recipe discovery UI still contains %q", removed)
		}
	}
}
