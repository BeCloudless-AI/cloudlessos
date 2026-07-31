package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestRecipeCleanupRetryReleasesVerifiedMissingResources(t *testing.T) {
	dir := t.TempDir()
	recipes := localrecipes.New(dir)
	recipe, err := recipes.Create(localrecipes.NewDraft())
	if err != nil {
		t.Fatal(err)
	}
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	resource := recipeops.Resource{Kind: "process-set", ID: operation.ID, Node: localRecipeNodeName()}
	if _, err := operations.Claim(operation.ID, resource); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Transition(operation.ID, recipeops.PhaseFailed, errors.New("launch failed")); err != nil {
		t.Fatal(err)
	}
	server := &Server{jobs: jobs.NewManager(), recipes: recipes, recipeOps: operations}
	request := httptest.NewRequest(http.MethodPost, "/api/recipes/"+recipe.ID+"/cleanup/"+operation.ID, nil)
	request.SetPathValue("id", recipe.ID)
	request.SetPathValue("operation", operation.ID)
	response := httptest.NewRecorder()
	server.localRecipeCleanupRetry(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("cleanup response = %d: %s", response.Code, response.Body.String())
	}
	var accepted map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	job, ok := server.jobs.Get(accepted["jobId"])
	if !ok {
		t.Fatal("cleanup job was not created")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !job.Snapshot().Done && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := job.Snapshot(); !snapshot.Done || snapshot.Phase == "error" {
		t.Fatalf("cleanup job = %#v", snapshot)
	}
	cleaned, ok := operations.Get(operation.ID)
	if !ok || cleaned.Phase != recipeops.PhaseFailed || len(operationCleanupResources(cleaned)) != 0 || cleaned.Error != "launch failed" {
		t.Fatalf("cleaned operation = %#v", cleaned)
	}
}

func TestRecipeCleanupRetryRejectsOperationWithoutObligations(t *testing.T) {
	dir := t.TempDir()
	recipes := localrecipes.New(dir)
	recipe, err := recipes.Create(localrecipes.NewDraft())
	if err != nil {
		t.Fatal(err)
	}
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Transition(operation.ID, recipeops.PhaseFailed, errors.New("launch failed")); err != nil {
		t.Fatal(err)
	}
	server := &Server{jobs: jobs.NewManager(), recipes: recipes, recipeOps: operations}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("id", recipe.ID)
	request.SetPathValue("operation", operation.ID)
	response := httptest.NewRecorder()
	server.localRecipeCleanupRetry(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("cleanup response = %d: %s", response.Code, response.Body.String())
	}
}
