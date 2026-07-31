package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func runTestGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func allowUnreviewedRecipeCommandsForTest(t *testing.T) {
	t.Helper()
	previous := recipeExecutionPolicyEvaluator
	recipeExecutionPolicyEvaluator = func(localrecipes.Recipe) recipeExecutionDecision {
		return recipeExecutionDecision{Allowed: true, Mode: "test-only"}
	}
	t.Cleanup(func() { recipeExecutionPolicyEvaluator = previous })
}

func TestRecipeCheckCheckoutIsOperationScoped(t *testing.T) {
	stateDir := t.TempDir()
	operation := recipeops.Operation{
		ID:             "rop-0123456789abcdef0123456789abcdef",
		RecipeID:       "local-0123456789abcdef",
		RecipeRevision: "sha256:" + strings.Repeat("a", 64),
	}
	checkout, err := recipeCheckCheckout(stateDir, operation)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(stateDir, "recipe-checks", operation.ID)
	if checkout != want {
		t.Fatalf("check checkout = %q, want %q", checkout, want)
	}
	if checkout == recipeCheckout(localrecipes.Recipe{ID: operation.RecipeID}) {
		t.Fatal("check checkout aliases the active runtime checkout")
	}
	operation.ID = "../escape"
	if _, err := recipeCheckCheckout(stateDir, operation); err == nil {
		t.Fatal("unsafe operation ID was accepted")
	}
}

func TestPrepareRecipeSourceAtRejectsChangedReviewedFile(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "source.git")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "init", "--quiet")
	runTestGit(t, repository, "config", "user.email", "test@cloudless.invalid")
	runTestGit(t, repository, "config", "user.name", "Cloudless Test")
	if err := os.WriteFile(filepath.Join(repository, "launch.sh"), []byte("#!/bin/sh\necho reviewed\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "add", "launch.sh")
	runTestGit(t, repository, "commit", "--quiet", "-m", "reviewed source")
	revision := runTestGit(t, repository, "rev-parse", "HEAD")

	recipe := localrecipes.Recipe{
		Source: localrecipes.Source{
			URL:      "file://" + strings.TrimSuffix(repository, ".git"),
			Revision: revision,
			Files:    map[string]string{"launch.sh": strings.Repeat("0", 64)},
		},
	}
	checkout := filepath.Join(root, "check")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	job := jobs.NewManager().Create("recipe-check:test")
	_, err := prepareRecipeSourceAt(context.Background(), job, recipe, checkout)
	if err == nil || !strings.Contains(err.Error(), "reviewed file changed") {
		t.Fatalf("prepare error = %v, want reviewed checksum rejection", err)
	}
}

func TestValidateRecipeLifecycleCommandsChecksRenderedScripts(t *testing.T) {
	workdir := t.TempDir()
	script := filepath.Join(workdir, "start.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{Runtime: localrecipes.Runtime{Lifecycle: localrecipes.Lifecycle{
		Start: localrecipes.Command{Program: "bash", Args: []string{"./start.sh"}},
		Stop:  localrecipes.Command{Program: "/bin/true"},
	}}}
	if err := validateRecipeLifecycleCommands(recipe, workdir); err != nil {
		t.Fatalf("valid rendered lifecycle rejected: %v", err)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if err := validateRecipeLifecycleCommands(recipe, workdir); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing rendered script error = %v", err)
	}
}

func TestValidateRecipeLifecycleCommandsRejectsEscapingScript(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.sh"), []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{Runtime: localrecipes.Runtime{Lifecycle: localrecipes.Lifecycle{
		Start: localrecipes.Command{Program: "bash", Args: []string{"../outside.sh"}},
	}}}
	err := validateRecipeLifecycleCommands(recipe, workdir)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping script error = %v", err)
	}
}

func TestLocalRecipeAbortCancelsRegisteredLaunch(t *testing.T) {
	server := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	job := jobs.NewManager().Create("recipe:local-0123456789abcdef")
	server.registerRecipeJob("local-0123456789abcdef", "rop-test", cancel, job)
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

func TestUnregisterRecipeJobDoesNotDeleteNewerOperation(t *testing.T) {
	server := &Server{}
	first := jobs.NewManager().Create("recipe:first")
	second := jobs.NewManager().Create("recipe:second")
	_, cancelFirst := context.WithCancel(context.Background())
	_, cancelSecond := context.WithCancel(context.Background())
	defer cancelFirst()
	defer cancelSecond()
	server.registerRecipeJob("local-0123456789abcdef", "rop-first", cancelFirst, first)
	server.registerRecipeJob("local-0123456789abcdef", "rop-second", cancelSecond, second)
	server.unregisterRecipeJob("local-0123456789abcdef", "rop-first")
	server.recipeJobsMu.Lock()
	control, ok := server.recipeJobs["local-0123456789abcdef"]
	server.recipeJobsMu.Unlock()
	if !ok || control.operationID != "rop-second" || control.job != second {
		t.Fatalf("newer operation control was removed: %#v", control)
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
	reapDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(reapDeadline) {
		if err := syscall.Kill(childPID, 0); err != nil {
			return
		}
		// Minimal test containers may not run an init process that promptly reaps
		// orphaned children. A zombie has exited and cannot keep doing work, which
		// is the behavior this cancellation test is intended to verify.
		if stat, err := os.ReadFile("/proc/" + strconv.Itoa(childPID) + "/stat"); err == nil && strings.Contains(string(stat), ") Z ") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child process %d survived cancellation", childPID)
}

func TestRunRecipeCommandFailureIncludesLastOutputLine(t *testing.T) {
	job := jobs.NewManager().Create("recipe:test")
	err := runRecipeCommand(context.Background(), job, "starting", "Start inference", t.TempDir(), nil, "bash", "-c", "echo preparing; echo 'undefined volume cloudless-hf' >&2; exit 1")
	if err == nil || !strings.Contains(err.Error(), "undefined volume cloudless-hf") {
		t.Fatalf("run error = %v, want final command output", err)
	}
}

func TestConsumeRecipeCommandOutputDrainsOversizedLines(t *testing.T) {
	payload := append(bytes.Repeat([]byte("x"), 2*1024*1024), '\n')
	payload = append(payload, []byte("finished\n")...)
	lines := make([]string, 0, 2)
	if err := consumeRecipeCommandOutput(bytes.NewReader(payload), func(line string) { lines = append(lines, line) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("bounded lines = %q", lines)
	}
	if len(lines[0]) > 4100 || !strings.HasSuffix(lines[0], "...") || lines[1] != "finished" {
		t.Fatalf("bounded lines = lengths/content %d, %q", len(lines[0]), lines)
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

func TestUnreviewedRecipeCannotReachCheckOrRunExecution(t *testing.T) {
	store := localrecipes.New(t.TempDir())
	recipe, err := store.Create(localrecipes.NewDraft())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{recipes: store}
	for _, endpoint := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"check", server.localRecipeCheck},
		{"run", server.localRecipeRun},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/recipes/"+recipe.ID+"/"+endpoint.name, nil)
			request.SetPathValue("id", recipe.ID)
			recorder := httptest.NewRecorder()
			endpoint.handler(recorder, request)
			if recorder.Code != http.StatusForbidden ||
				!strings.Contains(recorder.Body.String(), `"action":"review-permissions"`) {
				t.Fatalf("%s response = %d: %s", endpoint.name, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestConfiguredRecipeCommandBlocksInternalUnreviewedReplay(t *testing.T) {
	recipe := localrecipes.Recipe{ID: "local-0123456789abcdef", Origin: "local", Trust: "local-custom"}
	command := localrecipes.Command{Program: "/bin/true"}
	err := runConfiguredRecipeCommand(
		context.Background(), jobs.NewManager().Create("recipe:test"), "recovering", "Replay",
		t.TempDir(), nil, recipe, command,
	)
	if err == nil || !strings.Contains(err.Error(), "not Cloudless-signed") {
		t.Fatalf("internal unreviewed command error = %v", err)
	}
}

func TestPrepareRecipeComposeAddsRestartPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.dspark.yml")
	source := "services:\n  vllm-dspark:\n    image: ${DSPARK_VLLM_IMAGE:-vllm-dspark-runtime:clean}\n    network_mode: host\n    volumes:\n      - ${HF_CACHE:-${HOME}/.cache/huggingface}:/cache/huggingface\n    command:\n      - bash\n      - -lc\n      - >\n        exec vllm serve model\n        --port 8888\n"
	source = strings.Replace(source, "        --port 8888\n", "        --host ${VLLM_HOST:-127.0.0.1}\n        --port 8888\n", 1)
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
	if !strings.Contains(string(data), "${HF_CACHE:-/var/lib/cloudless/models-cache}:/cache/huggingface") ||
		strings.Contains(string(data), "external: true") {
		t.Fatal("managed host model-cache mount was not added")
	}
	if !strings.Contains(string(data), "--port ${ENGINE_PORT:-8890}") {
		t.Fatal("managed recipe engine port was not added")
	}
	if !strings.Contains(string(data), "--host ${VLLM_HOST:-0.0.0.0}") || strings.Contains(string(data), "--host ${VLLM_HOST:-127.0.0.1}") {
		t.Fatal("managed recipe engine is not reachable from the stable proxy")
	}
}

func TestPrepareRecipeComposeRejectsUnknownBindLayout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.dspark.yml")
	source := "services:\n  vllm-dspark:\n    image: ${DSPARK_VLLM_IMAGE:-vllm-dspark-runtime:clean}\n    volumes:\n      - ${HF_CACHE:-${HOME}/.cache/huggingface}:/cache/huggingface\n    command:\n      - --host 0.0.0.0\n      - --port 8888\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareRecipeCompose(dir, "unless-stopped"); err == nil || !strings.Contains(err.Error(), "engine-host layout changed") {
		t.Fatalf("prepareRecipeCompose error = %v, want fail-closed host-layout error", err)
	}
}

func TestRecipeRuntimeCheckoutIsPersistent(t *testing.T) {
	got := recipeCheckout(localrecipes.Recipe{ID: "local-0123456789abcdef"})
	want := filepath.Join(recipeRuntimeRoot, "local-0123456789abcdef")
	if got != want || strings.HasPrefix(got, "/tmp/") || strings.HasPrefix(got, "/var/tmp/") {
		t.Fatalf("recipeCheckout = %q, want persistent %q", got, want)
	}
}

func TestPrepareRecipeWorkerCheckoutUsesEnrolledUserPath(t *testing.T) {
	dir := t.TempDir()
	start := `#!/bin/bash
SCRIPT_DIR="$(pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$SCRIPT_DIR/docker-compose.dspark.yml}"
echo "${WORKER_HOST}:${SCRIPT_DIR}"
ssh "$WORKER_HOST" "mkdir -p '$SCRIPT_DIR'"
scp file "${WORKER_HOST}:${SCRIPT_DIR}/file"
ssh "$WORKER_HOST" "cd '$SCRIPT_DIR' && docker compose up -d"
`
	stop := `#!/bin/bash
SCRIPT_DIR="$(pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$SCRIPT_DIR/docker-compose.dspark.yml}"
ssh "$WORKER_HOST" "cd '$SCRIPT_DIR' && docker compose down"
`
	if err := os.WriteFile(filepath.Join(dir, "start-deepseek-v4-flash-dspark.sh"), []byte(start), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stop-deepseek-v4-flash-dspark.sh"), []byte(stop), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := prepareRecipeWorkerCheckout(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"start-deepseek-v4-flash-dspark.sh", "stop-deepseek-v4-flash-dspark.sh"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, `WORKER_CHECKOUT="${WORKER_CHECKOUT:-$SCRIPT_DIR}"`) || !strings.Contains(text, "cd '$WORKER_CHECKOUT'") {
			t.Fatalf("%s did not receive worker checkout guardrail:\n%s", name, text)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "start-deepseek-v4-flash-dspark.sh"))
	if !strings.Contains(string(data), "${WORKER_HOST}:${WORKER_CHECKOUT}") || !strings.Contains(string(data), "mkdir -p '$WORKER_CHECKOUT'") {
		t.Fatalf("start script still copies to coordinator checkout:\n%s", data)
	}
}

func TestRecipePeerCheckoutIsPersistentAndUserWritable(t *testing.T) {
	got, err := recipePeerCheckout("local-0123456789abcdef", "ledomaine")
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/ledomaine/.local/share/cloudless/recipes-runtime/local-0123456789abcdef"
	if got != want {
		t.Fatalf("recipePeerCheckout = %q, want %q", got, want)
	}
	if _, err := recipePeerCheckout("local-0123456789abcdef", "../root"); err == nil {
		t.Fatal("unsafe worker username was accepted")
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
	env, workdir, err := writeRecipeRuntime(recipe, checkout, sparkcluster.State{}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if workdir != checkout {
		t.Fatalf("workdir = %q", workdir)
	}
	for key, want := range map[string]string{
		"ENGINE_TYPE": "sglang", "ENGINE_IMAGE": "example/sglang:custom", "SERVED_MODEL_NAME": "cloudless",
		"VLLM_HOST":   "0.0.0.0",
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

func TestRecipeRuntimePersistsOperationOwnership(t *testing.T) {
	draft := localrecipes.NewDraft()
	draft.Source = localrecipes.Source{}
	draft.Distributed.Nodes = 1
	store := localrecipes.New(t.TempDir())
	recipe, err := store.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := recipeops.RecipeRevision(recipe)
	if err != nil {
		t.Fatal(err)
	}
	operation := recipeops.Operation{
		ID: "rop-0123456789abcdef0123456789abcdef", Kind: recipeops.KindRun,
		RecipeID: recipe.ID, RecipeRevision: revision, Phase: recipeops.PhasePreparing,
	}
	checkout := t.TempDir()
	env, _, err := writeRecipeRuntime(recipe, checkout, sparkcluster.State{}, true, &operation)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"CLOUDLESS_RECIPE_OPERATION_ID": operation.ID,
		"CLOUDLESS_RECIPE_REVISION":     revision,
		"COMPOSE_PROJECT_NAME":          "cloudless-recipe-0123456789abcdef",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q", key, env[key], want)
		}
	}
	data, err := os.ReadFile(filepath.Join(checkout, ".env.dspark"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"CLOUDLESS_RECIPE_OPERATION_ID": operation.ID,
		"CLOUDLESS_RECIPE_REVISION":     revision,
		"COMPOSE_PROJECT_NAME":          "cloudless-recipe-0123456789abcdef",
	} {
		if !strings.Contains(string(data), key+"="+want+"\n") {
			t.Fatalf("runtime environment is missing %s:\n%s", key, data)
		}
	}
	wrapper, err := os.ReadFile(filepath.Join(env["HOME"], "bin", "docker"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrapper), `--label "cloudless.recipe.operation=${CLOUDLESS_RECIPE_OPERATION_ID}"`) ||
		!strings.Contains(string(wrapper), `--label "cloudless.recipe.revision=${CLOUDLESS_RECIPE_REVISION:-unknown}"`) {
		t.Fatalf("Docker wrapper does not label direct helper containers:\n%s", wrapper)
	}
}

func TestCloseActiveRecipeOperationPreservesRunIdentity(t *testing.T) {
	dir := t.TempDir()
	store := localrecipes.New(dir)
	draft := localrecipes.NewDraft()
	draft.Source = localrecipes.Source{}
	draft.Distributed.Nodes = 1
	recipe, err := store.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	run, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []recipeops.Phase{
		recipeops.PhasePrepared, recipeops.PhaseSwitching, recipeops.PhaseStarting,
		recipeops.PhaseVerifying, recipeops.PhaseActive,
	} {
		run, err = operations.Transition(run.ID, phase, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{recipeOps: operations}
	if err := server.closeActiveRecipeOperation(recipe.ID, nil); err != nil {
		t.Fatal(err)
	}
	closed, ok := operations.Get(run.ID)
	if !ok || closed.Phase != recipeops.PhaseStopped {
		t.Fatalf("closed run operation = %#v, %v", closed, ok)
	}
	if _, ok := operations.ActiveForRecipe(recipe.ID); ok {
		t.Fatal("stopped run remains active")
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
	env, _, err := writeRecipeRuntime(recipe, checkout, cluster, false, nil)
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

func TestRecipeCommandsPropagateOwnershipToLocalAndPeerHelpers(t *testing.T) {
	env := map[string]string{
		"HOME": "/tmp/cloudless-home", "PATH": "/usr/bin",
		"CLOUDLESS_RECIPE_OPERATION_ID": "rop-owned", "CLOUDLESS_RECIPE_REVISION": "sha256:revision",
	}
	local := recipeLocalCommand(context.Background(), t.TempDir(), env, "docker", "run", "--rm", "runtime")
	localArgs := strings.Join(local.Args, " ")
	for _, want := range []string{"--label cloudless.recipe.operation=rop-owned", "--label cloudless.recipe.revision=sha256:revision"} {
		if !strings.Contains(localArgs, want) {
			t.Fatalf("local helper is missing %q: %s", want, localArgs)
		}
	}
	peer := recipeSSHCommand(context.Background(), t.TempDir(), env, recipePeer{Alias: "worker"}, "docker", "run", "--rm", "runtime")
	remote := peer.Args[len(peer.Args)-1]
	for _, want := range []string{"'/usr/bin/env' 'CLOUDLESS_RECIPE_OPERATION_ID=rop-owned'", "'--label' 'cloudless.recipe.operation=rop-owned'"} {
		if !strings.Contains(remote, want) {
			t.Fatalf("peer helper is missing %q: %s", want, remote)
		}
	}
	rsync := strings.Join(recipeRsyncOwnershipArgs(env), " ")
	if !strings.Contains(rsync, "CLOUDLESS_RECIPE_OPERATION_ID='rop-owned'") || !strings.Contains(rsync, "CLOUDLESS_RECIPE_REVISION='sha256:revision'") {
		t.Fatalf("rsync peer process has no ownership identity: %s", rsync)
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
	if strings.Count(text, `HF_TOKEN="${HF_TOKEN:-}"`) != 2 {
		t.Fatalf("the optional Hugging Face account was not passed into both containers:\n%s", text)
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
		`.recipe-actions { display: grid; grid-template-columns: repeat(2,minmax(0,1fr));`,
		`.recipe-actions .recipe-remove { grid-column: 2; }`,
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
		`trust.executionAllowed === true`,
		`Execution blocked`,
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
		`function finishRecipeCheckOverlay(ok, message, outcome = '')`,
		`class="recipe-check-overlay"`,
		`aria-label="Recipe check progress"`,
		`Cloudless is collecting launch evidence without loading the model or downloading its weights.`,
		`A small bounded fabric transfer is used for multi-Spark recipes.`,
		`'checking-image':2`,
		`'checking-capacity':4`,
		`'checking-accelerators':5`,
		`'checking-fabric':7`,
		`outcome === 'validated-with-warnings'`,
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

func TestRecipeCardsExposeDurablePreflightEvidence(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function recipeCurrentPreflight(recipe, data)`,
		`operation.phase==='prepared'`,
		`operation.recipeRevision===revision`,
		`function recipePreflightHTML(recipe, data)`,
		`Not checked for this recipe revision`,
		`Launch evidence verified`,
		`Requirements checked with warnings`,
		`recipePreflightHTML(recipe,data)`,
		`.recipe-preflight-item.warning i`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("recipe preflight UI is missing %q", want)
		}
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
		`aria-label="Overall recipe launch progress"`,
		`data.plans&&data.plans[id]`,
		`trackLocalRecipeJob(result.jobId,'run',id)`,
		`action.dataset.liveRecipeAbort = recipeId`,
		`update.phase === 'canceled'`,
		`message.textContent = recipeProgressMessage(update)`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("stable recipe run UI is missing %q", want)
		}
	}
	if strings.Contains(page, `refreshTimer = window.setTimeout`) {
		t.Fatal("recipe runs still poll by repainting the whole manager")
	}
}

func TestRecipeProgressFallsBackToDurableOperationJournal(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function recipeOperationWorking(operation)`,
		`function recipeOperationAsJob(operation)`,
		`function recipeLiveJobs(data)`,
		`overallPercent:Number.isFinite(overall)?overall:0`,
		`Cloudless is reconciling this operation after a service restart.`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("durable recipe progress UI is missing %q", want)
		}
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
