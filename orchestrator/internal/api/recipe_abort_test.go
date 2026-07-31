package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

type recipeAbortEngine struct{ engine.Engine }

func (e *recipeAbortEngine) Find(context.Context, string) (*engine.Container, error) {
	return nil, nil
}

func (e *recipeAbortEngine) Output(context.Context, ...string) (string, error) {
	return "", nil
}

func (e *recipeAbortEngine) ContainerNamesByLabel(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func (e *recipeAbortEngine) ImageDigest(_ context.Context, image string) (string, error) {
	return image, nil
}
func (e *recipeAbortEngine) VolumeMountpoint(context.Context, string) (string, error) {
	return "", context.Canceled
}
func (e *recipeAbortEngine) InspectImage(context.Context, string) (engine.ImageInfo, error) {
	return engine.ImageInfo{
		ID: "sha256:" + strings.Repeat("a", 64), OS: "linux", Architecture: runtime.GOARCH, Size: 4096,
	}, nil
}

type recipeOwnedContainerEngine struct {
	engine.Engine
	removed []string
}

func (e *recipeOwnedContainerEngine) Output(_ context.Context, args ...string) (string, error) {
	if len(args) >= 2 && args[0] == "ps" {
		return "gpu-container-a\ngpu-container-b\n", nil
	}
	return "", nil
}

func (e *recipeOwnedContainerEngine) ContainerNamesByLabel(context.Context, string, string) ([]string, error) {
	return []string{"gpu-container-a", "gpu-container-b"}, nil
}

func (e *recipeOwnedContainerEngine) Remove(_ context.Context, name string) error {
	e.removed = append(e.removed, name)
	return nil
}
func (e *recipeOwnedContainerEngine) VolumeMountpoint(context.Context, string) (string, error) {
	return "", context.Canceled
}

func TestRecipeAbortReturnsTrackableCleanupAndTransitionsStopping(t *testing.T) {
	operations, err := recipeops.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{ID: "local-0123456789abcdef", Name: "abort", Engine: localrecipes.Engine{ContainerPort: 8890}, Model: localrecipes.Model{ID: "owner/model", Revision: "rev"}, Runtime: localrecipes.Runtime{TimeoutMinutes: 1}}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server := &Server{recipeOps: operations, recipeJobs: map[string]recipeJobControl{recipe.ID: {operationID: operation.ID, cancel: cancel, job: job}}}
	request := httptest.NewRequest(http.MethodPost, "/api/recipes/"+recipe.ID+"/abort", nil).WithContext(ctx)
	request.SetPathValue("id", recipe.ID)
	response := httptest.NewRecorder()
	server.localRecipeAbort(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("abort status = %d: %s", response.Code, response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["jobId"] != job.ID || body["operationId"] != operation.ID {
		t.Fatalf("abort response = %#v", body)
	}
	persisted, _ := operations.Get(operation.ID)
	if persisted.Phase != recipeops.PhaseStopping {
		t.Fatalf("abort phase = %s", persisted.Phase)
	}
}

func TestRecipeAbortRejectsStaleJobOperationBinding(t *testing.T) {
	operations, err := recipeops.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{ID: "local-0123456789abcdef", Name: "abort"}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server := &Server{recipeOps: operations, recipeJobs: map[string]recipeJobControl{
		recipe.ID: {operationID: "rop-stale", cancel: cancel, job: job},
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/recipes/"+recipe.ID+"/abort", nil)
	request.SetPathValue("id", recipe.ID)
	response := httptest.NewRecorder()
	server.localRecipeAbort(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale abort status = %d: %s", response.Code, response.Body.String())
	}
	select {
	case <-ctx.Done():
		t.Fatal("stale operation binding cancelled a different run")
	default:
	}
	persisted, _ := operations.Get(operation.ID)
	if persisted.Phase != recipeops.PhasePreparing {
		t.Fatalf("active operation changed to %s", persisted.Phase)
	}
}

func TestFinishAbortedRecipeReleasesRuntimeClaimsAndRetainsReusableArtifacts(t *testing.T) {
	operations, err := recipeops.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "abort",
		Engine: localrecipes.Engine{ContainerPort: 8890},
		Model:  localrecipes.Model{ID: "owner/model", Revision: "revision"},
	}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	runtimeResources := []recipeops.Resource{
		{Kind: "container-set", ID: operation.ID, Node: localRecipeNodeName()},
		{Kind: "private-port", ID: strconv.Itoa(port), Node: localRecipeNodeName()},
		{Kind: "process-set", ID: operation.ID, Node: localRecipeNodeName()},
	}
	retainedResources := []recipeops.Resource{
		{Kind: "image", ID: "sha256:" + strings.Repeat("a", 64), Node: localRecipeNodeName()},
		{Kind: "model-cache", ID: "owner/model@revision", Node: localRecipeNodeName()},
	}
	for _, resource := range append(runtimeResources, retainedResources...) {
		if _, err := operations.Claim(operation.ID, resource); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := operations.Transition(operation.ID, recipeops.PhaseStopping, nil); err != nil {
		t.Fatal(err)
	}
	job := jobs.NewManager().Create("recipe:" + recipe.ID)
	server := &Server{eng: &recipeAbortEngine{}, recipeOps: operations}
	server.finishRecipeOperation(job, operation.ID, context.Canceled)
	persisted, _ := operations.Get(operation.ID)
	if persisted.Phase != recipeops.PhaseAborted {
		t.Fatalf("abort phase = %s, error = %s", persisted.Phase, persisted.Error)
	}
	for _, resource := range runtimeResources {
		for _, remaining := range persisted.Resources {
			if remaining == resource {
				t.Fatalf("runtime claim was not released: %#v", resource)
			}
		}
	}
	for _, resource := range retainedResources {
		found := false
		for _, remaining := range persisted.Resources {
			found = found || remaining == resource
		}
		if !found {
			t.Fatalf("reusable artifact was discarded: %#v", resource)
		}
	}
	if snapshot := job.Snapshot(); !snapshot.Done || snapshot.Phase != "canceled" {
		t.Fatalf("abort job = %#v", snapshot)
	}
}

func TestAbortCleanupRemovesEveryOperationOwnedGPUContainer(t *testing.T) {
	containerEngine := &recipeOwnedContainerEngine{}
	server := &Server{eng: containerEngine}
	operation := recipeops.Operation{
		ID: "rop-owned-containers",
		Resources: []recipeops.Resource{
			{Kind: "container-set", ID: "rop-owned-containers", Node: localRecipeNodeName()},
		},
	}
	if err := server.removeRecoveryContainers(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if len(containerEngine.removed) != 2 || !containsString(containerEngine.removed, "gpu-container-a") || !containsString(containerEngine.removed, "gpu-container-b") {
		t.Fatalf("owned GPU containers were not all removed: %#v", containerEngine.removed)
	}
}
