package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/state"
)

// TestRecipeLifecycleHTTPHelper is launched as a child process by the real
// recipe lifecycle command runner. It gives the matrix a native process with
// the operation identity, a private health endpoint, and the exact OpenAI
// model contract without introducing a second production test seam.
func TestRecipeLifecycleHTTPHelper(t *testing.T) {
	if os.Getenv("GO_WANT_RECIPE_HTTP_HELPER") != "1" {
		return
	}
	port, err := strconv.Atoi(os.Getenv("RECIPE_HELPER_PORT"))
	if err != nil || port < 1 || port > 65535 {
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"cloudless","object":"model"}]}`))
	})
	if err := http.ListenAndServe(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), mux); err != nil {
		os.Exit(3)
	}
}

type recipeMatrixEngine struct {
	engine.Engine
	mu        sync.Mutex
	proxy     *engine.Container
	listener  net.Listener
	digest    string
	port      int
	modelRoot string
}

func (e *recipeMatrixEngine) Pull(context.Context, string) error { return nil }

func (e *recipeMatrixEngine) InspectImage(_ context.Context, image string) (engine.ImageInfo, error) {
	return engine.ImageInfo{
		ID: e.digest, OS: "linux", Architecture: runtime.GOARCH, Size: 1,
		RepoDigests: []string{strings.Split(image, "@")[0] + "@" + e.digest},
	}, nil
}

func (e *recipeMatrixEngine) VolumeMountpoint(context.Context, string) (string, error) {
	return e.modelRoot, nil
}

func (e *recipeMatrixEngine) Run(_ context.Context, spec engine.RunSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(e.port)))
	if err != nil {
		return "", err
	}
	e.listener = listener
	e.proxy = &engine.Container{ID: "recipe-matrix-proxy", Name: spec.Name, Image: spec.Image, State: "running", Status: "Up"}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"cloudless","object":"model"}]}`))
	})}
	go func() { _ = server.Serve(listener) }()
	return e.proxy.ID, nil
}

func (e *recipeMatrixEngine) Remove(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.proxy != nil && (name == e.proxy.Name || name == e.proxy.ID) {
		if e.listener != nil {
			_ = e.listener.Close()
		}
		e.listener, e.proxy = nil, nil
	}
	return nil
}

func (e *recipeMatrixEngine) Find(_ context.Context, name string) (*engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.proxy != nil && (name == e.proxy.Name || name == e.proxy.ID) {
		copy := *e.proxy
		return &copy, nil
	}
	return nil, nil
}

func (e *recipeMatrixEngine) List(context.Context) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.proxy == nil {
		return nil, nil
	}
	return []engine.Container{*e.proxy}, nil
}

func (e *recipeMatrixEngine) Output(_ context.Context, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "ps" {
		return "", nil
	}
	return "", nil
}

func (e *recipeMatrixEngine) ContainerNamesByLabel(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func (e *recipeMatrixEngine) ImageDigest(_ context.Context, _ string) (string, error) {
	return e.digest, nil
}

func TestDestructiveRecipeFailureMatrixRollsBackToExactSafeState(t *testing.T) {
	boundaries := []recipeBoundary{
		recipeBoundaryRuntimeStart,
		recipeBoundaryPrivateHealth,
		recipeBoundaryProxyPull,
		recipeBoundaryProxyCreate,
		recipeBoundaryPromotion,
		recipeBoundaryStateCommit,
	}
	for _, boundary := range boundaries {
		t.Run(string(boundary), func(t *testing.T) {
			runDestructiveRecipeFailureCase(t, boundary)
		})
	}
}

func runDestructiveRecipeFailureCase(t *testing.T, boundary recipeBoundary) {
	t.Helper()
	allowUnreviewedRecipeCommandsForTest(t)
	oldRoot, oldCompatibilityDocker, oldStablePort := recipeRuntimeRoot, recipeDockerCompatibilityExecutable, recipeStablePort
	recipeRuntimeRoot = t.TempDir()
	modelRoot := t.TempDir()
	t.Setenv("CLOUDLESS_MODEL_CACHE", modelRoot)
	recipeStablePort = reserveRecipeMatrixPort(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	recipeDockerCompatibilityExecutable = writeRecipeMatrixDocker(t, modelRoot, digest)
	t.Cleanup(func() {
		recipeRuntimeRoot, recipeDockerCompatibilityExecutable, recipeStablePort = oldRoot, oldCompatibilityDocker, oldStablePort
	})

	privatePort := reserveRecipeMatrixPort(t)
	start, stop := writeRecipeMatrixLifecycle(t)
	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "Lifecycle matrix", Platform: "dgx-spark",
		Engine: localrecipes.Engine{
			Type: "vllm", Image: "example.invalid/runtime:latest", ContainerPort: privatePort,
			ProxyHost: "host.docker.internal", ServedModelName: localrecipes.CloudlessModelAlias, APIPath: "/v1",
		},
		Health:      localrecipes.Health{Scheme: "http", Host: "127.0.0.1", Port: privatePort, Path: "/health", TimeoutSeconds: 5, IntervalSeconds: 1},
		Model:       localrecipes.Model{ID: "example/model", Revision: "abc123"},
		Distributed: localrecipes.Distributed{Nodes: 1, MasterPort: privatePort + 1},
		Runtime: localrecipes.Runtime{
			WorkingDir: ".", TimeoutMinutes: 1,
			Environment: map[string]string{"GO_WANT_RECIPE_HTTP_HELPER": "1", "RECIPE_HELPER_PORT": strconv.Itoa(privatePort)},
			Lifecycle: localrecipes.Lifecycle{
				Start: localrecipes.Command{Program: start},
				Stop:  localrecipes.Command{Program: stop},
			},
		},
	}
	writeRecipeMatrixModelCache(t, recipe, modelRoot)

	dir := t.TempDir()
	operations, err := recipeops.Open(filepath.Join(dir, "operations"))
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	previous := state.InferenceRuntime{Engine: "vllm", Model: "stable/model", EngineUnloaded: true, ExecutionMode: "local"}
	if err := stateStore.CommitInferenceRuntime(previous); err != nil {
		t.Fatal(err)
	}
	prepareRecipeMatrixPreflight(t, operations, recipe, digest)
	run, err := operations.BeginRunValidatedWithPrevious(recipe, recipeops.PreviousRuntime{
		Engine: previous.Engine, Model: previous.Model, EngineUnloaded: previous.EngineUnloaded,
		ExecutionMode: previous.ExecutionMode, LocalRecipeID: previous.LocalRecipeID,
	})
	if err != nil {
		t.Fatal(err)
	}

	matrixEngine := &recipeMatrixEngine{digest: digest, port: recipeStablePort, modelRoot: modelRoot}
	server := &Server{
		eng: matrixEngine, state: stateStore, recipeOps: operations,
		recipeFailures: newRecipeFailurePlan(map[recipeBoundary]int{boundary: 1}),
		recipeRevalidate: func(context.Context, localrecipes.Recipe, recipeops.Operation, string, map[string]string) error {
			return nil
		},
	}
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server.runLocalRecipe(job, recipe, run.ID)

	snapshot := job.Snapshot()
	if !snapshot.Done || snapshot.Phase != "error" || !strings.Contains(snapshot.Error, string(boundary)) {
		t.Fatalf("job after %s failure = %#v", boundary, snapshot)
	}
	persisted, ok := operations.Get(run.ID)
	if !ok || persisted.Phase != recipeops.PhaseFailed || !strings.Contains(persisted.Error, string(boundary)) {
		t.Fatalf("operation after %s failure = %#v, %v", boundary, persisted, ok)
	}
	if got := stateStore.Get().InferenceRuntime(); got != previous {
		t.Fatalf("%s rollback state = %#v, want %#v", boundary, got, previous)
	}
	for _, resource := range persisted.Resources {
		if resource.RequiresCleanup() {
			t.Fatalf("%s left runtime cleanup claim %#v", boundary, resource)
		}
	}
	retained := map[string]bool{}
	for _, resource := range persisted.Resources {
		retained[resource.Kind] = true
	}
	if !retained["image"] || !retained["model-cache"] {
		t.Fatalf("%s discarded reusable artifacts: %#v", boundary, persisted.Resources)
	}
}

func prepareRecipeMatrixPreflight(t *testing.T, operations *recipeops.Store, recipe localrecipes.Recipe, digest string) {
	t.Helper()
	check, err := operations.Begin(recipeops.KindCheck, recipe, recipeops.PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operations.RecordCheck(check.ID, recipeops.CheckResult{ID: "contract", Status: recipeops.CheckPass, Summary: "contract preserved"}); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.BindPreflight(check.ID, digest, "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Transition(check.ID, recipeops.PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}
}

func reserveRecipeMatrixPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func writeRecipeMatrixLifecycle(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pid := filepath.Join(dir, "runtime.pid")
	start := filepath.Join(dir, "start")
	stop := filepath.Join(dir, "stop")
	startScript := fmt.Sprintf("#!/bin/sh\nnohup %s -test.run=TestRecipeLifecycleHTTPHelper -- >%s 2>&1 &\necho $! >%s\n", recipeShellQuote(binary), recipeShellQuote(filepath.Join(dir, "runtime.log")), recipeShellQuote(pid))
	stopScript := fmt.Sprintf("#!/bin/sh\nif test -f %s; then\n  pid=$(cat %s)\n  kill \"$pid\" 2>/dev/null || true\n  i=0\n  while kill -0 \"$pid\" 2>/dev/null && test \"$i\" -lt 40; do sleep 0.05; i=$((i+1)); done\n  kill -9 \"$pid\" 2>/dev/null || true\n  rm -f %s\nfi\n", recipeShellQuote(pid), recipeShellQuote(pid), recipeShellQuote(pid))
	if err := os.WriteFile(start, []byte(startScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stop, []byte(stopScript), 0o700); err != nil {
		t.Fatal(err)
	}
	return start, stop
}

func writeRecipeMatrixDocker(t *testing.T, modelRoot, digest string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	image := `{"Id":"` + digest + `","RepoDigests":[],"Os":"linux","Architecture":"` + runtime.GOARCH + `","Size":4096,"Config":{"Entrypoint":[],"Cmd":[]}}`
	script := fmt.Sprintf("#!/bin/sh\nif test \"$1 $2\" = \"image inspect\"; then printf '%%s\\n' %s; exit 0; fi\nif test \"$1 $2\" = \"volume inspect\"; then printf '%%s\\n' %s; exit 0; fi\nexit 0\n", recipeShellQuote(image), recipeShellQuote(modelRoot))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeRecipeMatrixModelCache(t *testing.T, recipe localrecipes.Recipe, root string) {
	t.Helper()
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok {
		t.Fatal("test model ID did not produce a cache name")
	}
	repository := filepath.Join(root, "hub", cacheName)
	snapshot := filepath.Join(repository, "snapshots", recipe.Model.Revision)
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "config.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := buildRecipeArtifactManifest(repository, snapshot, recipe.Model.ID, recipe.Model.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveRecipeArtifactManifest(recipeModelCompleteMarker(recipe, root), manifest); err != nil {
		t.Fatal(err)
	}
}
