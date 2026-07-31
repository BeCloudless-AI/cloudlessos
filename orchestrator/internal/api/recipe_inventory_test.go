package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestLocalProcessInventoryFindsOperationEnvironment(t *testing.T) {
	operationID := "rop-0123456789abcdef0123456789abcdef"
	command := exec.Command("sleep", "10")
	command.Env = append(os.Environ(), "CLOUDLESS_RECIPE_OPERATION_ID="+operationID)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	deadline := time.Now().Add(time.Second)
	for {
		observation := inspectLocalRecipeProcesses(operationID)
		if observation.Presence == recipeops.PresencePresent {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process inventory = %#v", observation)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRecipeInventoryReportsLocalAndPeerOwnershipWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	recipes := localrecipes.New(dir)
	draft := localrecipes.NewDraft()
	draft.Source = localrecipes.Source{}
	draft.Distributed.Nodes = 1
	recipe, err := recipes.Create(draft)
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
	for _, resource := range []recipeops.Resource{
		{Kind: "checkout", ID: dir, Node: localRecipeNodeName()},
		{Kind: "checkout", ID: "/peer/runtime", Node: "peer-spark"},
	} {
		if _, err := operations.Claim(operation.ID, resource); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{recipes: recipes, recipeOps: operations}
	request := httptest.NewRequest(http.MethodGet, "/api/recipes/"+recipe.ID+"/inventory?operation="+operation.ID, nil)
	request.SetPathValue("id", recipe.ID)
	recorder := httptest.NewRecorder()
	server.localRecipeInventory(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("inventory = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		`"operation":{"id":"` + operation.ID + `"`,
		`"presence":"present","detail":"directory exists"`,
		`"presence":"unknown","detail":"peer locator was not journaled"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("inventory response is missing %q:\n%s", want, body)
		}
	}
	if current, ok := operations.Get(operation.ID); !ok || len(current.Resources) != 2 {
		t.Fatalf("read-only inventory mutated operation: %#v, %v", current, ok)
	}
}

func TestRecipeInventoryRejectsUnknownOperation(t *testing.T) {
	dir := t.TempDir()
	recipes := localrecipes.New(dir)
	draft := localrecipes.NewDraft()
	draft.Source = localrecipes.Source{}
	draft.Distributed.Nodes = 1
	recipe, err := recipes.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := recipeops.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{recipes: recipes, recipeOps: operations}
	request := httptest.NewRequest(http.MethodGet, "/api/recipes/"+recipe.ID+"/inventory?operation=rop-missing", nil)
	request.SetPathValue("id", recipe.ID)
	recorder := httptest.NewRecorder()
	server.localRecipeInventory(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("inventory = %d: %s", recorder.Code, recorder.Body.String())
	}
}
